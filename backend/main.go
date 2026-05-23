// Command hanzitrack runs the HánzìTrack API server.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"hanzitrack/backend/db"
	"hanzitrack/backend/handlers"
	"hanzitrack/backend/llm"
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

	// LLM registry: start with stubs for all three providers so any agent
	// can at least respond with a placeholder. If DEEPSEEK_API_KEY is set,
	// swap the deepseek slot for the real client. Missing key is a warning,
	// not a fatal — agents fail at request time, not boot time.
	registry := llm.DefaultRegistry()
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		registry.Register("deepseek", llm.NewDeepSeek(key))
		log.Printf("deepseek: live client registered")
	} else {
		log.Printf("deepseek: DEEPSEEK_API_KEY not set — using stub client")
	}

	quiz := &handlers.Quiz{DB: database, Registry: registry}
	chat := &handlers.Chat{DB: database, Registry: registry}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dict/search", handlers.DictSearch(database))
	mux.HandleFunc("POST /api/vocab", vocab.Post)
	mux.HandleFunc("GET /api/vocab", vocab.List)
	mux.HandleFunc("GET /api/categories", vocab.Categories)
	mux.HandleFunc("GET /api/agents", handlers.ListAgents(database))
	mux.HandleFunc("GET /api/agents/quiz", quiz.Generate)
	mux.HandleFunc("POST /api/agents/quiz/submit", quiz.Submit)
	mux.HandleFunc("POST /api/agents/chat", chat.Send)
	mux.HandleFunc("GET /api/agents/chat/history", chat.History)
	// Static assets last — /api routes are more specific patterns and win in ServeMux.
	mux.Handle("/", http.FileServer(http.Dir(*frontendDir)))

	log.Printf("listening on %s (db=%s, frontend=%s)", *addr, *dbPath, *frontendDir)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
