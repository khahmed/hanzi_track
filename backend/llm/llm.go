// Package llm is the provider-agnostic interface used by every agent.
//
// Real provider implementations (DeepSeek, OpenAI, Anthropic) plug in via
// Registry.Register. Until slice 2 lands the real DeepSeek client, all three
// canonical provider names are registered against a stubClient that returns
// a fixed fake response so the rest of the system can be wired end-to-end.
package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Role names mirror the canonical chat-completion vocabulary used by every
// supported provider — DeepSeek and OpenAI are OpenAI-style, Anthropic uses
// the same role strings even though "system" lives in a separate field.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn in a chat-style completion call.
type Message struct {
	Role    string
	Content string
}

// Request is the provider-agnostic completion request. Concrete clients
// translate it to the wire shape their API expects.
type Request struct {
	Model       string
	System      string
	Messages    []Message
	Temperature float64
	MaxTokens   int
}

// Response is the provider-agnostic completion response.
type Response struct {
	Content string
}

// Client is the contract every provider implementation satisfies.
type Client interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Registry maps a provider name (as stored in system_agents.provider) to a
// concrete Client. Safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	clients map[string]Client
}

func NewRegistry() *Registry {
	return &Registry{clients: map[string]Client{}}
}

func (r *Registry) Register(name string, c Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[name] = c
}

// Get returns the Client registered for name. Returns ErrUnknownProvider if
// no client has been registered under that name.
func (r *Registry) Get(name string) (Client, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[name]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
	}
	return c, nil
}

// Names returns the sorted-by-insertion list of registered provider names.
// Useful for diagnostics and tests.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.clients))
	for n := range r.clients {
		names = append(names, n)
	}
	return names
}

// ErrUnknownProvider is returned by Registry.Get when the name is unbound.
var ErrUnknownProvider = errors.New("unknown llm provider")

// DefaultRegistry returns a Registry with all three canonical providers
// pre-registered against the stub client. Slice 2 will replace 'deepseek'
// with the real DeepSeek HTTP client; slice 4 swaps in real openai/anthropic.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register("deepseek", &stubClient{provider: "deepseek"})
	r.Register("openai", &stubClient{provider: "openai"})
	r.Register("anthropic", &stubClient{provider: "anthropic"})
	return r
}

// stubClient returns a fixed fake response. It exists so the agent registry,
// handler wiring, and frontend tab nav can ship end-to-end before any real
// LLM integration. The response intentionally mentions the provider name so
// it's obvious in logs / responses which slot was hit.
type stubClient struct {
	provider string
}

func (s *stubClient) Complete(ctx context.Context, req Request) (Response, error) {
	return Response{
		Content: fmt.Sprintf("[stub %s] no real LLM wired yet — model=%q messages=%d",
			s.provider, req.Model, len(req.Messages)),
	}, nil
}
