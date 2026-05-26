package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"hanzitrack/backend/llm"
)

// Quiz handles the Quizmaster agent's HTTP surface. The DB and Registry
// fields wire the handler to the underlying SQLite store and the provider
// registry from issue 01.
type Quiz struct {
	DB       *sql.DB
	Registry *llm.Registry
}

// QuizQuestion mirrors the SPEC §6 JSON shape that the LLM is asked to
// produce. The frontend renders this directly. QuestionPinyin and
// OptionPinyins are optional — the frontend falls back to hanzi when absent.
type QuizQuestion struct {
	Structure       string   `json:"structure"`
	QuestionChinese string   `json:"question_chinese"`
	QuestionPinyin  string   `json:"question_pinyin,omitempty"`
	Options         []string `json:"options"`
	OptionPinyins   []string `json:"option_pinyins,omitempty"`
	CorrectAnswer   string   `json:"correct_answer"`
	VocabID         int64    `json:"vocab_id"`
	Explanation     string   `json:"explanation"`
}

type quizVocab struct {
	ID       int64
	Hanzi    string
	Pinyin   string
	English  string
	Attempts int
	Correct  int
}

// Generate handles GET /api/agents/quiz?category=<name>.
// Loads the quizmaster row, gathers vocab in the category with per-word
// accuracy from review_logs, asks the registered LLM client for a question.
// On malformed JSON, retries once; second failure surfaces 502.
func (q *Quiz) Generate(w http.ResponseWriter, r *http.Request) {
	category := strings.TrimSpace(r.URL.Query().Get("category"))

	agent, err := q.loadAgent(r.Context(), "quizmaster")
	if err != nil {
		log.Printf("quiz: load agent: %v", err)
		http.Error(w, "agent not configured", http.StatusInternalServerError)
		return
	}

	words, err := q.loadVocab(r.Context(), category)
	if err != nil {
		log.Printf("quiz: load vocab: %v", err)
		http.Error(w, "vocab lookup failed", http.StatusInternalServerError)
		return
	}
	if len(words) == 0 {
		http.Error(w, "no vocab in this category yet — add some words first", http.StatusBadRequest)
		return
	}

	client, err := q.Registry.Get(agent.Provider)
	if err != nil {
		log.Printf("quiz: provider %q: %v", agent.Provider, err)
		http.Error(w, "provider not registered", http.StatusInternalServerError)
		return
	}

	llmReq := llm.Request{
		Model:       agent.Model,
		System:      agent.SystemPrompt,
		Messages:    []llm.Message{{Role: llm.RoleUser, Content: buildQuizPrompt(category, words)}},
		Temperature: agent.Temperature,
		MaxTokens:   512,
	}

	question, attempts, err := generateWithRetry(r.Context(), client, llmReq, 2)
	if err != nil {
		log.Printf("quiz: llm after %d attempts: %v", attempts, err)
		http.Error(w, "agent failed to produce a valid question", http.StatusBadGateway)
		return
	}

	writeJSON(w, question)
}

// generateWithRetry calls the client up to maxAttempts times, parsing the
// JSON response into a QuizQuestion. Returns the parsed question on success.
// Used by the handler to retry once on malformed JSON per the issue-02
// sign-off; testable independently of HTTP machinery.
func generateWithRetry(ctx context.Context, client llm.Client, req llm.Request, maxAttempts int) (QuizQuestion, int, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		resp, err := client.Complete(ctx, req)
		if err != nil {
			lastErr = err
			continue
		}
		var q QuizQuestion
		if err := json.Unmarshal([]byte(resp.Content), &q); err != nil {
			lastErr = fmt.Errorf("malformed JSON: %w (content=%q)", err, resp.Content)
			continue
		}
		if q.QuestionChinese == "" || len(q.Options) == 0 || q.CorrectAnswer == "" {
			lastErr = fmt.Errorf("incomplete question: %+v", q)
			continue
		}
		return q, attempt, nil
	}
	return QuizQuestion{}, maxAttempts, lastErr
}

type submitRequest struct {
	VocabID   int64  `json:"vocab_id"`
	QuizType  string `json:"quiz_type"`
	IsCorrect bool   `json:"is_correct"`
}

type submitResponse struct {
	ID int64 `json:"id"`
}

