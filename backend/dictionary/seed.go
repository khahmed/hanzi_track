package dictionary

import (
	"bufio"
	"compress/gzip"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
)

// CedictURL is the mdbg.net mirror of CC-CEDICT (gzipped UTF-8).
const CedictURL = "https://www.mdbg.net/chinese/export/cedict/cedict_1_0_ts_utf-8_mdbg.txt.gz"

// EnsureCedictFile makes sure path exists; if not, downloads the CEDICT
// archive from CedictURL and writes the decompressed text there.
func EnsureCedictFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	log.Printf("downloading CEDICT from %s", CedictURL)
	resp, err := http.Get(CedictURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()

	out, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, gz); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Seed parses the CEDICT file at path and inserts every entry into the
// cedict table inside a single transaction. It returns the number of rows
// inserted and the number of malformed lines that were skipped.
//
// Seed truncates the cedict table first, so re-running gives a clean state.
func Seed(database *sql.DB, path string) (inserted, skipped int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	tx, err := database.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	if _, err = tx.Exec("DELETE FROM cedict"); err != nil {
		return 0, 0, fmt.Errorf("truncate: %w", err)
	}

	stmt, err := tx.Prepare(
		"INSERT INTO cedict (hanzi_simplified, pinyin, pinyin_flat, english) VALUES (?, ?, ?, ?)",
	)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		entry, perr := ParseLine(scanner.Text())
		if perr != nil {
			skipped++
			continue
		}
		if entry == nil {
			continue
		}
		if _, err = stmt.Exec(entry.Simplified, entry.Pinyin, entry.PinyinFlat, entry.English); err != nil {
			return inserted, skipped, fmt.Errorf("insert: %w", err)
		}
		inserted++
	}
	if err = scanner.Err(); err != nil {
		return inserted, skipped, fmt.Errorf("scan: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return inserted, skipped, fmt.Errorf("commit: %w", err)
	}
	return inserted, skipped, nil
}
