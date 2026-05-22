package llm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	fake := &Fake{Response: Response{Content: "hello"}}
	r.Register("test", fake)

	got, err := r.Get("test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != fake {
		t.Errorf("registered client not returned identically")
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("nope")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("got %v, want ErrUnknownProvider", err)
	}
}

func TestRegistry_RegisterOverwrites(t *testing.T) {
	r := NewRegistry()
	r.Register("test", &Fake{Response: Response{Content: "a"}})
	r.Register("test", &Fake{Response: Response{Content: "b"}})

	c, _ := r.Get("test")
	resp, _ := c.Complete(context.Background(), Request{})
	if resp.Content != "b" {
		t.Errorf("expected second registration to win, got %q", resp.Content)
	}
}

func TestDefaultRegistry_HasThreeProviders(t *testing.T) {
	r := DefaultRegistry()
	for _, name := range []string{"deepseek", "openai", "anthropic"} {
		c, err := r.Get(name)
		if err != nil {
			t.Errorf("provider %q not registered: %v", name, err)
			continue
		}
		resp, err := c.Complete(context.Background(), Request{Model: "fake-model"})
		if err != nil {
			t.Errorf("stub %q Complete returned error: %v", name, err)
		}
		if !strings.Contains(resp.Content, name) {
			t.Errorf("stub %q response did not mention provider name: %q", name, resp.Content)
		}
	}
}

func TestFake_RecordsCalls(t *testing.T) {
	f := &Fake{Response: Response{Content: "fixed"}}
	ctx := context.Background()

	_, _ = f.Complete(ctx, Request{Model: "m1", System: "sys"})
	_, _ = f.Complete(ctx, Request{Model: "m2"})

	if f.Calls != 2 {
		t.Errorf("Calls = %d, want 2", f.Calls)
	}
	if f.LastRequest.Model != "m2" {
		t.Errorf("LastRequest.Model = %q, want m2", f.LastRequest.Model)
	}
	if len(f.Requests) != 2 {
		t.Errorf("Requests length = %d, want 2", len(f.Requests))
	}
}
