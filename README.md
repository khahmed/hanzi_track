# HánzìTrack

A self-hosted Chinese vocabulary tracker with adaptive AI agents and a self-mutation engine. Mobile-first SPA backed by Go and SQLite.

## Features

- **Pinyin autocomplete** — Search CC-CEDICT by pinyin with sub-millisecond prefix matching
- **Vocabulary bank** — Save words with categories, review with cached Tatoeba example sentences
- **Quizmaster** — AI-generated fill-in-the-blank questions targeting your weaker words
- **Conversational Partner** — Bilingual Mandarin chat with inline pinyin and linguistic coach notes
- **Pedagogical Learning Loop** — The Orchestrator monitors your accuracy trends and auto-tunes agent prompts to keep difficulty balanced
- **Self-Mutation Engine (DevOps Cockpit)** — Type a feature request in English; the server reads its own frontend source, calls an LLM to edit it, validates the changes, and opens a GitHub PR — all streamed in real time to a floating terminal drawer

## Architecture

```
┌─────────────────────────────────────────────────┐
│                  Go Backend                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐  │
│  │ Handlers │  │  Agents  │  │ DevOps/Mutator│  │
│  │ (API)    │──│(Quiz,    │──│(Self-mutation)│  │
│  │          │  │ Chat,    │  │              │  │
│  │          │  │Orch.)    │  │              │  │
│  └────┬─────┘  └────┬─────┘  └──────┬───────┘  │
│       │              │               │          │
│  ┌────▼──────────────▼───────────────▼───────┐  │
│  │           LLM Registry                     │  │
│  │  (Anthropic / OpenAI / DeepSeek)          │  │
│  └────────────────────────────────────────────┘  │
│       │                                           │
│  ┌────▼───────────────────────────────────────┐  │
│  │            SQLite (data/hanzitrack.db)      │  │
│  └────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────┘
         │                            │
         ▼                            ▼
┌──────────────────┐     ┌────────────────────────┐
│  Frontend (SPA)  │     │  OS Execution Layer    │
│  Alpine.js +     │     │  (git, gh, node —      │
│  Tailwind CDN    │     │   used by Mutator)     │
└──────────────────┘     └────────────────────────┘
```

