package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"hanzitrack/backend/db"
	"hanzitrack/backend/sentences"
)

// freshDB returns a freshly-initialised SQLite DB in a tempdir, schema applied.
func freshDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

type fakeFetcher struct {
	calls atomic.Int32
	pairs []sentences.Pair
	err   error
}

func (f *fakeFetcher) Fetch(ctx context.Context, hanzi string) ([]sentences.Pair, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.pairs, nil
}

func postJSON(t *testing.T, handler http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(buf)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func newVocab(t *testing.T) (*Vocab, *fakeFetcher, http.Handler) {
	t.Helper()
	d := freshDB(t)
	f := &fakeFetcher{pairs: []sentences.Pair{
		{Chinese: "你好,世界。", English: "Hello, world."},
	}}
	v := &Vocab{DB: d, Sentences: f}
	// Drain in-flight workers before the tempdir is torn down — otherwise
	// the goroutine and TempDir cleanup race over the .db file.
	t.Cleanup(v.WaitWorkers)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/vocab", v.Post)
	mux.HandleFunc("GET /api/vocab", v.List)
	mux.HandleFunc("GET /api/categories", v.Categories)
	return v, f, mux
}

func TestPostVocab_NewWordCreatesRowAndFiresWorker(t *testing.T) {
	v, f, mux := newVocab(t)

	rec := postJSON(t, mux, "/api/vocab", postVocabRequest{
		Hanzi:      "你好",
		Pinyin:     "ni3 hao3",
		English:    "hello",
		Categories: []string{"Greetings"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body)
	}
	var resp postVocabResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID == 0 || !resp.Created {
		t.Errorf("unexpected response: %+v", resp)
	}

	v.WaitWorkers()
	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetcher calls = %d, want 1", got)
	}

	// Verify the row + the cached sentence both made it to disk.
	var hanzi string
	if err := v.DB.QueryRow("SELECT hanzi FROM vocabulary WHERE id = ?", resp.ID).Scan(&hanzi); err != nil {
		t.Fatal(err)
	}
	if hanzi != "你好" {
		t.Errorf("hanzi = %q", hanzi)
	}
	var sCount int
	if err := v.DB.QueryRow("SELECT COUNT(*) FROM example_sentences WHERE vocab_id = ?", resp.ID).Scan(&sCount); err != nil {
		t.Fatal(err)
	}
	if sCount != 1 {
		t.Errorf("sentence count = %d, want 1", sCount)
	}
}

func TestPostVocab_DedupeReturnsSameIDAndSkipsWorker(t *testing.T) {
	v, f, mux := newVocab(t)

	first := postJSON(t, mux, "/api/vocab", postVocabRequest{
		Hanzi: "你好", Pinyin: "ni3 hao3", English: "hello",
		Categories: []string{"Greetings"},
	})
	var firstResp postVocabResponse
	json.Unmarshal(first.Body.Bytes(), &firstResp)

	second := postJSON(t, mux, "/api/vocab", postVocabRequest{
		Hanzi: "你好", Pinyin: "ni3 hao3", English: "hello",
		Categories: []string{"Duolingo Ch.1"},
	})
	if second.Code != http.StatusCreated {
		t.Fatalf("status=%d", second.Code)
	}
	var secondResp postVocabResponse
	json.Unmarshal(second.Body.Bytes(), &secondResp)

	if secondResp.ID != firstResp.ID {
		t.Errorf("dedupe failed: first=%d second=%d", firstResp.ID, secondResp.ID)
	}
	if secondResp.Created {
		t.Errorf("expected created=false on dedupe")
	}

	v.WaitWorkers()
	if got := f.calls.Load(); got != 1 {
		t.Errorf("fetcher calls = %d, want 1 (worker should not fire on dedupe)", got)
	}

	// Both categories should now be linked to the single vocab row.
	rows, _ := v.DB.Query(`
		SELECT c.name FROM categories c
		JOIN vocab_categories vc ON vc.category_id = c.id
		WHERE vc.vocab_id = ? ORDER BY c.name`, firstResp.ID)
	defer rows.Close()
	var cats []string
	for rows.Next() {
		var n string
		rows.Scan(&n)
		cats = append(cats, n)
	}
	if len(cats) != 2 || cats[0] != "Duolingo Ch.1" || cats[1] != "Greetings" {
		t.Errorf("categories after merge: %v", cats)
	}
}

func TestPostVocab_ValidationErrors(t *testing.T) {
	_, _, mux := newVocab(t)

	cases := []struct {
		name string
		body postVocabRequest
	}{
		{"missing hanzi", postVocabRequest{Pinyin: "x", English: "y"}},
		{"missing pinyin", postVocabRequest{Hanzi: "你", English: "y"}},
		{"missing english", postVocabRequest{Hanzi: "你", Pinyin: "x"}},
		{"whitespace only hanzi", postVocabRequest{Hanzi: "   ", Pinyin: "x", English: "y"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postJSON(t, mux, "/api/vocab", c.body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status=%d, want 400", rec.Code)
			}
		})
	}
}

func TestPostVocab_InvalidJSON(t *testing.T) {
	_, _, mux := newVocab(t)
	req := httptest.NewRequest(http.MethodPost, "/api/vocab", strings.NewReader("{not json"))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status=%d", rec.Code)
	}
}

func TestListVocab_Filters(t *testing.T) {
	v, _, mux := newVocab(t)
	post := func(h, p, e string, cats []string) {
		postJSON(t, mux, "/api/vocab", postVocabRequest{Hanzi: h, Pinyin: p, English: e, Categories: cats})
	}
	post("你好", "ni3 hao3", "hello", []string{"Greetings"})
	post("朋友", "peng2 you5", "friend", []string{"People"})
	post("苹果", "ping2 guo3", "apple", []string{"Food"})
	v.WaitWorkers()

	t.Run("no filter returns all", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vocab", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var resp listVocabResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Results) != 3 {
			t.Errorf("got %d, want 3", len(resp.Results))
		}
	})

	t.Run("category filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vocab?category=Food", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var resp listVocabResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Results) != 1 || resp.Results[0].Hanzi != "苹果" {
			t.Errorf("got %+v", resp.Results)
		}
	})

	t.Run("search filter on pinyin_flat", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vocab?search=peng", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var resp listVocabResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Results) != 1 || resp.Results[0].Hanzi != "朋友" {
			t.Errorf("got %+v", resp.Results)
		}
	})

	t.Run("card embeds categories and sentences", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/vocab?category=Greetings", nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var resp listVocabResponse
		json.Unmarshal(rec.Body.Bytes(), &resp)
		if len(resp.Results) != 1 {
			t.Fatalf("got %d results", len(resp.Results))
		}
		card := resp.Results[0]
		if len(card.Categories) == 0 || card.Categories[0] != "Greetings" {
			t.Errorf("categories: %v", card.Categories)
		}
		if len(card.Sentences) != 1 || card.Sentences[0].English != "Hello, world." {
			t.Errorf("sentences: %+v", card.Sentences)
		}
	})
}

func TestCategoriesEndpoint(t *testing.T) {
	_, _, mux := newVocab(t)
	postJSON(t, mux, "/api/vocab", postVocabRequest{
		Hanzi: "你好", Pinyin: "ni3 hao3", English: "hello",
		Categories: []string{"Greetings", "Duolingo Ch.1"},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/categories", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var resp struct {
		Results []string `json:"results"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	want := []string{"Duolingo Ch.1", "Greetings"}
	if len(resp.Results) != 2 || resp.Results[0] != want[0] || resp.Results[1] != want[1] {
		t.Errorf("got %v, want %v", resp.Results, want)
	}
}
