// Package sentences fetches and persists example sentences for vocabulary
// entries. The Fetcher interface lets tests inject a fake; Tatoeba is the
// real-world implementation used by main.
package sentences

import (
	"context"
	"database/sql"
	"fmt"
)

type Pair struct {
	Chinese string
	English string
}

type Fetcher interface {
	Fetch(ctx context.Context, hanzi string) ([]Pair, error)
}

// Save writes pairs into example_sentences in a single transaction. Returns
// the number of rows inserted. Empty input is a no-op.
func Save(ctx context.Context, db *sql.DB, vocabID int64, pairs []Pair) (int, error) {
	if len(pairs) == 0 {
		return 0, nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO example_sentences (vocab_id, chinese_sentence, english_sentence) VALUES (?, ?, ?)")
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	for _, p := range pairs {
		if _, err := stmt.ExecContext(ctx, vocabID, p.Chinese, p.English); err != nil {
			return 0, fmt.Errorf("insert: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(pairs), nil
}
