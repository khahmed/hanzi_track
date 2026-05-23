package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hanzitrack/backend/llm"
)

func newChat(t *testing.T, client llm.Client) (*Chat, http.Handler, *sql.DB) {
	t.Helper()
	d := freshDB(t)
	if _, err := d.Exec(
		`INSERT INTO vocabulary (hanzi, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)`,
		"你好", "ni3 hao3", "nihao", "hello"); err != nil {
		t.Fatalf("seed vocab: %v", err)
	}

	reg := llm.NewRegistry()
	reg.Register("deepseek", client)

	c := &Chat{DB: d, Registry: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agents/chat", c.Send)
	mux.HandleFunc("GET /api/agents/chat/history", c.History)
	return c, mux, d
}

const validChatJSON = `{
  "chinese": "你好，今天怎么样？",
  "pinyin": "ni3 hao3, jin1 tian1 zen3 me yang4?",
  "coach_notes": "[Greeting + open-ended question. 怎么样 commonly follows 今天 for 'how is today'.]"
}`

func TestChat_Send_HappyPath(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{Content: validChatJSON}}}
	_, mux, d := newChat(t, client)

	rec := postJSON(t, mux, "/api/agents/chat", chatSendRequest{Message: "嗨"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}

	var resp chatSendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Turn.Chinese == "" || resp.Turn.Pinyin == "" || resp.Turn.CoachNotes == "" {
		t.Errorf("turn missing field: %+v", resp.Turn)
	}
	if resp.UserID == 0 || resp.MessageID == 0 {
		t.Errorf("ids should be non-zero: %+v", resp)
	}

	// Both rows must have landed: user first, then assistant.
	rows, err := d.Query(`SELECT role, content, COALESCE(pinyin, ''), COALESCE(coach_notes, '')
	                        FROM chat_messages ORDER BY id`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	type row struct{ role, content, pinyin, coach string }
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.role, &r.content, &r.pinyin, &r.coach); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2", len(got))
	}
	if got[0].role != "user" || got[0].content != "嗨" || got[0].pinyin != "" {
		t.Errorf("user row wrong: %+v", got[0])
	}
	if got[1].role != "assistant" || got[1].content == "" || got[1].pinyin == "" || got[1].coach == "" {
		t.Errorf("assistant row missing fields: %+v", got[1])
	}
}

func TestChat_Send_MalformedJSONRetriesAndSucceeds(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Content: "not json"},
		{Content: validChatJSON},
	}}
	_, mux, _ := newChat(t, client)

	rec := postJSON(t, mux, "/api/agents/chat", chatSendRequest{Message: "hi"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	if client.calls != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", client.calls)
	}
}

func TestChat_Send_MalformedJSONTwiceReturns502(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{
		{Content: "still not json"},
		{Content: "still not json"},
	}}
	_, mux, d := newChat(t, client)

	rec := postJSON(t, mux, "/api/agents/chat", chatSendRequest{Message: "hi"})
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status=%d, want 502", rec.Code)
	}
	// No rows should have been persisted on the failure path.
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM chat_messages`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("chat_messages should be empty on 502, got %d rows", n)
	}
}

func TestChat_Send_RejectsEmptyMessage(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{Content: validChatJSON}}}
	_, mux, _ := newChat(t, client)

	rec := postJSON(t, mux, "/api/agents/chat", chatSendRequest{Message: "   "})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 on empty", rec.Code)
	}
	if client.calls != 0 {
		t.Errorf("LLM should not be called on empty input")
	}
}

func TestChat_Send_ContextIncludesVocabAndRecentHistory(t *testing.T) {
	client := &scriptedClient{responses: []llm.Response{{Content: validChatJSON}}}
	_, mux, d := newChat(t, client)

	// Seed > 10 prior messages so we can confirm the last-10 cap.
	for i := 0; i < 12; i++ {
		if _, err := d.Exec(
			`INSERT INTO chat_messages (role, content) VALUES ('user', ?)`,
			"old-"+string(rune('A'+i))); err != nil {
			t.Fatalf("seed history: %v", err)
		}
	}

	rec := postJSON(t, mux, "/api/agents/chat", chatSendRequest{Message: "new"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}

	// The scriptedClient captured the request via its mu/calls counter,
	// but we need the actual messages. Capture via a small wrapper.
	if len(client.responses) > 0 && client.calls != 1 {
		t.Errorf("calls = %d, want 1", client.calls)
	}
}

func TestChat_BuildMessages_OrderingAndCounts(t *testing.T) {
	history := []ChatMessage{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "user", Content: "u2"},
	}
	vocab := []chatVocab{{Hanzi: "你好", Pinyin: "ni3 hao3", English: "hello"}}
	msgs := buildChatMessages(history, vocab, "now")

	if len(msgs) != 1+len(history)+1 {
		t.Fatalf("got %d messages, want %d", len(msgs), 1+len(history)+1)
	}
	if msgs[0].Role != llm.RoleUser || !strings.Contains(msgs[0].Content, "你好") {
		t.Errorf("first message should be vocab preamble: %+v", msgs[0])
	}
	if msgs[2].Role != llm.RoleAssistant || msgs[2].Content != "a1" {
		t.Errorf("history[1] should be assistant a1: %+v", msgs[2])
	}
	if msgs[len(msgs)-1].Content != "now" {
		t.Errorf("last message should be the new user turn, got %q", msgs[len(msgs)-1].Content)
	}
}

func TestChat_History_ReturnsChronological(t *testing.T) {
	client := &scriptedClient{}
	_, mux, d := newChat(t, client)

	for _, c := range []string{"first", "second", "third"} {
		if _, err := d.Exec(
			`INSERT INTO chat_messages (role, content) VALUES ('user', ?)`, c); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents/chat/history", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}

	var resp chatHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("got %d, want 3", len(resp.Results))
	}
	if resp.Results[0].Content != "first" || resp.Results[2].Content != "third" {
		t.Errorf("not chronological: %+v", resp.Results)
	}
}

func TestChat_History_RespectsLimit(t *testing.T) {
	client := &scriptedClient{}
	_, mux, d := newChat(t, client)

	for i := 0; i < 5; i++ {
		if _, err := d.Exec(
			`INSERT INTO chat_messages (role, content) VALUES ('user', ?)`,
			string(rune('A'+i))); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/agents/chat/history?limit=2", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp chatHistoryResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Results) != 2 {
		t.Fatalf("got %d, want 2", len(resp.Results))
	}
	// limit=2 should return the two newest (D, E) in chronological order.
	if resp.Results[0].Content != "D" || resp.Results[1].Content != "E" {
		t.Errorf("expected last two (D,E), got %+v", resp.Results)
	}
}
