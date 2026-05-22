Technical Specification: HánzìTrack (The Self-Evolving & Self-Mutating Engine)

A self-hosted, self-evolving, and self-mutating Chinese vocabulary tracker and hyper-personalized learning system optimized for mobile and desktop web browsers.
1. System Architecture Overview

    Backend: Go (Golang) standard library with os/exec systems integration, connecting to an external LLM Provider API (e.g., Anthropic Claude) via a secure HTTP transport client layer.

    Database: SQLite3 for relational, zero-configuration storage of tracking data, interaction historical states, and adaptive system/agent configurations.

    Frontend: Template-driven Single-Page Application (SPA) layout powered by HTML5, CSS3 (Tailwind CSS via CDN), and Alpine.js. Views and configuration navigation components are built dynamically at runtime by querying the database's active engine schemas.

    Core Paradigm Shift:

        Meta-Prompt Injection: No system prompts or learning parameters are hardcoded. Agents are dynamically generated or modified based on relational learning metrics.

        Runtime Source Self-Mutation: An isolated, secure pipeline allowing the user to type features in plain English directly into the client application. The Go server invokes an AI developer session at runtime, edits the source code files, runs safe local tests, and issues system-level Git/PR mutations automatically.

2. Directory Structure
Plaintext

hanzitrack/
├── backend/               <-- Develop primarily in Claude Code
│   ├── main.go            <-- Server initialization, static routing & config
│   ├── handlers/          <-- API Endpoints (Dynamic Routing, Streaming, and Chat)
│   ├── db/                <-- SQLite connection & schema migrations
│   ├── dictionary/        <-- CC-CEDICT parser logic
│   ├── agents/            
│   │   ├── manager.go     <-- Instantiates agents dynamically from database properties
│   │   └── orchestrator.go <-- Analyzes logs, scores trends, rewrites system prompts
│   └── devops/            
│       └── mutator.go     <-- Core logic for file disk reading/writing, shell execution, lint checks
├── frontend/              <-- Open this folder in Cursor
│   ├── index.html         <-- Main dynamic UI Layout (Add, Bank, Agent-Rendered Panels)
│   ├── app.js             <-- Fetch requests, dynamic action routing, execution state
│   └── styles.css         <-- Custom Tailwind configurations/overrides
├── data/
│   ├── cedict_ts.u8       <-- Source dictionary file
│   └── hanzitrack.db      <-- Generated SQLite file
└── README.md

3. Database Schema (backend/db/schema.sql)

This configuration turns both the learning environment prompts and the runtime system profiles into modular database rows.
SQL

-- Core vocabulary table
CREATE TABLE IF NOT EXISTS vocabulary (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hanzi TEXT NOT NULL,
    pinyin TEXT NOT NULL, 
    pinyin_flat TEXT NOT NULL, 
    english TEXT NOT NULL,
    search_count INTEGER DEFAULT 0, 
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Tagging/Categories
CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL
);

-- Many-to-Many mapping table
CREATE TABLE IF NOT EXISTS vocab_categories (
    vocab_id INTEGER,
    category_id INTEGER,
    PRIMARY KEY (vocab_id, category_id),
    FOREIGN KEY(vocab_id) REFERENCES vocabulary(id) ON DELETE CASCADE,
    FOREIGN KEY(category_id) REFERENCES categories(id) ON DELETE CASCADE
);

-- Cached example sentences
CREATE TABLE IF NOT EXISTS example_sentences (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vocab_id INTEGER,
    chinese_sentence TEXT NOT NULL,
    english_sentence TEXT NOT NULL,
    FOREIGN KEY(vocab_id) REFERENCES vocabulary(id) ON DELETE CASCADE
);

-- Internal CC-CEDICT lookup table
CREATE TABLE IF NOT EXISTS cedict (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hanzi_simplified TEXT NOT NULL,
    pinyin TEXT NOT NULL,
    pinyin_flat TEXT NOT NULL,
    english TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_cedict_pinyin ON cedict(pinyin_flat);

-- Review logs tracking quiz histories for Orchestrator optimization
CREATE TABLE IF NOT EXISTS review_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vocab_id INTEGER,
    agent_type TEXT NOT NULL, -- The agent that initiated the test
    quiz_type TEXT NOT NULL,  -- e.g., "structural_fill", "tone", "listening"
    is_correct INTEGER NOT NULL, -- 0 = False, 1 = True
    reviewed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(vocab_id) REFERENCES vocabulary(id) ON DELETE CASCADE
);

