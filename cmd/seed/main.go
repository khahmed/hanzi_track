// Command seed populates the cedict table from the CC-CEDICT text file.
// Downloads the archive on first run if data/cedict_ts.u8 is missing.
package main

import (
	"flag"
	"log"
	"time"

	"hanzitrack/backend/db"
	"hanzitrack/backend/dictionary"
)

func main() {
	dbPath := flag.String("db", "data/hanzitrack.db", "path to SQLite database")
	cedictPath := flag.String("cedict", "data/cedict_ts.u8", "path to CC-CEDICT text file")
	flag.Parse()

	if err := dictionary.EnsureCedictFile(*cedictPath); err != nil {
		log.Fatalf("cedict file: %v", err)
	}

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	start := time.Now()
	inserted, skipped, err := dictionary.Seed(database, *cedictPath)
	if err != nil {
		log.Fatalf("seed: %v", err)
	}
	log.Printf("seeded %d entries (%d skipped) in %s", inserted, skipped, time.Since(start))
}
