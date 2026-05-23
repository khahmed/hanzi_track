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

func TestAnthropic_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if got := r.Header.Get("anthropic-version"); got != AnthropicVersion {
			t.Errorf("anthropic-version = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		var req anthropicRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Model != "claude-haiku-4-5" {
			t.Errorf("model = %q", req.Model)
		}
		if req.MaxTokens == 0 {
			t.Error("max_tokens must be set; Anthropic requires it")
		}
		if req.System != "you are a quizmaster" {
			t.Errorf("system = %q (must be top-level, not in messages)", req.System)
		}
		// System must NOT appear in messages.
		for _, m := range req.Messages {
			if m.Role == "system" {
				t.Errorf("messages contains role=system, should be top-level: %+v", m)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"content":[{"type":"text","text":"hi from claude"}],"type":"message"}`))
	}))
	defer srv.Close()

	c := NewAnthropic("test-key")
	c.BaseURL = srv.URL

	resp, err := c.Complete(context.Background(), Request{
		Model:     "claude-haiku-4-5",
		System:    "you are a quizmaster",
		Messages:  []Message{{Role: RoleUser, Content: "go"}},
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hi from claude" {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestAnthropic_DefaultMaxTokensWhenZero(t *testing.T) {
	var captured anthropicRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &captured)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"type":"message"}`))
	}))
	defer srv.Close()

	c := NewAnthropic("k")
	c.BaseURL = srv.URL
	_, err := c.Complete(context.Background(), Request{
		Model:    "claude-haiku-4-5",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
		// MaxTokens intentionally 0
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if captured.MaxTokens != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens = %d, want default %d", captured.MaxTokens, anthropicDefaultMaxTokens)
	}
}

func TestAnthropic_MissingKey(t *testing.T) {
	c := NewAnthropic("")
	_, err := c.Complete(context.Background(), Request{Model: "claude-haiku-4-5"})
	if err != ErrAnthropicMissingKey {
		t.Errorf("err = %v, want ErrAnthropicMissingKey", err)
	}
}

func TestAnthropic_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"x-api-key header is required"}}`))
	}))
	defer srv.Close()

	c := NewAnthropic("bad")
	c.BaseURL = srv.URL
	_, err := c.Complete(context.Background(), Request{
		Model:     "claude-haiku-4-5",
		MaxTokens: 8,
	})
	if err == nil || !strings.Contains(err.Error(), "x-api-key header is required") {
		t.Errorf("err = %v, want auth error", err)
	}
}

func TestAnthropic_ConcatenatesTextBlocks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"content":[
			{"type":"text","text":"part one "},
			{"type":"text","text":"part two"}
		],"type":"message"}`))
	}))
	defer srv.Close()

	c := NewAnthropic("k")
	c.BaseURL = srv.URL
	resp, err := c.Complete(context.Background(), Request{
		Model:     "claude-haiku-4-5",
		MaxTokens: 8,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "part one part two" {
		t.Errorf("content = %q, want concatenation", resp.Content)
	}
}

func TestAnthropic_LiveIntegration(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	c := NewAnthropic(key)
	resp, err := c.Complete(context.Background(), Request{
		Model:     "claude-haiku-4-5",
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