**Key design choices:**
- Pure-Go SQLite ([modernc.org/sqlite](https://modernc.org/sqlite)) — no CGO, no system library dependencies
- Frontend is static HTML + Alpine.js + Tailwind CDN — no build step
- LLM providers are pluggable via environment variables
- DB connection pool capped at 1 — SQLite has a single global writer; multi-conn creates `SQLITE_BUSY` contention

## Quick start

### Prerequisites

- Go 1.25+ (or 1.21+ — any recent version should work)
- An LLM API key (Anthropic, OpenAI, or DeepSeek) for agent features

### Setup

```bash
# 1. Set up an LLM provider (at least one)
export ANTHROPIC_API_KEY="sk-..."
# or: export OPENAI_API_KEY="..."
# or: export DEEPSEEK_API_KEY="..."

# 2. (Optional) If the database doesn't exist yet, seed it:
go run ./cmd/seed              # downloads CC-CEDICT (~125k entries)
go run ./cmd/probe             # verify autocomplete latency

# 3. Start the server
go run ./backend

# 4. Open http://localhost:8080
```

The server creates `data/hanzitrack.db` automatically on first run (schema is embedded in `backend/db/schema.sql`). The seed step is only needed to populate the CC-CEDICT dictionary for pinyin search.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | Listen address |
| `-db` | `data/hanzitrack.db` | Path to SQLite database |
| `-frontend` | `frontend` | Static frontend directory |

## Configuration

### LLM Providers

Agent features (Quizmaster, Conversational Partner, Orchestrator) require an LLM API key. The server logs a warning if a key is missing and falls back to a stub client — agents will fail at request time with a placeholder response.

| Variable | Used by |
|----------|---------|
| `DEEPSEEK_API_KEY` | Agents set to `deepseek` provider (default for Quizmaster and Conversational Partner) |
| `OPENAI_API_KEY` | Agents set to `openai` provider |
| `ANTHROPIC_API_KEY` | Agents set to `anthropic` provider |

Provider and model per agent are configured at runtime in the `system_agents` database table (editable via SQLite).

### Self-Mutation Engine (DevOps Cockpit)

| Variable | Default | Description |
|----------|---------|-------------|
| `MUTATOR_API_KEY` | — | API key for the mutation LLM (required for code generation) |
| `MUTATOR_PROVIDER` | `anthropic` | LLM provider for code generation |
| `MUTATOR_MODEL` | `claude-sonnet-4-20250514` | Model for code generation |

The Mutator reads `app.js`, `index.html`, and `styles.css` from the frontend directory, packages them with the feature request, and sends them to the LLM. It never edits files outside the frontend sandbox.

### Agent system prompts

System prompts are stored in the `system_agents` table and can be modified at runtime. The Orchestrator (Auto-tune button) rewrites them based on your quiz accuracy trends — no config file editing needed.

## Usage

### Adding vocabulary

1. Type pinyin in the search bar (e.g., `pengyou`)
2. Select a match from the CC-CEDICT autocomplete results
3. Tag it with categories (e.g., "Duolingo Ch.1", "Food")
4. Press **Save to Notebook**

Example sentences are fetched from Tatoeba in the background after saving.

### Taking quizzes (Quizmaster)

Switch to the **Quizmaster** tab, optionally pick a category, and press **Generate question**. Each question presents a structural fill-in-the-blank sentence (e.g., 一边...一边...). After answering, the result is logged to `review_logs` for the Orchestrator.

### Chat (Conversational Partner)

Switch to the **Conversational Partner** tab and type a message. The partner replies in Mandarin Chinese with pinyin romanization and bracketed coach notes.

### Auto-tuning prompts (Orchestrator)

Press the **Auto-tune** button in any agent tab. The Orchestrator analyzes your recent quiz accuracy trends and rewrites the agent's system prompt to improve the balance of challenge and comprehension.

### Self-Mutation Cockpit

1. Click the gear icon (bottom-right corner) to open the DevOps Engine drawer
2. Type a feature request, e.g., "Add a delete button to each vocab card"
3. Press **Build it**

The backend streams real-time status: reading source files → contacting LLM → writing code → validating → creating branch → opening PR. The result is a GitHub pull request you can review and merge.

**Requires:** `MUTATOR_API_KEY` configured, `gh` CLI authenticated, and a git remote configured for PR creation.

## Project structure

```
├── backend/
│   ├── main.go                 # Server entry point, flag parsing, route wiring
│   ├── handlers/               # HTTP handlers (dict, vocab, quiz, chat, agents, devops)
│   ├── db/
│   │   ├── schema.sql          # Full SQLite schema
│   │   └── db.go               # Connection helper (PRAGMA foreign_keys ON, single conn pool)
│   ├── dictionary/             # CC-CEDICT parser
│   │   ├── parser.go
│   │   └── seed.go             # Downloads CEDICT source, populates table
│   ├── agents/
│   │   ├── orchestrator.go     # Loop 1 — analyzes review_logs, rewrites system prompts
│   │   └── manager.go          # Agent registry bridge
│   ├── devops/
│   │   └── mutator.go          # Loop 2 — self-mutation pipeline (read, LLM, write, validate, git)
│   ├── sentences/              # Tatoeba sentence fetcher
│   │   ├── sentences.go        # Fetcher interface
│   │   └── tatoeba.go          # Tatoeba API client
│   └── llm/                    # Pluggable LLM client abstraction
│       ├── client.go           # Client interface + Request/Response types
│       ├── registry.go         # Provider registry
│       ├── anthropic.go        # Anthropic Claude provider
│       ├── openai.go           # OpenAI provider
│       └── deepseek.go         # DeepSeek provider
├── cmd/
│   ├── seed/                   # CEDICT seeder CLI
│   └── probe/                  # Query latency probe CLI
├── frontend/
│   ├── index.html              # SPA layout (Alpine.js + Tailwind)
│   ├── app.js                  # Alpine component + helpers
│   └── styles.css              # Custom overrides
└── data/
    ├── hanzitrack.db           # SQLite database (generated)
    └── cedict_ts.u8            # CC-CEDICT source (downloaded by seed)
```

## Troubleshooting

### "no such table: cedict" / autocomplete returns nothing
Run `go run ./cmd/seed` to download and parse the CC-CEDICT dictionary. The seed is idempotent — it truncates and re-imports.

### "DEEPSEEK_API_KEY not set — using stub client"
Agent features need an LLM API key. Set at least one of `DEEPSEEK_API_KEY`, `OPENAI_API_KEY`, or `ANTHROPIC_API_KEY` depending on which provider your agents are configured to use. You can check or change the provider per agent in the `system_agents` table.

### "Mutator: stub client" / DevOps Engine returns parse errors
The mutation engine needs its own API key. Set `MUTATOR_API_KEY` (and optionally `MUTATOR_PROVIDER` / `MUTATOR_MODEL`). The default provider is `anthropic` with `claude-sonnet-4-20250514`.

### "gh not found" or PR creation fails
The mutation engine uses the GitHub CLI for PR creation. Install `gh` and authenticate with `gh auth login`. See [GitHub CLI docs](https://cli.github.com/).

### SQLITE_BUSY errors
The connection pool is intentionally capped at 1 connection. If you see `SQLITE_BUSY`, it's usually a long-running background query. The server logs the conflicting operation — wait and retry.

### Pinyin autocomplete returns irrelevant results for short prefixes
The query ranking sorts by (common-noun before proper-noun, then by pinyin length, then by ID). This works well for full-pinyin queries but is poor for short prefixes like `peng`. A proper fix needs HSK/BCC frequency data — not yet imported.

### "Handler tests skip if data/hanzitrack.db is missing"
The handler tests require a real database. Run `go run ./cmd/seed` first, or create an empty database with the schema:

```bash
sqlite3 data/hanzitrack.db < backend/db/schema.sql
```

## Development

```bash
go test ./...        # Run all tests
go test -v ./...     # Verbose output
go run ./cmd/probe   # Verify autocomplete latency (< 1ms)
```

Tests in `backend/handlers/` require a real database. Tests in `backend/devops/` and `backend/dictionary/` are self-contained.

## License

MIT
