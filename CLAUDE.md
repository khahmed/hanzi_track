# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

MVP feature-complete end-to-end. `SPEC.md` is still the source of truth for architecture, schema, and API shape — read it before making structural decisions. Backend: CEDICT parser + seeder, all four API endpoints, Tatoeba background worker. Frontend: single-file Alpine SPA at `frontend/index.html` + `frontend/app.js`, served by the Go server at `/`.

## Build / run

- `go test ./...` — runs all unit + handler tests. Handler tests skip if `data/hanzitrack.db` is missing.
- `go run ./cmd/seed` — downloads CEDICT if missing, populates `cedict` table. Idempotent (truncates first). ~2s, ~125k rows.
- `go run ./cmd/probe` — sanity-checks the autocomplete index hits sub-ms. Useful after schema or query changes.
- `go run ./backend [-addr :8080] [-db data/hanzitrack.db]` — runs the API server.
- Pure-Go SQLite (`modernc.org/sqlite`) — no CGO, no system libs.

## What this is

HánzìTrack: a self-hosted Chinese vocabulary tracker. Mobile-first SPA backed by a Go server and a single SQLite file. The user adds words while practicing (e.g., from Duolingo) by typing pinyin, picking a CC-CEDICT match, tagging it, and reviewing it later with cached Tatoeba example sentences.

## Stack and layout

- Backend: Go standard library (Chi or Fiber acceptable if needed). Lives in `backend/` — `main.go`, `handlers/`, `db/`, `dictionary/`.
- Frontend: static HTML + vanilla JS (Alpine.js acceptable) + Tailwind via CDN. Lives in `frontend/`. Go serves it directly.
- Data: SQLite at `data/hanzitrack.db`. CC-CEDICT source at `data/cedict_ts.u8`, parsed into the `cedict` table on first run.
- No build step on the frontend by design — keep it that way unless there's a strong reason.

## Architectural conventions baked into the spec

- **Pinyin search uses `pinyin_flat`** (tone-stripped, e.g. `nihao`) for both `cedict` and `vocabulary`. Whenever pinyin is stored, store both the tonal form and the flat form. The index on `cedict(pinyin_flat)` is what makes autocomplete fast.
- **Query `pinyin_flat` with `GLOB`, not `LIKE`.** SQLite's `LIKE` is case-insensitive by default and won't use a BINARY index — verified via `EXPLAIN QUERY PLAN`. `pinyin_flat` is already lowercased by `dictionary.FlattenPinyin`, so `WHERE pinyin_flat GLOB 'nihao*'` is the canonical prefix-match query and hits the index (~200µs vs ~50ms full-scan).
- **CC-CEDICT line format** is `Simplified Traditional [pin yin] /def1/def2/`. Parser lives in `backend/dictionary/`.
- **Sentence fetching is asynchronous.** `POST /api/vocab` returns immediately after the word is saved; a background worker fetches Tatoeba sentences and writes them to `example_sentences`. Don't make the save endpoint block on Tatoeba. The fetcher is injectable (`sentences.Fetcher` interface) — tests pass a fake; production uses `sentences.Tatoeba`. `Vocab.WaitWorkers()` drains in-flight workers (used in tests; could be wired to graceful shutdown later).
- **`POST /api/vocab` is idempotent on `hanzi`.** Second POST with the same characters returns the existing row's id with `created: false`, merges in any new categories, and does NOT re-fetch sentences. This is an interpretation of the spec, not a literal requirement — change if you want raw-append behavior.
- **DB pool capped at 1 connection** (`db.SetMaxOpenConns(1)`). SQLite has a single global writer anyway; multi-conn just creates `SQLITE_BUSY` contention between the POST handler and the background worker. For a single-user app, the loss of read parallelism is irrelevant.
- **`/api/dict/search` ranking is best-effort, no frequency data.** Order is: (1) common-noun pinyin before capitalised proper nouns, (2) shorter `pinyin_flat` first, (3) `id` ASC. This ranks **full pinyin** queries well (`pengyou` → 朋友 first) but is poor for **short prefixes** like `peng` — rare Extension-A chars surface ahead of common ones. A proper fix needs HSK/BCC frequency data; not yet imported.
- **Numeric pinyin is converted to tone-mark pinyin at render time** by `toneMark()` in `app.js`, not stored that way. The placement rule is: `a` wins, then `o`, then `e`, otherwise the last vowel of the cluster. `u:` is rewritten to `ü` before marking. Server stores only the CEDICT-form numeric pinyin.
- **Frontend is a single-file Alpine SPA** (`index.html` + `app.js`, no build step). Two views toggled by `view` state, bottom tab nav, slide-up save modal. Tailwind + Alpine via CDN. Save flow re-fetches categories + recent; bank view re-fetches lazily on tab switch and after each save.
- **`search_count` on `vocabulary`** is incremented on lookups and used to surface weak words — don't drop it when adding new query paths.
- Cascade deletes (`ON DELETE CASCADE`) are relied on for `vocab_categories` and `example_sentences`. Enable `PRAGMA foreign_keys = ON` on every SQLite connection or the cascades silently no-op.

## API surface (from spec)

- `GET /` — serves `frontend/index.html`
- `GET /api/dict/search?q=<pinyin>` — autocomplete against `cedict.pinyin_flat`. Target <10ms.
- `POST /api/vocab` — body `{ hanzi, pinyin, english, categories: [...] }`. Saves + kicks off sentence fetch.
- `GET /api/vocab` — supports `?category=` and `?search=` filters.
- `GET /api/categories` — list of category names.
- `PUT /api/categories/rename` — body `{ old_name, new_name }`. Renames a category across the bank.
- `PUT /api/vocab/{id}` — body `{ categories: [...] }`. Replaces category associations for a word.

## Phasing

1. Data pipeline: CEDICT parser + seed + `/api/dict/search`.
2. Frontend UI (the spec expects this to be built in Cursor, but either tool can do it).
3. Wire frontend to API; add the Tatoeba background worker.

Don't skip phase 1 — autocomplete latency is the feature the rest of the UX depends on.

## Agent skills

### Issue tracker

Issues live as markdown files under `.scratch/<feature-slug>/`. See `docs/agents/issue-tracker.md`.

### Triage labels

Default vocabulary: needs-triage, needs-info, ready-for-agent, ready-for-human, wontfix. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context — one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

