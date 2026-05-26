// Package devops implements the Self-Mutation Engineering Loop (Loop 2).
// The Mutator reads frontend source files, calls an LLM to edit them based
// on a user feature request, applies the changes, validates, and pushes to
// a feature branch with a pull request.
package devops

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"hanzitrack/backend/llm"
)

// AllowedFiles is the whitelist of filenames the mutator may edit.
var AllowedFiles = map[string]bool{
	"app.js":     true,
	"index.html": true,
	"styles.css": true,
}

// MutationResult is returned by Mutator.Mutate.
type MutationResult struct {
	JobID        int64    `json:"job_id"`
	Status       string   `json:"status"`
	FilesChanged []string `json:"files_changed,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	Branch       string   `json:"branch,omitempty"`
	PRURL        string   `json:"pr_url,omitempty"`
	Error        string   `json:"error,omitempty"`
}

// MutationEvent is emitted by the Mutator during the mutation cycle for SSE streaming.
type MutationEvent struct {
	Step    string `json:"step"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

// Mutator reads frontend source files, calls an LLM to edit them,
// validates, commits to a feature branch, and creates a PR.
type Mutator struct {
	DB          *sql.DB
	Registry    *llm.Registry
	FrontendDir string // path to frontend/ directory
	RepoDir     string // path to git repo root (defaults to FrontendDir/..)
	Provider    string
	Model       string
	MaxAttempts int // max LLM attempts including auto-correction, default 3

	// OnEvent is called at each stage of mutation for SSE streaming.
	// If set, each call blocks the mutation pipeline — the handler should
	// write the event to the HTTP response and flush immediately.
	OnEvent func(MutationEvent)
}

// emit sends a MutationEvent through the callback, if set.
func (m *Mutator) emit(step, message, detail string) {
	if m.OnEvent != nil {
		m.OnEvent(MutationEvent{Step: step, Message: message, Detail: detail})
	}
}

// validateExec is exec.CommandContext, replaced in tests.
var validateExec = exec.CommandContext

// gitExec is exec.CommandContext, replaced in tests.
var gitExec = exec.CommandContext

// Mutate performs one self-mutation cycle: read sources, call LLM, write,
// validate, auto-correct if needed, git commit, push, and create PR.
func (m *Mutator) Mutate(ctx context.Context, featureDesc string) (*MutationResult, error) {
	featureDesc = strings.TrimSpace(featureDesc)
	if featureDesc == "" {
		return nil, fmt.Errorf("feature description is required")
	}

	maxAttempts := m.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 3
	}

	branchName := slugify(featureDesc)
	targetBranch := "feature/" + branchName

	// Create the job row.
	res, err := m.DB.ExecContext(ctx,
		`INSERT INTO mutation_jobs (user_request, target_branch, status)
		 VALUES (?, ?, 'processing')`,
		featureDesc, targetBranch)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	jobID, _ := res.LastInsertId()

	setStatus := func(status, errLog string) {
		if uerr := m.updateJobStatus(ctx, jobID, status, errLog); uerr != nil {
			log.Printf("mutator: update job %d: %v", jobID, uerr)
		}
	}

	// Read source files.
	m.emit("reading_source", "Reading source files...", "")
	source, err := m.readSource()
	if err != nil {
		setStatus("failed", fmt.Sprintf("read source: %v", err))
		m.emit("error", "Failed to read source", err.Error())
		return &MutationResult{JobID: jobID, Status: "failed", Error: err.Error()}, nil
	}

	m.emit("consulting_llm", "Consulting LLM engineer...", "")
	// Get LLM client once.
	client, err := m.Registry.Get(m.Provider)
	if err != nil {
		setStatus("failed", fmt.Sprintf("provider %q: %v", m.Provider, err))
		return &MutationResult{JobID: jobID, Status: "failed", Error: err.Error()}, nil
	}

	var (
		changed  map[string]string
		summary  string
		lastErr  string
		restore  func()
	)

	llmPrompt := buildMutationPrompt(source, featureDesc)

	for attempt := 0; attempt < maxAttempts; attempt++ {
		currentPrompt := llmPrompt
		if attempt > 0 && lastErr != "" {
			currentPrompt = llmPrompt + "\n\nPREVIOUS ATTEMPT ERROR:\n" + lastErr +
				"\n\nFix the syntax error above and try again. Return the COMPLETE corrected files."
		}

		llmReq := llm.Request{
			Model:       m.Model,
			System:      "You are a senior frontend engineer modifying a Chinese vocabulary tracker SPA. Output only valid JSON.",
			Messages:    []llm.Message{{Role: llm.RoleUser, Content: currentPrompt}},
			Temperature: 0.2,
			MaxTokens:   16384,
		}

		resp, llmErr := client.Complete(ctx, llmReq)
		m.emit("consulting_llm", "Contacting engineer API...", "")
		if llmErr != nil {
			lastErr = fmt.Sprintf("LLM call: %v", llmErr)
			if attempt < maxAttempts-1 {
				continue
			}
			setStatus("failed", lastErr)
			m.emit("error", "LLM call failed", lastErr)
			return &MutationResult{JobID: jobID, Status: "failed", Error: lastErr}, nil
		}

		changed, summary, err = parseMutationResponse(resp.Content, source)
		if err != nil {
			lastErr = fmt.Sprintf("parse: %v", err)
			if attempt < maxAttempts-1 {
				continue
			}
			setStatus("failed", lastErr)
			return &MutationResult{JobID: jobID, Status: "failed", Error: lastErr}, nil
		}

		if len(changed) == 0 {
			setStatus("completed", "")
			return &MutationResult{JobID: jobID, Status: "completed", Summary: summary}, nil
		}

		m.emit("writing_files", "Writing code updates...", "")
		restore, err = m.backupAndWrite(changed)
		if err != nil {
			lastErr = fmt.Sprintf("write: %v", err)
			if attempt < maxAttempts-1 {
				continue
			}
			setStatus("failed", lastErr)
			m.emit("error", "Failed to write files", lastErr)
			return &MutationResult{JobID: jobID, Status: "failed", Error: lastErr}, nil
		}

		// Validate.
		m.emit("validating", "Running validation checks...", "")
		valErr := m.validateFiles(changed)
		if valErr == nil {
			restore = nil // success — discard backup
			break
		}

		lastErr = fmt.Sprintf("validation: %v", valErr)

		// Auto-correction: restore originals for the next attempt.
		if restore != nil {
			restore()
			restore = nil
		}

		if attempt == maxAttempts-1 {
			setStatus("failed_validation", lastErr)
			return &MutationResult{JobID: jobID, Status: "failed_validation", Error: lastErr}, nil
		}
	}

	// Git pipeline.
	setStatus("lint_passed", "")
	var prURL string
	if m.RepoDir != "" {
		prURL, err = m.runGitPipeline(ctx, featureDesc, branchName)
		if err != nil {
			// Git failure is not fatal — files are written and backed up.
			log.Printf("mutator: git pipeline: %v", err)
			setStatus("completed", fmt.Sprintf("git: %v", err))
		} else {
			setStatus("pr_created", "")
		}
	}

	var fileNames []string
	for name := range changed {
		fileNames = append(fileNames, name)
	}

	status := "completed"
	if prURL != "" {
		status = "pr_created"
	}
	setStatus(status, "")
	m.emit("done", "Mutation complete", "")

	return &MutationResult{
		JobID:        jobID,
		Status:       status,
		FilesChanged: fileNames,
		Summary:      summary,
		Branch:       targetBranch,
		PRURL:        prURL,
	}, nil
}

