package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListAgents_ReturnsSeededRows(t *testing.T) {
	d := freshDB(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents", ListAgents(d))

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp listAgentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("got %d agents, want 2 (seeded quizmaster + conversationalist)", len(resp.Results))
	}

	byName := map[string]Agent{}
	for _, a := range resp.Results {
		byName[a.Name] = a
	}
	for _, want := range []string{"quizmaster", "conversationalist"} {
		a, ok := byName[want]
		if !ok {
			t.Errorf("missing seeded agent %q", want)
			continue
		}
		if a.DisplayName == "" {
			t.Errorf("%s: empty display_name", want)
		}
		if a.SystemPrompt == "" {
			t.Errorf("%s: empty system_prompt", want)
		}
		if a.Provider != "deepseek" {
			t.Errorf("%s: provider = %q, want deepseek", want, a.Provider)
		}
		if a.Model == "" {
			t.Errorf("%s: empty model", want)
		}
		if !a.IsActive {
			t.Errorf("%s: is_active should be true on seeded rows", want)
		}
	}
}

func TestListAgents_OmitsInactive(t *testing.T) {
	d := freshDB(t)
	if _, err := d.Exec("UPDATE system_agents SET is_active = 0 WHERE name = 'conversationalist'"); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/agents", ListAgents(d))

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	var resp listAgentsResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if len(resp.Results) != 1 || resp.Results[0].Name != "quizmaster" {
		t.Errorf("got %+v, want only [quizmaster]", resp.Results)
	}
}

func TestListAgents_SeedIsIdempotent(t *testing.T) {
	d := freshDB(t)
	// Simulate a user edit, then re-apply the schema.sql (which contains the
	// INSERT OR IGNORE seed). The edit must survive.
	const edited = "EDITED PROMPT — survives restart"
	if _, err := d.Exec("UPDATE system_agents SET system_prompt = ? WHERE name = 'quizmaster'", edited); err != nil {
		t.Fatalf("edit: %v", err)
	}

	// Re-run the seed by calling the embedded schema directly. We can't call
	// db.Open(samePath) here without a second goroutine, so just re-execute
	// the INSERT OR IGNORE statement and confirm it's a no-op for edited rows.
	if _, err := d.Exec(`INSERT OR IGNORE INTO system_agents
		(name, display_name, system_prompt, temperature, provider, model)
		VALUES ('quizmaster', 'Quizmaster', 'SHOULD NOT WIN', 0.3, 'deepseek', 'deepseek-chat')`); err != nil {
		t.Fatalf("reseed: %v", err)
	}

	var got string
	if err := d.QueryRow("SELECT system_prompt FROM system_agents WHERE name = 'quizmaster'").Scan(&got); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got != edited {
		t.Errorf("user edit lost: got %q, want %q", got, edited)
	}
}