-- Chat session history
CREATE TABLE IF NOT EXISTS chat_messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    role TEXT NOT NULL, -- "user", "assistant", "system_coach"
    content TEXT NOT NULL,
    pinyin TEXT,        
    coach_notes TEXT,   
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Adaptive Prompt Engineering Configuration Engine
CREATE TABLE IF NOT EXISTS system_agents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,      -- e.g., "quizmaster", "conversationalist"
    display_name TEXT NOT NULL,    -- Tab name rendered in Frontend
    system_prompt TEXT NOT NULL,   -- The mutable system instruction string
    temperature REAL DEFAULT 0.3,  
    is_active INTEGER DEFAULT 1,   -- 1 = Active, 0 = Inactive/Deprecated
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Runtime Self-Mutation Job Audit Trail
CREATE TABLE IF NOT EXISTS mutation_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_request TEXT NOT NULL,
    target_branch TEXT NOT NULL,
    status TEXT NOT NULL,         -- "processing", "lint_passed", "failed_validation", "pr_created"
    error_log TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);


4. Multi-Agent Engine Core Logic (Go Engine Backend)

The Go backend runs an agent layer utilizing system prompts that process relational database context into tailored LLM inputs.
Plaintext

               +----------------------------------------+
               |            SQLite Database             |
               | (Vocabulary, Review Logs, Categories)  |
               +----------------------------------------+
                                   |
                                   v
               +----------------------------------------+
               |        Go Agent Manager Router         |
               +----------------------------------------+
                /                  |                   \
               /                   |                    \
              v                    v                     v
   +--------------------+  +--------------------+  +--------------------+
   |    Quizmaster      |  |   Conversational   |  | Linguistic Copilot |
   |    Agent Core      |  |   Partner Agent    |  |     Agent Core     |
   | (Structural Slots) |  | (Bilingual Engine) |  | (Grammar Monitor)  |
   +--------------------+  +--------------------+  +--------------------+

Agent A: The Quizmaster

    System Prompt Target: "You are an expert Chinese pedagogy teacher specializing in syntactic frame structures. Your task is to use the user's provided list of learned vocabulary words to construct fill-in-the-blank structural questions."

    Context Payload: Pulls an array of words from vocabulary matching a chosen category filter, paired with historical metrics from review_logs.

    Execution Strategy: Builds a sentence skeleton around common structural skeletons (e.g., 虽然...但是..., 一边...一边..., Subject + Time + Place + Verb). It sends a JSON output pattern back containing the sentence question, the answer options, and the matching index mapping keys.

Agent B: The Conversational Partner & Linguistic Copilot (Combined Pipeline)

    System Prompt Target: "You function as a dual-role language environment. Primary Persona (Conversational Partner): Speak entirely in natural, simple Mandarin Chinese matching a target situation (e.g., booking a room, buying food). You must limit your sentence structures and vocabulary complexity to the user's logged dataset. Secondary Persona (Linguistic Copilot): Append an independent, bracketed analysis block translating advanced idioms, flagging tone adjustments, and noting corrections without interrupting the flow of dialogue."

    Context Payload: Pulls all rows from vocabulary along with the last 10 records from chat_messages.


5. Double-Loop Adaptive Architecture

The application implements two entirely distinct adaptive loops running in parallel:
Loop 1: The Pedagogical Learning Loop (Orchestrator)

The backend running process triggers the Orchestrator Agent asynchronously. It processes accuracy arrays extracted from review_logs and rewrites the instructions stored in the system_agents table to maintain an optimal balance between challenge and comprehension.
Loop 2: The Self-Mutation Engineering Loop (DevOps Engine)

When the user submits a text feature request from the client front end, the server halts runtime learning pipelines and runs a dedicated environment update loop:
Plaintext

[ Frontend Feature Input Bar ] ---> Triggers POST /api/devops/mutate
                                               |
                                               v
                 +-----------------------------------------------------------+
                 |                       Go Backend                          |
                 | 1. Reads local files on disk (index.html, app.js)         |
                 | 2. Packages source into JSON Payload + User Request       |
                 | 3. Submits to Anthropic Claude Engineer Persona API       |
                 +-----------------------------------------------------------+
                                               |
                                               v
                 +-----------------------------------------------------------+
                 |               OS Execution Layer (os/exec)                |
                 | 1. Runs 'git checkout -b feature/requested-node'          |
                 | 2. Overwrites local target script files on disk           |
                 | 3. Automatically executes JavaScript / HTML validation    |
                 +-----------------------------------------------------------+
                                               |
                     +-------------------------+-------------------------+
                     | (Validation Success)                              | (Validation Fails)
                     v                                                   v
   +----------------------------------+                +----------------------------------+
   | 4. Runs 'git commit & git push'  |                | 4. Rolls back changes on disk    |
   | 5. Triggers GitHub CLI: 'gh pr'  |                | 5. Logs error code back to user  |
   | 6. Returns code link to client   |                +----------------------------------+
   +----------------------------------+

