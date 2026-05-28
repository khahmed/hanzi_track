# Building a Self-Evolving Language App with Agentic AI

*Lessons learned from creating HánzìTrack — a Chinese vocabulary tracker that rewrites its own code and tunes its own prompts.*

---

## The Duolingo Gap

I've been learning Chinese on Duolingo for over a year. The gamification is great, the streak pressure works, and the bite-sized lessons fit into a lunch break. But I kept running into the same wall: Duolingo doesn't let me organize words by category across lessons.

Words from Lesson 5 Unit 2 — "hotel," "restaurant," "booking" — live in the same flat list as Lesson 8 Unit 4's "directions" vocabulary. There's no way to group them, no way to quiz yourself on just the food words or just the travel phrases. The app decides what you review and when.

I wanted something different: a vocabulary tracker that I could organize my way, that would adapt to my weak spots, and — here's the provocative idea — that could modify itself to add features I wanted, on the fly, without me opening an IDE.

This is the story of building **HánzìTrack**, and what I learned about agentic AI along the way.

---

## Phase 1: From Idea to Specification

I didn't start with code. I started with a conversation.

I went to **Google Gemini** and described the problem: *"I'm learning Chinese on Duolingo and want a personal vocabulary tracker that lets me categorize words and adapt to my learning patterns."* Over several rounds of back-and-forth, Gemini helped me identify features, suggested technical approaches, and eventually produced a detailed technical specification.

The spec (SPEC.md) became the single source of truth for the entire project. It described:

- A Go backend serving a **CC-CEDICT** dictionary with sub-millisecond pinyin autocomplete
- A mobile-first SPA frontend using **Alpine.js** and **Tailwind CSS** without any build step
- **LLM-powered agents** for quiz generation and conversational practice
- Two adaptive loops: one that tunes teaching prompts based on accuracy trends, and one that lets the app modify its own frontend code

**Best practice:** Let an LLM produce a specification before writing code. It forces you to think through architecture, edge cases, and tradeoffs. The spec becomes a contract you can iterate on and a reference point the coding agent can consult.

---

## Phase 2: Breaking the Spec Into Slices

The spec was comprehensive — too big to build in one shot. I needed a way to decompose it.

This is where I discovered **Matt Pocock's skills framework** ([github.com/mattpocock/skills](https://github.com/mattpocock/skills)). It lets you define reusable skills that Claude Code can invoke. One skill in particular — `/to-issues` — became my primary planning tool.

The skill reads a spec and breaks it into **vertical-slice issues**: each slice is a thin, end-to-end path through every layer (schema → API → frontend → tests) that's independently demoable. A single slice might be "user can search pinyin and see autocomplete results" — not "build the search API" (horizontal), but the whole flow from keystroke to rendered result.

For HánzìTrack, the slices looked like:

1. **CEDICT parser + seed + dictionary search API** — the data pipeline that makes autocomplete work
2. **Frontend search UI + save modal** — the first user-facing interaction
3. **Vocabulary bank with categories** — browsing and organizing saved words
4. **Quizmaster agent** — AI-generated fill-in-the-blank questions
5. **Conversational Partner** — bilingual chat with linguistic coaching
6. **Pedagogical Learning Loop** — the Orchestrator that auto-tunes agent prompts
7. **Self-Mutation Engine** — the DevOps cockpit that edits frontend code at runtime

Each slice was tagged as AFK (can be implemented autonomously) or HITL (needs human review for architectural decisions). The agent worked through them in dependency order.

> **Screenshot suggestion:** The /to-issues skill output showing the broken-down vertical slices with AFK/HITL tags.

**Best practice:** Vertical slicing forces you to deliver working, integrated functionality every cycle. You never end up with a perfect backend API and no UI to test it. The skills framework makes this repeatable — you can reuse the same planning approach across projects.

---

## Phase 3: Building the Core Application

The architecture was deliberately minimal:

