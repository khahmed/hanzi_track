package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"hanzitrack/backend/llm"
)

// scriptedClient pops one canned (content, err) pair per Complete call.
// Used to drive the retry path in handler tests without spinning a real
// HTTP server. The base Fake in backend/llm always returns the same
// response, which is fine for single-call paths but can't distinguish
// "bad then good" from "bad twice".
type scriptedClient struct {
	mu        sync.Mutex
	responses []llm.Response
	errors    []error
	calls     int
}

func (s *scriptedClient) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.calls
	s.calls++
	var resp llm.Response
	var err error
	if i < len(s.responses) {
		resp = s.responses[i]
	}
	if i < len(s.errors) {
		err = s.errors[i]
	}
	return resp, err
}

func newQuiz(t *testing.T, client llm.Client) (*Quiz, http.Handler, int64) {
	t.Helper()
	d := freshDB(t)
	res, err := d.Exec(
		`INSERT INTO vocabulary (hanzi, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)`,
		"朋友", "peng2 you5", "pengyou", "friend")
	if err != nil {
		t.Fatalf("seed vocab: %v", err)
	}
	vocabID, _ := res.LastInsertId()
	if _, err := d.Exec(`INSERT INTO categories (name) VALUES ('Food')`); err != nil {
		t.Fatalf("seed cat: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO vocab_categories (vocab_id, category_id)
	                      VALUES (?, (SELECT id FROM categories WHERE name = 'Food'))`, vocabID); err != nil {
		t.Fatalf("link cat: %v", err)
	}

	reg := llm.NewRegistry()
	reg.Register("deepseek", client)

	q := &Quiz{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents/quiz", q.Generate)
	mux.HandleFunc("POST /api/agents/quiz/submit", q.Submit)
	return q, mux, vocabID
}

const validQuizJSON = `{
  "structure": "Subject + Verb + Object",
  "question_chinese": "我有一个____。",
  "options": ["朋友", "学校", "苹果", "昨天"],
  "correct_answer": "朋友",
  "vocab_id": 1,
  "explanation": "朋友 (friend) is the noun being possessed."
}`

func TestQuiz_Generate_HappyPath(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{Content: validQuizJSON}}}
	_, mux, _ := newQuiz(t, client)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/quiz?category=Food", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var q QuizQuestion
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if q.CorrectAnswer != "朋友" || q.QuestionChinese == "" || len(q.Options) != 4 {
		t.Errorf("unexpected question: %+v", q)
	}
	if client.calls != 1 {
		t.Errorf("calls = %d, want 1", client.calls)
	}
}

func TestQuiz_Generate_MalformedJSONRetriesAndSucceeds(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Content: "not json at all"},
		{Content: validQuizJSON},
	}}
	_, mux, _ := newQuiz(t, client)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/quiz?category=Food", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if client.calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", client.calls)
	}
}

func TestQuiz_Generate_MalformedJSONTwiceReturns502(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Content: "still not json"},
		{Content: "still not json"},
	}}
	_, mux, _ := newQuiz(t, client)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/quiz?category=Food", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status=%d, want 502", rec.Code)
	}
	if client.calls != 2 {
		t.Errorf("calls = %d, want 2 (no third attempt)", client.calls)
	}
}

func TestQuiz_Generate_EmptyCategoryFallsThroughToWholeBank(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{Content: validQuizJSON}}}
	_, mux, _ := newQuiz(t, client)

	req := httptest.NewRequest(http.MethodGet, "/api/agents/quiz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
}

func TestQuiz_Submit_WritesReviewLog(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{Content: validQuizJSON}}}
	q, mux, vocabID := newQuiz(t, client)

	rec := postJSON(t, mux, "/api/agents/quiz/submit", submitRequest{
		VocabID:   vocabID,
		QuizType:  "structural_fill",
		IsCorrect: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp submitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == 0 {
		t.Error("expected non-zero log id")
	}

	var (
		agent     string
		quizType  string
		isCorrect int
	)
	row := q.DB.QueryRow(`SELECT agent_type, quiz_type, is_correct FROM review_logs WHERE id = ?`, resp.ID)
	if err := row.Scan(&agent, &quizType, &isCorrect); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if agent != "quizmaster" || quizType != "structural_fill" || isCorrect != 1 {
		t.Errorf("row = %s/%s/%d", agent, quizType, isCorrect)
	}
}

func TestQuiz_Submit_RejectsMissingFields(t *testing.T) {
	client := &scriptedClient{}
	_, mux, _ := newQuiz(t, client)

	rec := postJSON(t, mux, "/api/agents/quiz/submit", submitRequest{
		QuizType: "structural_fill",
		// VocabID intentionally zero
	})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (missing vocab_id)", rec.Code)
	}
}

func TestQuiz_BuildPrompt_IncludesAccuracy(t *testing.T) {
	words := []quizVocab{
		{ID: 1, Hanzi: "朋友", Pinyin: "peng2 you5", English: "friend", Attempts: 3, Correct: 1},
	}
	got := buildQuizPrompt("Food", words)
	for _, want := range []string{"朋友", "peng2 you5", "friend", "| 3 |", "| 1", "Food", "json"} {
		if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("prompt missing %q\n---\n%s", want, got)
		}
	}
}
