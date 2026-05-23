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

func TestOpenAI_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req deepseekRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Model != "gpt-4o-mini" {
			t.Errorf("model = %q", req.Model)
		}
		if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_object" {
			t.Errorf("response_format = %+v", req.ResponseFormat)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer srv.Close()

	c := NewOpenAI("test-key")
	c.BaseURL = srv.URL

	resp, err := c.Complete(context.Background(), Request{
		Model:    "gpt-4o-mini",
		System:   "you are a quizmaster",
		Messages: []Message{{Role: RoleUser, Content: "go"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestOpenAI_MissingKey(t *testing.T) {
	c := NewOpenAI("")
	_, err := c.Complete(context.Background(), Request{Model: "gpt-4o-mini"})
	if err != ErrOpenAIMissingKey {
		t.Errorf("err = %v, want ErrOpenAIMissingKey", err)
	}
}

func TestOpenAI_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid auth","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()

	c := NewOpenAI("bad")
	c.BaseURL = srv.URL
	_, err := c.Complete(context.Background(), Request{Model: "gpt-4o-mini"})
	if err == nil || !strings.Contains(err.Error(), "Invalid auth") {
		t.Errorf("err = %v, want Invalid auth", err)
	}
}

func TestOpenAI_LiveIntegration(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("OPENAI_API_KEY not set")
	}
	c := NewOpenAI(key)
	c.JSONMode = false
	resp, err := c.Complete(context.Background(), Request{
		Model:     "gpt-4o-mini",
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
