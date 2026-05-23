package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hanzitrack/backend/devops"
	"hanzitrack/backend/llm"
)

const mutateResponseAddButton = `{
  "files": {
    "app.js": "function greet(name) { return 'hello ' + name; }",
    "index.html": "<!doctype html><html><body><p>hi</p></body></html>"
  },
  "summary": "Added a re-categorize button to each vocab card."
}`

func TestMutate_HappyPath_ModifiesFilesAndLogsJob(t *testing.T) {
	frontendDir, cleanup := fakeFrontend(t)
	defer cleanup()

	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: mutateResponseAddButton}}
	reg := llm.NewRegistry()
	reg.Register("anthropic", fake)

	m := &devops.Mutator{
		DB:          d,
		Registry:    reg,
		FrontendDir: frontendDir,
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-20250514",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/devops/mutate", Mutate(m))

	body := `{"feature_description": "Add a re-categorize button"}`
	req := httptest.NewRequest(http.MethodPost, "/api/devops/mutate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp mutateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.JobID == 0 {
		t.Error("expected non-zero job_id")
	}
	if resp.Status != "completed" {
		t.Errorf("status=%s, want completed", resp.Status)
	}
	if len(resp.FilesChanged) != 2 {
		t.Errorf("files_changed=%v, want 2 files", resp.FilesChanged)
	}
	if resp.Summary == "" {
		t.Error("summary is empty")
	}

	// Verify mutation_jobs was logged.
	var status string
	var userReq string
	if err := d.QueryRow(`SELECT status, user_request FROM mutation_jobs WHERE id = ?`, resp.JobID).
		Scan(&status, &userReq); err != nil {
		t.Fatalf("read job: %v", err)
	}
	if status != "completed" {
		t.Errorf("job status=%s, want completed", status)
	}
	if userReq != "Add a re-categorize button" {
		t.Errorf("user_request=%q", userReq)
	}

	// Verify files were actually written.
	data, err := os.ReadFile(filepath.Join(frontendDir, "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "function greet(name) { return 'hello ' + name; }" {
		t.Errorf("app.js content=%q", string(data))
	}
	data, err = os.ReadFile(filepath.Join(frontendDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "<!doctype html><html><body><p>hi</p></body></html>" {
		t.Errorf("index.html content=%q", string(data))
	}
}

func TestMutate_NoFilesChanged_ReturnsSuccessWithNoChanges(t *testing.T) {
	frontendDir, cleanup := fakeFrontend(t)
	defer cleanup()

	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: `{"files": {}, "summary": "No changes needed."}`}}
	reg := llm.NewRegistry()
	reg.Register("anthropic", fake)

	m := &devops.Mutator{
		DB:          d,
		Registry:    reg,
		FrontendDir: frontendDir,
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-20250514",
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/devops/mutate", Mutate(m))

	body := `{"feature_description": "Do nothing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/devops/mutate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp mutateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != "completed" {
		t.Errorf("status=%s, want completed", resp.Status)
	}
	if len(resp.FilesChanged) != 0 {
		t.Errorf("files_changed=%v, want empty", resp.FilesChanged)
	}
}

func TestMutate_EmptyDescription_Returns400(t *testing.T) {
	d := freshDB(t)
	reg := llm.NewRegistry()

	m := &devops.Mutator{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/devops/mutate", Mutate(m))

	body := `{"feature_description": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/devops/mutate", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", rec.Code)
	}
}

func TestMutate_DisallowedFile_ReturnsError(t *testing.T) {
	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: `{"files": {"main.go": "package main"}, "summary": "bad"}`}}
	reg := llm.NewRegistry()
	reg.Register("anthropic", fake)

	m := &devops.Mutator{
		DB:          d,
		Registry:    reg,
		FrontendDir: t.TempDir(),
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-20250514",
	}

	result, err := m.Mutate(context.Background(), "hack the backend")
	if err != nil {
		t.Fatalf("Mutate returned err (should be in result): %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status=%s, want failed", result.Status)
	}
	if !strings.Contains(result.Error, "disallowed") {
		t.Errorf("error=%q, want disallowed file error", result.Error)
	}
}

func TestMutate_NonexistentFrontendDir_ReturnsFailed(t *testing.T) {
	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: mutateResponseAddButton}}
	reg := llm.NewRegistry()
	reg.Register("anthropic", fake)

	m := &devops.Mutator{
		DB:          d,
		Registry:    reg,
		FrontendDir: "/nonexistent/path",
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-20250514",
	}

	result, err := m.Mutate(context.Background(), "add a button")
	if err != nil {
		t.Fatalf("Mutate got err (should be in result): %v", err)
	}
	if result.Status != "failed" {
		t.Errorf("status=%s, want failed", result.Status)
	}
}

func TestMutate_MalformedLLMResponse_ReturnsFailed(t *testing.T) {
	frontendDir, cleanup := fakeFrontend(t)
	defer cleanup()

	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: "this is not json at all"}}
	reg := llm.NewRegistry()
	reg.Register("anthropic", fake)

	m := &devops.Mutator{
		DB:          d,
		Registry:    reg,
		FrontendDir: frontendDir,
		Provider:    "anthropic",
		Model:       "claude-sonnet-4-20250514",
	}

	result, _ := m.Mutate(context.Background(), "add a button")
	if result.Status != "failed" {
		t.Errorf("status=%s, want failed", result.Status)
	}
}

// Test helpers.

func fakeFrontend(t *testing.T) (dir string, cleanup func()) {
	t.Helper()
	dir = t.TempDir()
	for name, content := range map[string]string{
		"app.js":     "// original app.js",
		"index.html": "<!-- original index.html -->",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir, func() {}
}