6. System API Endpoints (Backend Blueprint for Claude Code)
Core Base Handlers

    GET / -> Serves frontend/index.html.

    GET /api/dict/search?q=pinyin -> Autocomplete lookup on cedict.

    POST /api/vocab -> Saves a new word to track.

    GET /api/vocab -> Retrieves vocabulary bank tracking rows.

    GET /api/agents/quiz?category=Food -> Queries the Quizmaster Agent. Returns a dynamically generated structural sentence question array:
    JSON

    {
      "structure": "一边...一边...",
      "question_chinese": "他喜欢一边____，一边听音乐。",
      "options": ["吃饭", "苹果", "学校", "昨天"],
      "correct_answer": "吃饭",
      "vocab_id": 42,
      "explanation": "吃饭 (chīfàn - to eat) fits the blank as a verb action happening concurrently with listening to music."
    }

    POST /api/agents/quiz/submit -> Submits user selection and records results to review_logs.

    POST /api/agents/chat -> Receives messages from the interactive chat terminal. Communicates with your LLM engine using a streaming or clean text payload response that separates the Chinese dialogue response, the computed Pinyin layout string, and the accompanying Coach notes block.

Dynamic Agent Engine Routes

    GET /api/agents -> Returns all active records from system_agents to build the client tab interface layout.

    POST /api/agents/action -> Unified execution pipeline router. Evaluates requests based on incoming payload properties: { "agent_name": "quizmaster", "input_data": {} }.

    POST /api/agents/orchestrate -> Explicitly invokes the Orchestrator to update prompt configurations based on learning logs.

NEW: Self-Mutation System Routes

    POST /api/devops/mutate

        Payload: { "feature_description": "Add an edit category button onto each vocab card" }

        Response Stream: Returns real-time text logs tracking mutation status updates directly to the front-end interface:
        "Processing request..." → "Contacting engineer API..." → "Writing code updates to branch feature/edit-button..." → "Running lint checks..." → "PR #42 opened successfully!"

6. Dynamic Frontend UI Architecture (Frontend Blueprint for Cursor)

The frontend is completely template-driven so it can accommodate new features or views introduced by the self-mutating engine without crashing.
The Self-Evolving View Layout Container

The main layout switches its view mode parameter depending on what active agent or configuration block is called from the client interface layer.
NEW: The Developer Mutation Cockpit (Floating Drawer Interface)

    UI Trigger: A small settings icon at the bottom corner of the viewport pulls up an overlay panel called the Developer Mutation Cockpit.

    Input Elements: * A descriptive text field placeholder: "What feature would you like to build into HánzìTrack right now? (e.g., 'Add a button to re-categorize words')".

        A primary action submission button node.

    Terminal Diagnostics Component: A scrollable box that stays hidden until submission. It streams real-time status output updates straight from the backend handler using server-sent messages or standard fetch streams.

7. Hyper-Tuning & Self-Mutation Safeguards

When writing your implementation code via Claude Code, enforce these safety constraints to avoid corrupted system states:

    Strict File Sandbox Boundaries: The mutator.go module is strictly banned from reading or editing files outside the explicit web frontend folder (frontend/index.html, frontend/app.js, frontend/styles.css). This guarantees that even if the AI introduces an unexpected bug, it will never corrupt or crash the running Go backend process.

    The Human-In-The-Loop Validation Engine: The server never merges code modifications directly into the main operational branch. It must run an isolated Git workflow strategy (git checkout -b, followed by creating an upstream GitHub Pull Request). This allows you to verify code modifications inside Cursor on your laptop before hitting merge.

    Automated Error Self-Correction Loop: If your verification step configuration fails (e.g., a script linter throws an error code during testing), the server reads that terminal failure, redirects it straight back to the engineer API as a system instruction snippet, and prompts: "Your modification caused a compilation error. Fix the syntax and try again."