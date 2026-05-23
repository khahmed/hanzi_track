package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hanzitrack/backend/agents"
	"hanzitrack/backend/llm"
)

const tuneResponseJSON = `{
  "revised_prompt": "You are an expert Chinese teacher. Challenge the user with harder sentence structures.",
  "rationale": "User accuracy is high, so increasing difficulty."
}`

func TestStats_EmptyDB_ReturnsEmptyResults(t *testing.T) {
	d := freshDB(t)
	reg := llm.NewRegistry()
	reg.Register("deepseek", &llm.Fake{Response: llm.Response{Content: tuneResponseJSON}})

	orch := &agents.Orchestrator{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/stats", Stats(orch))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		Results []agents.AgentStats `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 0 {
		t.Errorf("got %d results, want 0", len(resp.Results))
	}
}

func TestStats_WithReviewLogs_ReturnsAggregatedStats(t *testing.T) {
	d := freshDB(t)
	reg := llm.NewRegistry()
	reg.Register("deepseek", &llm.Fake{Response: llm.Response{Content: tuneResponseJSON}})

	// Seed vocabulary rows so review_logs FK constraints pass.
	for _, h := range []string{"朋友", "老师", "学校"} {
		if _, err := d.Exec(
			`INSERT INTO vocabulary (hanzi, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)`,
			h, "test", "test", "test"); err != nil {
			t.Fatalf("seed vocab: %v", err)
		}
	}

	// Insert review_logs rows with known accuracy.
	logs := []struct {
		agentType string
		correct   int
	}{
		{"quizmaster", 1},
		{"quizmaster", 1},
		{"quizmaster", 0},
		{"quizmaster", 1},
		{"quizmaster", 1},
		{"conversationalist", 0},
		{"conversationalist", 0},
		{"conversationalist", 1},
	}
	for i, l := range logs {
		vocabID := (i % 3) + 1
		if _, err := d.Exec(
			`INSERT INTO review_logs (vocab_id, agent_type, quiz_type, is_correct) VALUES (?, ?, ?, ?)`,
			vocabID, l.agentType, "structural_fill", l.correct); err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	orch := &agents.Orchestrator{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/stats", Stats(orch))

	req := httptest.NewRequest(http.MethodGet, "/api/agents/stats", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp struct {
		Results []agents.AgentStats `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d result groups, want 2", len(resp.Results))
	}

	// Find quizmaster stats.
	var qs *agents.AgentStats
	for i := range resp.Results {
		if resp.Results[i].AgentType == "quizmaster" {
			qs = &resp.Results[i]
			break
		}
	}
	if qs == nil {
		t.Fatal("quizmaster stats not found")
	}
	if qs.Total != 5 || qs.Correct != 4 || qs.Incorrect != 1 {
		t.Errorf("quizmaster: total=%d correct=%d incorrect=%d, want 5/4/1", qs.Total, qs.Correct, qs.Incorrect)
	}
	if qs.QuizType != "structural_fill" {
		t.Errorf("quiz_type=%s, want structural_fill", qs.QuizType)
	}
}

func TestOrchestrate_NoReviewLogs_LeavesPromptsUnchanged(t *testing.T) {
	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: tuneResponseJSON}}
	reg := llm.NewRegistry()
	reg.Register("deepseek", fake)

	orch := &agents.Orchestrator{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agents/orchestrate", Orchestrate(orch))

	req := httptest.NewRequest(http.MethodPost, "/api/agents/orchestrate", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var result agents.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Changes) != 2 {
		t.Fatalf("got %d changes, want 2 (quizmaster + conversationalist)", len(result.Changes))
	}
	// With no review data, the diff should show no change.
	for _, c := range result.Changes {
		if c.Changed {
			t.Errorf("%s was changed but shouldn't have been (no review data)", c.AgentName)
		}
	}
	// The LLM should not have been called (no review data means prompt says "leave unchanged").
	if fake.Calls != 0 {
		t.Errorf("llm was called %d times, want 0 (no review data means no LLM call needed)", fake.Calls)
	}
}

func TestOrchestrate_WithReviewLogs_CallsLLMAndUpdatesPrompts(t *testing.T) {
	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: tuneResponseJSON}}
	reg := llm.NewRegistry()
	reg.Register("deepseek", fake)

	// Seed vocab + review_logs.
	if _, err := d.Exec(
		`INSERT INTO vocabulary (hanzi, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)`,
		"朋友", "peng2 you5", "pengyou", "friend"); err != nil {
		t.Fatalf("seed vocab: %v", err)
	}
	for i := 0; i < 10; i++ {
		correct := 1
		if i > 7 {
			correct = 0 // last 2 wrong to create a declining trend
		}
		if _, err := d.Exec(
			`INSERT INTO review_logs (vocab_id, agent_type, quiz_type, is_correct) VALUES (?, ?, ?, ?)`,
			1, "quizmaster", "structural_fill", correct); err != nil {
			t.Fatalf("seed log: %v", err)
		}
	}

	orch := &agents.Orchestrator{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agents/orchestrate", Orchestrate(orch))

	req := httptest.NewRequest(http.MethodPost, "/api/agents/orchestrate", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var result agents.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Changes) != 2 {
		t.Fatalf("got %d changes, want 2", len(result.Changes))
	}

	// Quizmaster has review data so the LLM should have been called.
	if fake.Calls != 1 {
		t.Fatalf("llm calls = %d, want 1", fake.Calls)
	}
	// Quizmaster should be updated.
	for _, c := range result.Changes {
		if c.AgentName == "quizmaster" {
			if !c.Changed {
				t.Error("quizmaster prompt should have been changed")
			}
			if c.NewPrompt == "" {
				t.Error("new prompt is empty")
			}
		}
	}

	// Verify the DB was updated.
	var prompt string
	err := d.QueryRow(`SELECT system_prompt FROM system_agents WHERE name = 'quizmaster'`).Scan(&prompt)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if prompt != "You are an expert Chinese teacher. Challenge the user with harder sentence structures." {
		t.Errorf("prompt not updated: %s", prompt)
	}
}

func TestStats_AndOrchestrate_ShareSameData(t *testing.T) {
	d := freshDB(t)
	fake := &llm.Fake{Response: llm.Response{Content: tuneResponseJSON}}
	reg := llm.NewRegistry()
	reg.Register("deepseek", fake)

	// Seed vocab + a single review log.
	if _, err := d.Exec(
		`INSERT INTO vocabulary (hanzi, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)`,
		"你好", "ni3 hao3", "nihao", "hello"); err != nil {
		t.Fatalf("seed vocab: %v", err)
	}
	if _, err := d.Exec(
		`INSERT INTO review_logs (vocab_id, agent_type, quiz_type, is_correct) VALUES (?, ?, ?, ?)`,
		1, "quizmaster", "structural_fill", 1); err != nil {
		t.Fatalf("seed log: %v", err)
	}

	orch := &agents.Orchestrator{DB: d, Registry: reg}
	statsH := Stats(orch)
	orchH := Orchestrate(orch)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/stats", statsH)
	mux.HandleFunc("POST /api/agents/orchestrate", orchH)

	// Stats first.
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/api/agents/stats", nil))
	if rec1.Code != http.StatusOK {
		t.Fatalf("stats status=%d", rec1.Code)
	}
	var statsResp struct {
		Results []agents.AgentStats `json:"results"`
	}
	if err := json.Unmarshal(rec1.Body.Bytes(), &statsResp); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	if len(statsResp.Results) != 1 {
		t.Fatalf("stats results = %d, want 1", len(statsResp.Results))
	}
	if statsResp.Results[0].Total != 1 || statsResp.Results[0].Correct != 1 {
		t.Errorf("stats: want 1/1, got %d/%d", statsResp.Results[0].Total, statsResp.Results[0].Correct)
	}

	// Then orchestrate.
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/api/agents/orchestrate", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("orch status=%d", rec2.Code)
	}
	var result agents.Result
	if err := json.Unmarshal(rec2.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode orch: %v", err)
	}
	if len(result.Stats) != 1 {
		t.Fatalf("result stats = %d, want 1", len(result.Stats))
	}
}