// readSource reads all AllowedFiles from the frontend directory.
func (m *Mutator) readSource() (map[string]string, error) {
	source := map[string]string{}
	for name := range AllowedFiles {
		path := filepath.Join(m.FrontendDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		source[name] = string(data)
	}
	return source, nil
}

// backupAndWrite saves originals to a temp dir, then writes new content.
// Returns a restore function that can be called to undo the writes.
func (m *Mutator) backupAndWrite(files map[string]string) (func(), error) {
	backupDir, err := os.MkdirTemp("", "hanzitrack-mutator-*")
	if err != nil {
		return nil, fmt.Errorf("backup dir: %w", err)
	}

	for name := range files {
		src := filepath.Join(m.FrontendDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			os.RemoveAll(backupDir)
			return nil, fmt.Errorf("backup %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(backupDir, name), data, 0644); err != nil {
			os.RemoveAll(backupDir)
			return nil, fmt.Errorf("backup write %s: %w", name, err)
		}
	}

	for name, content := range files {
		dst := filepath.Join(m.FrontendDir, name)
		if err := os.WriteFile(dst, []byte(content), 0644); err != nil {
			m.restoreFrom(backupDir, files)
			os.RemoveAll(backupDir)
			return nil, fmt.Errorf("write %s: %w", name, err)
		}
	}

	restore := func() {
		m.restoreFrom(backupDir, files)
		os.RemoveAll(backupDir)
	}
	return restore, nil
}

func (m *Mutator) restoreFrom(backupDir string, files map[string]string) {
	for name := range files {
		src := filepath.Join(backupDir, name)
		dst := filepath.Join(m.FrontendDir, name)
		data, err := os.ReadFile(src)
		if err != nil {
			log.Printf("mutator: restore read %s: %v", name, err)
			continue
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			log.Printf("mutator: restore write %s: %v", name, err)
		}
	}
}

// validateFiles runs basic syntax checks on modified files.
func (m *Mutator) validateFiles(files map[string]string) error {
	for name := range files {
		path := filepath.Join(m.FrontendDir, name)
		switch filepath.Ext(name) {
		case ".js":
			// node --check verifies JS syntax without executing.
			// If node isn't available, skip JS validation.
			cmd := validateExec(context.Background(), "node", "--check", path)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Run(); err != nil {
				// Check if node is missing vs. a syntax error.
				if _, isPathErr := err.(*exec.Error); isPathErr {
					log.Printf("mutator: node not available, skipping JS validation")
					continue
				}
				return fmt.Errorf("%s syntax: %s", name, strings.TrimSpace(stderr.String()))
			}
		case ".html":
			// Basic structural check — ensure the file is not empty
			// and contains required elements.
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", name, err)
			}
			content := string(data)
			if !strings.Contains(content, "<html") && !strings.Contains(content, "<!doctype") {
				return fmt.Errorf("%s: missing HTML structure", name)
			}
			if !strings.Contains(content, "</body>") {
				return fmt.Errorf("%s: missing </body>", name)
			}
		}
	}
	return nil
}

func (m *Mutator) repoDir() string {
	if m.RepoDir != "" {
		return m.RepoDir
	}
	return filepath.Dir(m.FrontendDir)
}

// runGitPipeline creates a feature branch, commits, pushes, and creates a PR.
func (m *Mutator) runGitPipeline(ctx context.Context, featureDesc, branchSlug string) (string, error) {
	repo := m.repoDir()
	branch := "feature/" + branchSlug

	m.emit("creating_branch", "Creating feature branch...", branch)
	if out, err := runGit(ctx, repo, "checkout", "-b", branch); err != nil {
		return "", fmt.Errorf("git checkout -b: %s: %w", out, err)
	}

	rel, _ := filepath.Rel(repo, m.FrontendDir)
	if out, err := runGit(ctx, repo, "add", rel); err != nil {
		runGit(ctx, repo, "checkout", "-")
		return "", fmt.Errorf("git add: %s: %w", out, err)
	}

	m.emit("committing", "Committing changes...", "")
	msg := fmt.Sprintf("feat: %s", featureDesc)
	if out, err := runGit(ctx, repo, "commit", "-m", msg); err != nil {
		runGit(ctx, repo, "checkout", "-")
		return "", fmt.Errorf("git commit: %s: %w", out, err)
	}

	m.emit("pushing", "Pushing to remote...", branch)
	if out, err := runGit(ctx, repo, "push", "-u", "origin", branch); err != nil {
		return "", fmt.Errorf("git push: %s: %w", out, err)
	}

	m.emit("creating_pr", "Creating pull request...", "")
	prBody := fmt.Sprintf(`## What

%s

## Changes

- Self-mutated by HánzìTrack DevOps Engine

🤖 Generated by Loop 2`, featureDesc)
	prURL, err := runGH(ctx, repo, "pr", "create",
		"--title", msg,
		"--body", prBody,
		"--head", branch)
	if err != nil {
		return "", fmt.Errorf("gh pr create: %s: %w", prURL, err)
	}

	return strings.TrimSpace(prURL), nil
}

func runGit(ctx context.Context, repoDir string, args ...string) (string, error) {
	cmd := gitExec(ctx, "git", args...)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String() + stderr.String())
	return out, err
}

