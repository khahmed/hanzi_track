package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"hanzitrack/backend/dictionary"
	"hanzitrack/backend/sentences"
)

// Vocab bundles dependencies for the vocab-related handlers. A non-nil
// Sentences enables the background worker that fetches example sentences
// when a new word is saved. Tests can call WaitWorkers to drain in-flight
// goroutines before asserting against the DB.
type Vocab struct {
	DB        *sql.DB
	Sentences sentences.Fetcher

	wg sync.WaitGroup
}

// WaitWorkers blocks until every background sentence-fetch launched by this
// handler set has finished. Intended for tests and graceful shutdown.
func (v *Vocab) WaitWorkers() { v.wg.Wait() }

type postVocabRequest struct {
	Hanzi      string   `json:"hanzi"`
	Pinyin     string   `json:"pinyin"`
	English    string   `json:"english"`
	Categories []string `json:"categories"`
}

type postVocabResponse struct {
	ID      int64 `json:"id"`
	Created bool  `json:"created"`
}

// Post handles POST /api/vocab. Idempotent by hanzi: a second POST with the
// same characters returns the existing row's id with created=false, after
// merging in any new categories. Sentence-fetch only runs for genuinely new
// rows.
func (v *Vocab) Post(w http.ResponseWriter, r *http.Request) {
	var req postVocabRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.Hanzi = strings.TrimSpace(req.Hanzi)
	req.Pinyin = strings.TrimSpace(req.Pinyin)
	req.English = strings.TrimSpace(req.English)
	if req.Hanzi == "" || req.Pinyin == "" || req.English == "" {
		http.Error(w, "hanzi, pinyin and english are required", http.StatusBadRequest)
		return
	}

	id, created, err := v.upsertVocab(r.Context(), req)
	if err != nil {
		log.Printf("upsert vocab: %v", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	if created {
		v.launchSentenceWorker(id, req.Hanzi)
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, postVocabResponse{ID: id, Created: created})
}

func (v *Vocab) upsertVocab(ctx context.Context, req postVocabRequest) (id int64, created bool, err error) {
	tx, err := v.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	err = tx.QueryRowContext(ctx, "SELECT id FROM vocabulary WHERE hanzi = ?", req.Hanzi).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		flat := dictionary.FlattenPinyin(req.Pinyin)
		res, ierr := tx.ExecContext(ctx,
			"INSERT INTO vocabulary (hanzi, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)",
			req.Hanzi, req.Pinyin, flat, req.English)
		if ierr != nil {
			err = ierr
			return
		}
		id, err = res.LastInsertId()
		if err != nil {
			return
		}
		created = true
	case err != nil:
		return
	}

	for _, name := range req.Categories {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var catID int64
		qerr := tx.QueryRowContext(ctx, "SELECT id FROM categories WHERE name = ?", name).Scan(&catID)
		if errors.Is(qerr, sql.ErrNoRows) {
			res, ierr := tx.ExecContext(ctx, "INSERT INTO categories (name) VALUES (?)", name)
			if ierr != nil {
				err = ierr
				return
			}
			catID, err = res.LastInsertId()
			if err != nil {
				return
			}
		} else if qerr != nil {
			err = qerr
			return
		}
		if _, ierr := tx.ExecContext(ctx,
			"INSERT OR IGNORE INTO vocab_categories (vocab_id, category_id) VALUES (?, ?)",
			id, catID); ierr != nil {
			err = ierr
			return
		}
	}

	err = tx.Commit()
	return
}

type vocabCard struct {
	ID          int64        `json:"id"`
	Hanzi       string       `json:"hanzi"`
	Pinyin      string       `json:"pinyin"`
	English     string       `json:"english"`
	SearchCount int          `json:"search_count"`
	CreatedAt   string       `json:"created_at"`
	Categories  []string     `json:"categories"`
	Sentences   []sentencePair `json:"sentences"`
}

type sentencePair struct {
	Chinese string `json:"chinese"`
	English string `json:"english"`
}

type listVocabResponse struct {
	Results []vocabCard `json:"results"`
}

// List handles GET /api/vocab?category=<name>&search=<pinyin>&limit=<n>.
// Returns vocab cards with categories and cached example sentences, newest
// first. Filters are AND-composed.
func (v *Vocab) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var (
		conds []string
		args  []any
	)

	if cat := strings.TrimSpace(q.Get("category")); cat != "" {
		conds = append(conds,
			`v.id IN (SELECT vc.vocab_id FROM vocab_categories vc
			           JOIN categories c ON c.id = vc.category_id
			          WHERE c.name = ?)`)
		args = append(args, cat)
	}
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		conds = append(conds, "v.pinyin_flat GLOB ?")
		args = append(args, dictionary.FlattenPinyin(search)+"*")
	}

	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}

	limitClause := ""
	if s := strings.TrimSpace(q.Get("limit")); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limitClause = " LIMIT " + strconv.Itoa(n)
		}
	}

	query := `SELECT v.id, v.hanzi, v.pinyin, v.english, v.search_count, v.created_at
	            FROM vocabulary v
	            ` + where + `
	         ORDER BY v.created_at DESC, v.id DESC` + limitClause

	rows, err := v.DB.QueryContext(r.Context(), query, args...)
	if err != nil {
		log.Printf("list vocab: %v", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	cards := []vocabCard{}
	ids := []any{}
	cardByID := map[int64]*vocabCard{}
	for rows.Next() {
		var c vocabCard
		if err := rows.Scan(&c.ID, &c.Hanzi, &c.Pinyin, &c.English, &c.SearchCount, &c.CreatedAt); err != nil {
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		c.Categories = []string{}
		c.Sentences = []sentencePair{}
		cards = append(cards, c)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "iter failed", http.StatusInternalServerError)
		return
	}
	for i := range cards {
		ids = append(ids, cards[i].ID)
		cardByID[cards[i].ID] = &cards[i]
	}

	if len(ids) > 0 {
		if err := loadCategories(r.Context(), v.DB, ids, cardByID); err != nil {
			log.Printf("load categories: %v", err)
			http.Error(w, "category lookup failed", http.StatusInternalServerError)
			return
		}
		if err := loadSentences(r.Context(), v.DB, ids, cardByID); err != nil {
			log.Printf("load sentences: %v", err)
			http.Error(w, "sentence lookup failed", http.StatusInternalServerError)
			return
		}
	}

	writeJSON(w, listVocabResponse{Results: cards})
}

func loadCategories(ctx context.Context, db *sql.DB, ids []any, cards map[int64]*vocabCard) error {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT vc.vocab_id, c.name
	        FROM vocab_categories vc
	        JOIN categories c ON c.id = vc.category_id
	       WHERE vc.vocab_id IN (` + placeholders + `)
	    ORDER BY c.name`
	rows, err := db.QueryContext(ctx, q, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var vid int64
		var name string
		if err := rows.Scan(&vid, &name); err != nil {
			return err
		}
		if c, ok := cards[vid]; ok {
			c.Categories = append(c.Categories, name)
		}
	}
	return rows.Err()
}

func loadSentences(ctx context.Context, db *sql.DB, ids []any, cards map[int64]*vocabCard) error {
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT vocab_id, chinese_sentence, english_sentence
	        FROM example_sentences
	       WHERE vocab_id IN (` + placeholders + `)
	    ORDER BY id`
	rows, err := db.QueryContext(ctx, q, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var vid int64
		var s sentencePair
		if err := rows.Scan(&vid, &s.Chinese, &s.English); err != nil {
			return err
		}
		if c, ok := cards[vid]; ok {
			c.Sentences = append(c.Sentences, s)
		}
	}
	return rows.Err()
}

// Categories handles GET /api/categories — flat alphabetical list of names.
func (v *Vocab) Categories(w http.ResponseWriter, r *http.Request) {
	rows, err := v.DB.QueryContext(r.Context(), "SELECT name FROM categories ORDER BY name")
	if err != nil {
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			http.Error(w, "scan failed", http.StatusInternalServerError)
			return
		}
		names = append(names, n)
	}
	writeJSON(w, struct {
		Results []string `json:"results"`
	}{names})
}