// Submit handles POST /api/agents/quiz/submit — records a review_logs row.
func (q *Quiz) Submit(w http.ResponseWriter, r *http.Request) {
	var req submitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.VocabID == 0 || strings.TrimSpace(req.QuizType) == "" {
		http.Error(w, "vocab_id and quiz_type are required", http.StatusBadRequest)
		return
	}

	correct := 0
	if req.IsCorrect {
		correct = 1
	}
	res, err := q.DB.ExecContext(r.Context(),
		`INSERT INTO review_logs (vocab_id, agent_type, quiz_type, is_correct)
		 VALUES (?, 'quizmaster', ?, ?)`,
		req.VocabID, req.QuizType, correct)
	if err != nil {
		log.Printf("quiz: insert review_log: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, submitResponse{ID: id})
}

type loadedAgent struct {
	Name         string
	SystemPrompt string
	Temperature  float64
	Provider     string
	Model        string
}

func (q *Quiz) loadAgent(ctx context.Context, name string) (loadedAgent, error) {
	var a loadedAgent
	err := q.DB.QueryRowContext(ctx,
		`SELECT name, system_prompt, temperature, provider, model
		   FROM system_agents
		  WHERE name = ? AND is_active = 1`, name).
		Scan(&a.Name, &a.SystemPrompt, &a.Temperature, &a.Provider, &a.Model)
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("agent %q not active", name)
	}
	return a, err
}

func (q *Quiz) loadVocab(ctx context.Context, category string) ([]quizVocab, error) {
	// Group by vocab.id so the LEFT JOIN against review_logs (one row per
	// attempt) collapses into per-word totals. An empty category filter is
	// allowed — picks across the whole bank.
	args := []any{}
	join := ""
	where := ""
	if category != "" {
		join = `JOIN vocab_categories vc ON vc.vocab_id = v.id
		        JOIN categories c ON c.id = vc.category_id`
		where = "WHERE c.name = ?"
		args = append(args, category)
	}
	query := `SELECT v.id, v.hanzi, v.pinyin, v.english,
	                 COALESCE(COUNT(rl.id), 0) AS attempts,
	                 COALESCE(SUM(rl.is_correct), 0) AS correct
	            FROM vocabulary v
	            ` + join + `
	            LEFT JOIN review_logs rl ON rl.vocab_id = v.id
	            ` + where + `
	         GROUP BY v.id
	         ORDER BY v.id DESC
	            LIMIT 50`

	rows, err := q.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []quizVocab{}
	for rows.Next() {
		var v quizVocab
		if err := rows.Scan(&v.ID, &v.Hanzi, &v.Pinyin, &v.English, &v.Attempts, &v.Correct); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// buildQuizPrompt assembles the user-turn payload. Words are listed with
// their per-word accuracy so the LLM can lean on weak words. The required
// JSON shape is restated inline because system prompts in system_agents
// are mutable and may drift from the wire contract.
func buildQuizPrompt(category string, words []quizVocab) string {
	var b strings.Builder
	if category != "" {
		fmt.Fprintf(&b, "Category: %s\n\n", category)
	}
	b.WriteString("Vocabulary (hanzi | pinyin | english | attempts | correct):\n")
	for _, w := range words {
		fmt.Fprintf(&b, "- %s | %s | %s | %d | %d\n", w.Hanzi, w.Pinyin, w.English, w.Attempts, w.Correct)
	}
	b.WriteString("\nPick ONE vocabulary entry from the list above (prefer words with low accuracy) ")
	b.WriteString("and construct a fill-in-the-blank structural question around a common Chinese ")
	b.WriteString("sentence skeleton (e.g. 虽然...但是..., 一边...一边..., Subject + Time + Place + Verb).\n\n")
	b.WriteString("Respond ONLY with a JSON object matching this exact shape — no markdown, no commentary:\n")
	b.WriteString(`{
  "structure": "<sentence-skeleton name>",
  "question_chinese": "<full sentence with ____ where the blank goes>",
  "question_pinyin": "<question_chinese with all characters replaced by pinyin, e.g. ta xi3huan1 yi4bian1 ____ yi4bian1 ting1 yin1yue4>",
  "options": ["<correct hanzi>", "<distractor 1>", "<distractor 2>", "<distractor 3>"],
  "option_pinyins": ["<option 0 in numeric pinyin>", "<option 1 in numeric pinyin>", "<option 2 in numeric pinyin>", "<option 3 in numeric pinyin>"],
  "correct_answer": "<exact match for the correct option>",
  "vocab_id": <integer id of the chosen vocab row>,
  "explanation": "<one-sentence explanation in English>"
}`)
	b.WriteString("\n\nAll four options must be distinct. correct_answer must equal one of the options exactly. vocab_id must come from the list.")
	b.WriteString(" question_pinyin and option_pinyins are optional — omit them if the question doesn't have hanzi characters.")
	return b.String()
}
