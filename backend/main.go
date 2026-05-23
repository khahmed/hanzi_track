// Command hanzitrack runs the HánzìTrack API server.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"hanzitrack/backend/agents"
	"hanzitrack/backend/db"
	"hanzitrack/backend/devops"
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
	// can at least respond with a placeholder. For each provider, if the
	// matching env var is set, swap the stub for the real client. Missing
	// keys are warnings, not fatal — agents fail at request time, not boot
	// time, so an unconfigured provider only matters if an agent is wired
	// to use it.
	registry := llm.DefaultRegistry()
	if key := os.Getenv("DEEPSEEK_API_KEY"); key != "" {
		registry.Register("deepseek", llm.NewDeepSeek(key))
		log.Printf("deepseek: live client registered")
	} else {
		log.Printf("deepseek: DEEPSEEK_API_KEY not set — using stub client")
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		registry.Register("openai", llm.NewOpenAI(key))
		log.Printf("openai: live client registered")
	} else {
		log.Printf("openai: OPENAI_API_KEY not set — using stub client")
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		registry.Register("anthropic", llm.NewAnthropic(key))
		log.Printf("anthropic: live client registered")
	} else {
		log.Printf("anthropic: ANTHROPIC_API_KEY not set — using stub client")
	}

	// Mutator LLM client — env-configurable. Defaults to anthropic so the
	// self-mutation loop (Loop 2) uses the strongest model for code gen.
	mutatorProvider := getEnv("MUTATOR_PROVIDER", "anthropic")
	mutatorModel := getEnv("MUTATOR_MODEL", "claude-sonnet-4-20250514")
	if key := os.Getenv("MUTATOR_API_KEY"); key != "" {
		switch mutatorProvider {
		case "anthropic":
			registry.Register("mutator", llm.NewAnthropic(key))
		case "openai":
			registry.Register("mutator", llm.NewOpenAI(key))
		case "deepseek":
			registry.Register("mutator", llm.NewDeepSeek(key))
		default:
			log.Printf("mutator: unknown provider %q, falling back to stub", mutatorProvider)
		}
		log.Printf("mutator: live %s client registered", mutatorProvider)
	} else {
		log.Printf("mutator: MUTATOR_API_KEY not set — using stub client")
	}

	quiz := &handlers.Quiz{DB: database, Registry: registry}
	chat := &handlers.Chat{DB: database, Registry: registry}
	orch := &agents.Orchestrator{DB: database, Registry: registry}
	mutator := &devops.Mutator{
		DB:          database,
		Registry:    registry,
		FrontendDir: *frontendDir,
		Provider:    mutatorProvider,
		Model:       mutatorModel,
	}

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
	mux.HandleFunc("GET /api/agents/stats", handlers.Stats(orch))
	mux.HandleFunc("POST /api/agents/orchestrate", handlers.Orchestrate(orch))
	mux.HandleFunc("POST /api/devops/mutate", handlers.Mutate(mutator))
	// Static assets last — /api routes are more specific patterns and win in ServeMux.
	mux.Handle("/", http.FileServer(http.Dir(*frontendDir)))

	log.Printf("listening on %s (db=%s, frontend=%s)", *addr, *dbPath, *frontendDir)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}

// getEnv returns the env var value or a default if unset.
func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