func (v *Vocab) launchSentenceWorker(vocabID int64, hanzi string) {
	if v.Sentences == nil {
		return
	}
	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		pairs, err := v.Sentences.Fetch(ctx, hanzi)
		if err != nil {
			log.Printf("sentence fetch (%s): %v", hanzi, err)
			return
		}
		n, err := sentences.Save(ctx, v.DB, vocabID, pairs)
		if err != nil {
			log.Printf("sentence save (%s): %v", hanzi, err)
			return
		}
		if n > 0 {
			log.Printf("cached %d sentences for %s (id=%d)", n, hanzi, vocabID)
		}
	}()
}


type updateCategoriesRequest struct {
	Categories []string `json:"categories"`
}

// UpdateCategories handles PUT /api/vocab/{id} — replaces the category
// associations for a vocabulary entry. Expects { "categories": [...] } in
// the request body. Creates new categories on the fly if they don't exist.
func (v *Vocab) UpdateCategories(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	vocabID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid vocab id", http.StatusBadRequest)
		return
	}

	var req updateCategoriesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	tx, err := v.DB.BeginTx(r.Context(), nil)
	if err != nil {
		log.Printf("update categories begin tx: %v", err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(r.Context(), "SELECT 1 FROM vocabulary WHERE id = ?", vocabID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "vocab not found", http.StatusNotFound)
			return
		}
		log.Printf("update categories check vocab: %v", err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	if _, err := tx.ExecContext(r.Context(), "DELETE FROM vocab_categories WHERE vocab_id = ?", vocabID); err != nil {
		log.Printf("update categories delete: %v", err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	for _, name := range req.Categories {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		var catID int64
		qerr := tx.QueryRowContext(r.Context(), "SELECT id FROM categories WHERE name = ?", name).Scan(&catID)
		if errors.Is(qerr, sql.ErrNoRows) {
			res, ierr := tx.ExecContext(r.Context(), "INSERT INTO categories (name) VALUES (?)", name)
			if ierr != nil {
				log.Printf("update categories create: %v", ierr)
				http.Error(w, "update failed", http.StatusInternalServerError)
				return
			}
			catID, _ = res.LastInsertId()
		} else if qerr != nil {
			log.Printf("update categories lookup: %v", qerr)
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
		if _, ierr := tx.ExecContext(r.Context(),
			"INSERT INTO vocab_categories (vocab_id, category_id) VALUES (?, ?)", vocabID, catID); ierr != nil {
			log.Printf("update categories insert: %v", ierr)
			http.Error(w, "update failed", http.StatusInternalServerError)
			return
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("update categories commit: %v", err)
		http.Error(w, "update failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	writeJSON(w, map[string]string{"status": "ok"})
}

type renameCategoryRequest struct {
	OldName string `json:"old_name"`
	NewName string `json:"new_name"`
}

// RenameCategory handles PUT /api/categories/rename — renames a category
// across the entire vocabulary bank. Expects { "old_name": "...", "new_name": "..." }.
func (v *Vocab) RenameCategory(w http.ResponseWriter, r *http.Request) {
	var req renameCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	req.OldName = strings.TrimSpace(req.OldName)
	req.NewName = strings.TrimSpace(req.NewName)
	if req.OldName == "" || req.NewName == "" {
		http.Error(w, "old_name and new_name are required", http.StatusBadRequest)
		return
	}
	if req.OldName == req.NewName {
		writeJSON(w, map[string]string{"status": "ok"})
		return
	}

	res, err := v.DB.ExecContext(r.Context(), "UPDATE categories SET name = ? WHERE name = ?", req.NewName, req.OldName)
	if err != nil {
		log.Printf("rename category: %v", err)
		http.Error(w, "rename failed", http.StatusInternalServerError)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		http.Error(w, "category not found", http.StatusNotFound)
		return
	}

	writeJSON(w, map[string]string{"status": "ok"})
}