- **Backend:** Go standard library (net/http ServeMux) with pure-Go SQLite ([modernc.org/sqlite](https://modernc.org/sqlite)) — no CGO, no ORM, no framework
- **Frontend:** Single HTML file + one JavaScript file, Alpine.js for reactivity, Tailwind via CDN
- **LLM layer:** A provider-agnostic client interface supporting DeepSeek, OpenAI, and Anthropic

The core features came together quickly:

| Feature | How it works |
|---------|-------------|
| Pinyin autocomplete | Type `pengyou` → see 朋友, 朋友关系, etc. in <1ms via GLOB on indexed `pinyin_flat` |
| Vocabulary bank | Save words with categories, review with cached Tatoeba example sentences |
| Quizmaster | LLM generates fill-in-the-blank structural questions weighted toward your weaker words |
| Conversational Partner | Chat in Mandarin with inline pinyin and a linguistic copilot that flags mistakes |

The database schema stores everything in a single SQLite file — vocabulary, categories, review logs, chat history, and even the LLM system prompts for each agent. Nothing is hardcoded.

> **Screenshot suggestion:** The main app showing the Add view with pinyin search bar, autocomplete results, and the Recent list below.
> **Screenshot suggestion:** The Quizmaster tab showing a fill-in-the-blank structural question with answer options.

---

## Phase 4: The Pedagogical Learning Loop (Loop 1)

Here's where things get interesting.

The **Orchestrator** is a background agent that analyzes your quiz accuracy data. It looks at per-word correctness rates, identifies trends (improving? declining? plateauing?), and **rewrites the system prompts** of the Quizmaster and Conversational Partner agents to hit a target accuracy of ~75%.

If you're getting 95% of questions right, the Orchestrator makes the prompts harder — ask about less common grammar structures, use more challenging vocabulary. If you're at 50%, it simplifies them. The sweet spot for learning is the zone of proximal development: not too easy, not too hard.

This runs on-demand via an "Auto-tune" button in the UI. A future version could trigger it automatically based on trends.

> **Screenshot suggestion:** The agent tab showing the accuracy badge and the Auto-tune button, with the orchestration result banner visible below.

**Best practice:** Don't hardcode prompts. Store them in a database, track their effectiveness, and let an LLM optimize them based on real usage data. Your teaching prompts should evolve as your users do.

---

## Phase 5: The Self-Mutation Engine (Loop 2)

This was the experiment I was most curious about: **can an application modify its own source code at runtime, safely?**

The DevOps Cockpit is a floating drawer accessible from a gear icon in the bottom-right corner. You type a feature request in English:

> *"Add a toggle to switch quiz questions between pinyin and hanzi characters"*

The backend:

1. Reads the current frontend files (app.js, index.html, styles.css)
2. Packages them with the feature request and sends them to an LLM
3. The LLM returns modified versions of the files
4. The mutator writes the changes to a temp directory, runs validation (syntax checks, HTML structure)
5. If validation fails, it auto-corrects by feeding the error back to the LLM (up to 3 attempts)
6. If validation passes, it creates a git feature branch, commits, pushes, and opens a GitHub PR
7. All of this streams back to the UI in real-time via Server-Sent Events, so the user sees a terminal-style log: "Reading source files..." → "Consulting engineer API..." → "Writing code updates..." → "Running validation..." → "PR #42 opened!"

The whole experience feels like a live coding session inside your browser.

### Safety First

The sandbox is the critical design decision. The mutator can **only** edit three files: `app.js`, `index.html`, and `styles.css`. No backend Go files, no database schema, no configuration. This was intentional:

- Frontend mistakes are cosmetic — a broken button vs. a crashed server
- Static files are served and reload on browser refresh — no restart needed
- Git branches + PRs mean every change is reviewable and revertible

I tested this by having the mutator add a pinyin toggle to the QuizMaster interface. It modified `app.js` and `index.html` correctly — added a toggle button, switching logic, and the wiring. The only issue was the **backend** needed a new API field to return pinyin data alongside the hanzi questions. Since the backend was outside the sandbox, I added that manually.

> **Screenshot suggestion:** The DevOps cockpit drawer open with the terminal log showing real-time SSE events — "Reading source files..." → "Consulting LLM..." → "Writing code..." → "PR created!"
> **Screenshot suggestion:** The GitHub PR page showing the auto-generated changes with title "feat: add pinyin toggle to quiz questions"

**Best practice:** Sandbox your agent's write access. Let it modify presentation layer code, not infrastructure. Use git branches as a safety net — every mutation is a PR, not a direct push to main. And invest in validation: if `node --check` passes on the generated JavaScript, you're in good shape.

---

## Key Lessons Learned

### 1. Spec-first development works

Letting an LLM generate a detailed specification before writing any code gave me a roadmap that survived the entire project. The spec evolved as I encountered edge cases, but having it upfront prevented architectural drift.

### 2. Vertical slices beat horizontal layers

Building a thin slice through every layer — database query → API endpoint → UI component → test — meant every milestone was demonstrable. I never had a "perfect backend with no UI" situation.

### 3. Agentic planning needs human judgment

The `/to-issues` skill did a good job of breaking work into slices, but the HITL/AFK distinction was essential. Architectural decisions (how to structure the LLM provider layer, where to draw the mutation sandbox) needed human input. Implementation work (writing the parser, building the UI) could be delegated.

### 4. Self-modification is viable — with boundaries

The mutation engine successfully added real features to the frontend. The guardrails — file sandbox, syntax validation, git PR workflow — made it safe enough to trust. But I would not extend the sandbox to backend Go code without significant additional validation (compilation checks, integration tests, possibly a staging server).

### 5. Prompt engineering is a runtime concern

Storing system prompts in a database and letting an Orchestrator rewrite them based on learning metrics is more powerful than hand-tuning them in a config file. The prompts evolve with the user.

### 6. The developer experience matters

Streaming SSE events to a terminal-style log in the mutation cockpit made the self-modification process feel tangible and debuggable. Without real-time feedback, the mutator would have been a black box. With it, I could see exactly where it was in the pipeline and diagnose failures immediately.

---

## Try It Yourself

HánzìTrack is open source at [github.com/khahmed/hanzi_track](https://github.com/khahmed/hanzi_track). To run it:

```bash
git clone https://github.com/khahmed/hanzi_track
cd hanzi_track
export DEEPSEEK_API_KEY="sk-..."   # or OpenAI / Anthropic
go run ./cmd/seed                  # download CC-CEDICT dictionary
go run ./backend                   # start on :8080
```

Open `http://localhost:8080` and start adding vocabulary. The DevOps cockpit needs `MUTATOR_API_KEY` set separately — or just use the existing agents without self-mutation.

---

The most interesting part of this project wasn't the Chinese vocabulary tracker itself. It was proving that an application can participate in its own evolution — that with the right architecture, an app can grow features without a developer opening an IDE. The agent doesn't replace the developer; it handles the mechanical work of code generation within safe boundaries, while the developer focuses on architecture, safety, and direction.

That's a future I want to build toward.
