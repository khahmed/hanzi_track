package db

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Open opens (or creates) the SQLite database at path, enables foreign keys,
// and applies the schema. Safe to call on an existing DB — schema uses IF NOT EXISTS.
func Open(path string) (*sql.DB, error) {
	// busy_timeout makes writers wait for the write lock instead of failing
	// instantly — required because the sentence-fetch worker writes
	// concurrently with subsequent POST /api/vocab handlers.
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := conn.Ping(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := conn.Exec(schemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// SQLite has one global writer at a time. Pinning the pool to a single
	// connection serialises writes in-process and removes the SQLITE_BUSY
	// contention we saw between concurrent POST handlers and the background
	// sentence worker. For a single-user app the loss of read parallelism
	// is irrelevant.
	conn.SetMaxOpenConns(1)
	return conn, nil
}
