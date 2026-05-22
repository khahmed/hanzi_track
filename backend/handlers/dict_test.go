package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hanzitrack/backend/db"
)

// openSeededDB opens the seeded database from the repo's data/ dir.
// Tests that rely on it should skip if it isn't there yet.
func openSeededDB(t *testing.T) *http.ServeMux {
	t.Helper()
	dbPath := filepath.Join("..", "..", "data", "hanzitrack.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Skipf("seeded DB not found at %s — run `go run ./cmd/seed` first", dbPath)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dict/search", DictSearch(database))
	return mux
}

func get(t *testing.T, mux *http.ServeMux, url string) (int, dictSearchResponse) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var body dictSearchResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v (raw: %s)", err, rec.Body.String())
		}
	}
	return rec.Code, body
}

func TestDictSearch_KnownWord(t *testing.T) {
	mux := openSeededDB(t)
	code, body := get(t, mux, "/api/dict/search?q=nihao")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if len(body.Results) == 0 {
		t.Fatal("no results for nihao")
	}
	found := false
	for _, r := range body.Results {
		if r.Hanzi == "你好" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 你好 in results, got %+v", body.Results)
	}
}

func TestDictSearch_StripsTonesInInput(t *testing.T) {
	mux := openSeededDB(t)
	_, withTones := get(t, mux, "/api/dict/search?q=ni3+hao3")
	_, flat := get(t, mux, "/api/dict/search?q=nihao")
	if len(withTones.Results) == 0 || len(flat.Results) == 0 {
		t.Fatal("expected results for both forms")
	}
	if withTones.Results[0].Hanzi != flat.Results[0].Hanzi {
		t.Errorf("tones-vs-flat divergence: %q vs %q",
			withTones.Results[0].Hanzi, flat.Results[0].Hanzi)
	}
}

func TestDictSearch_EmptyQuery(t *testing.T) {
	mux := openSeededDB(t)
	code, body := get(t, mux, "/api/dict/search?q=")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if len(body.Results) != 0 {
		t.Errorf("expected empty results, got %d", len(body.Results))
	}
}

func TestDictSearch_NoMatch(t *testing.T) {
	mux := openSeededDB(t)
	code, body := get(t, mux, "/api/dict/search?q=zzzzqqqq")
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if len(body.Results) != 0 {
		t.Errorf("expected empty results, got %d", len(body.Results))
	}
}

func TestDictSearch_LimitRespected(t *testing.T) {
	mux := openSeededDB(t)
	_, body := get(t, mux, "/api/dict/search?q=ma&limit=3")
	if len(body.Results) != 3 {
		t.Errorf("limit=3 returned %d results", len(body.Results))
	}
}
