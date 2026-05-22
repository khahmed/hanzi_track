// Package handlers contains HTTP handlers for the HánzìTrack JSON API.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"hanzitrack/backend/dictionary"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50
)

type DictMatch struct {
	Hanzi   string `json:"hanzi"`
	Pinyin  string `json:"pinyin"`
	English string `json:"english"`
}

type dictSearchResponse struct {
	Results []DictMatch `json:"results"`
}

// DictSearch returns a handler for GET /api/dict/search?q=<pinyin>[&limit=N].
//
// The query is flattened (digits/spaces stripped, lowercased, u:->v) before
// being matched. We use GLOB rather than LIKE — see SPEC.md and the comment
// on idx_cedict_pinyin in schema.sql.
func DictSearch(database *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("q")
		flat := dictionary.FlattenPinyin(raw)
		if flat == "" {
			writeJSON(w, dictSearchResponse{Results: []DictMatch{}})
			return
		}

		limit := defaultSearchLimit
		if s := r.URL.Query().Get("limit"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				if n > maxSearchLimit {
					n = maxSearchLimit
				}
				limit = n
			}
		}

		// Ranking, best-effort without frequency data:
		//   1. Common-noun pinyin (lowercase) before proper nouns (capitalised
		//      in CEDICT for surnames/place names, e.g. "Peng2" for 彭).
		//   2. Shorter pinyin_flat first (single chars before compounds).
		//   3. Stable tiebreak by id.
		// This still ranks rare variant chars too high for short prefixes; a
		// proper fix needs HSK or BCC frequency data.
		rows, err := database.QueryContext(r.Context(),
			`SELECT hanzi_simplified, pinyin, english
			   FROM cedict
			  WHERE pinyin_flat GLOB ?
			  ORDER BY (CASE WHEN pinyin GLOB '[A-Z]*' THEN 1 ELSE 0 END),
			           LENGTH(pinyin_flat) ASC,
			           id ASC
			  LIMIT ?`,
			flat+"*", limit,
		)
		if err != nil {
			http.Error(w, "query failed", http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		results := make([]DictMatch, 0, limit)
		for rows.Next() {
			var m DictMatch
			if err := rows.Scan(&m.Hanzi, &m.Pinyin, &m.English); err != nil {
				http.Error(w, "scan failed", http.StatusInternalServerError)
				return
			}
			results = append(results, m)
		}
		if err := rows.Err(); err != nil {
			http.Error(w, "iter failed", http.StatusInternalServerError)
			return
		}
		writeJSON(w, dictSearchResponse{Results: results})
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}
