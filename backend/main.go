// Command hanzitrack runs the HánzìTrack API server.
package main

import (
	"flag"
	"log"
	"net/http"

	"hanzitrack/backend/db"
	"hanzitrack/backend/handlers"
	"hanzitrack/backend/sentences"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", "data/hanzitrack.db", "path to SQLite database")
	frontendDir := flag.String("frontend", "frontend", "directory containing static frontend assets")
	flag.Parse()

	database, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}
	defer database.Close()

	vocab := &handlers.Vocab{
		DB:        database,
		Sentences: &sentences.Tatoeba{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dict/search", handlers.DictSearch(database))
	mux.HandleFunc("POST /api/vocab", vocab.Post)
	mux.HandleFunc("GET /api/vocab", vocab.List)
	mux.HandleFunc("GET /api/categories", vocab.Categories)
	mux.HandleFunc("GET /api/agents", handlers.ListAgents(database))
	// Static assets last — /api routes are more specific patterns and win in ServeMux.
	mux.Handle("/", http.FileServer(http.Dir(*frontendDir)))

	log.Printf("listening on %s (db=%s, frontend=%s)", *addr, *dbPath, *frontendDir)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
