package llm

import (
	"context"
	"sync"
)

// Fake is a Client implementation for tests. It records every call and
// returns the configured Response/Err on each invocation. Safe for
// concurrent use.
type Fake struct {
	mu          sync.Mutex
	Response    Response
	Err         error
	Calls       int
	LastRequest Request
	Requests    []Request
}

func (f *Fake) Complete(ctx context.Context, req Request) (Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls++
	f.LastRequest = req
	f.Requests = append(f.Requests, req)
	return f.Response, f.Err
}
