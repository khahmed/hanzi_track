package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDeepSeek_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req deepseekRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Model != "deepseek-chat" {
			t.Errorf("model = %q", req.Model)
		}
		if req.MaxTokens != 512 {
			t.Errorf("max_tokens = %d", req.MaxTokens)
		}
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Errorf("response_format = %+v, want {json_object}", req.ResponseFormat)
		}
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Errorf("messages shape wrong: %+v", req.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}]}`))
	}))
	defer srv.Close()

	c := NewDeepSeek("test-key")
	c.BaseURL = srv.URL

	resp, err := c.Complete(context.Background(), Request{
		Model:     "deepseek-chat",
		System:    "you are a quizmaster",
		Messages:  []Message{{Role: RoleUser, Content: "go"}},
		MaxTokens: 512,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestDeepSeek_MissingKey(t *testing.T) {
	c := NewDeepSeek("")
	_, err := c.Complete(context.Background(), Request{Model: "deepseek-chat"})
	if err != ErrMissingAPIKey {
		t.Errorf("err = %v, want ErrMissingAPIKey", err)
	}
}

func TestDeepSeek_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid API key","type":"authentication_error"}}`))
	}))
	defer srv.Close()

	c := NewDeepSeek("bad")
	c.BaseURL = srv.URL
	_, err := c.Complete(context.Background(), Request{Model: "deepseek-chat"})
	if err == nil || !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("err = %v, want unauth error", err)
	}
}

// TestDeepSeek_LiveIntegration hits the real API. Skipped unless
// DEEPSEEK_API_KEY is set, to keep CI free of network/cost requirements.
func TestDeepSeek_LiveIntegration(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}
	c := NewDeepSeek(key)
	c.JSONMode = false // simple string echo, no JSON contract
	resp, err := c.Complete(context.Background(), Request{
		Model:     "deepseek-chat",
		System:    "Respond with exactly the word OK.",
		Messages:  []Message{{Role: RoleUser, Content: "ping"}},
		MaxTokens: 8,
	})
	if err != nil {
		t.Fatalf("live Complete: %v", err)
	}
	if !strings.Contains(strings.ToUpper(resp.Content), "OK") {
		t.Errorf("unexpected live response: %q", resp.Content)
	}
}