func runGH(ctx context.Context, repoDir string, args ...string) (string, error) {
	cmd := gitExec(ctx, "gh", args...)
	cmd.Dir = repoDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String() + stderr.String())
	return out, err
}

func (m *Mutator) updateJobStatus(ctx context.Context, jobID int64, status, errLog string) error {
	_, err := m.DB.ExecContext(ctx,
		`UPDATE mutation_jobs SET status = ?, error_log = ? WHERE id = ?`,
		status, errLog, jobID)
	return err
}

// slugify converts text to a git-safe hyphenated string.
func slugify(s string) string {
	lower := strings.ToLower(s)
	// Replace runs of non-alphanumeric (except hyphens) with a single hyphen.
	re := regexp.MustCompile(`[^a-z0-9]+`)
	slug := re.ReplaceAllString(lower, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 60 {
		slug = slug[:60]
	}
	slug = strings.TrimRight(slug, "-")
	if slug == "" {
		slug = "feature"
	}
	return slug
}

// mutationResponse is the JSON shape the LLM is asked to produce.
type mutationResponse struct {
	Files   map[string]string `json:"files"`
	Summary string            `json:"summary"`
}

// buildMutationPrompt assembles the LLM prompt with source code and feature request.
func buildMutationPrompt(source map[string]string, featureDesc string) string {
	var b strings.Builder

	b.WriteString("You are modifying a Chinese vocabulary tracker SPA. The app uses:\n")
	b.WriteString("- Alpine.js 3.13 for reactive UI (loaded via CDN)\n")
	b.WriteString("- Tailwind CSS (loaded via CDN, no build step)\n")
	b.WriteString("- No build step — everything is plain HTML + JS\n")
	b.WriteString("- The app is served by a Go backend, but you only edit frontend files\n\n")

	b.WriteString("RULES:\n")
	b.WriteString("- You may ONLY modify these files: app.js, index.html, styles.css\n")
	b.WriteString("- Return the COMPLETE new content of each modified file, not a diff\n")
	b.WriteString("- Only include a file in the response if it actually changed\n")
	b.WriteString("- Do NOT change the tech stack or add build tools\n")
	b.WriteString("- Do NOT change or remove existing functionality unless the feature request explicitly asks for it\n")
	b.WriteString("- Style new UI elements consistently with existing Tailwind classes\n\n")

	b.WriteString("CURRENT APP.JS:\n")
	b.WriteString("```javascript\n")
	if src, ok := source["app.js"]; ok {
		b.WriteString(src)
	}
	b.WriteString("\n```\n\n")

	b.WriteString("CURRENT INDEX.HTML:\n")
	b.WriteString("```html\n")
	if src, ok := source["index.html"]; ok {
		b.WriteString(src)
	}
	b.WriteString("\n```\n\n")

	if src, ok := source["styles.css"]; ok {
		b.WriteString("CURRENT STYLES.CSS:\n")
		b.WriteString("```css\n")
		b.WriteString(src)
		b.WriteString("\n```\n\n")
	}

	fmt.Fprintf(&b, "FEATURE REQUEST: %s\n\n", featureDesc)

	b.WriteString(`Respond ONLY with a JSON object matching this exact shape — no markdown, no commentary:
{
  "files": {
    "app.js": "complete new content, only if changed",
    "index.html": "complete new content, only if changed"
  },
  "summary": "one-sentence description of what was changed"
}

If no files need to change to implement the feature, return {"files": {}, "summary": "No changes needed."}
`)

	return b.String()
}

// parseMutationResponse extracts modified files and summary from the LLM JSON.
func parseMutationResponse(content string, source map[string]string) (changed map[string]string, summary string, err error) {
	var resp mutationResponse
	if err := json.Unmarshal([]byte(content), &resp); err != nil {
		return nil, "", fmt.Errorf("invalid JSON: %w", err)
	}

	changed = map[string]string{}
	for name, newContent := range resp.Files {
		if !AllowedFiles[name] {
			return nil, "", fmt.Errorf("disallowed file: %s", name)
		}
		original, exists := source[name]
		if exists && original == newContent {
			continue
		}
		changed[name] = newContent
	}

	return changed, resp.Summary, nil
}
