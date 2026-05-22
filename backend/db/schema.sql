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

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL
);

CREATE TABLE IF NOT EXISTS vocab_categories (
    vocab_id INTEGER,
    category_id INTEGER,
    PRIMARY KEY (vocab_id, category_id),
    FOREIGN KEY(vocab_id) REFERENCES vocabulary(id) ON DELETE CASCADE,
    FOREIGN KEY(category_id) REFERENCES categories(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS example_sentences (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    vocab_id INTEGER,
    chinese_sentence TEXT NOT NULL,
    english_sentence TEXT NOT NULL,
    FOREIGN KEY(vocab_id) REFERENCES vocabulary(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS cedict (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hanzi_simplified TEXT NOT NULL,
    pinyin TEXT NOT NULL,
    pinyin_flat TEXT NOT NULL,
    english TEXT NOT NULL
);
-- Query this index with GLOB, not LIKE: SQLite's LIKE is case-insensitive
-- by default and won't use a BINARY index. pinyin_flat is already lowercased
-- by FlattenPinyin, so GLOB 'nihao*' is the right prefix-match query.
CREATE INDEX IF NOT EXISTS idx_cedict_pinyin ON cedict(pinyin_flat);

-- Adaptive Prompt Engineering Configuration Engine. provider+model are
-- per-agent so the same registry can route quizmaster and conversationalist
-- to different LLM backends. system_prompt is mutable at runtime by the
-- Orchestrator (Loop 1, Section 5 of SPEC).
CREATE TABLE IF NOT EXISTS system_agents (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    display_name TEXT NOT NULL,
    system_prompt TEXT NOT NULL,
    temperature REAL DEFAULT 0.3,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    is_active INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Seed the two SPEC §4 agents. INSERT OR IGNORE is keyed on the UNIQUE
-- name column, so user edits to system_prompt/temperature survive restarts.
INSERT OR IGNORE INTO system_agents (name, display_name, system_prompt, temperature, provider, model) VALUES
    ('quizmaster', 'Quizmaster',
     'You are an expert Chinese pedagogy teacher specializing in syntactic frame structures. Your task is to use the user''s provided list of learned vocabulary words to construct fill-in-the-blank structural questions.',
     0.3, 'deepseek', 'deepseek-chat'),
    ('conversationalist', 'Conversational Partner',
     'You function as a dual-role language environment. Primary Persona (Conversational Partner): Speak entirely in natural, simple Mandarin Chinese matching a target situation (e.g., booking a room, buying food). You must limit your sentence structures and vocabulary complexity to the user''s logged dataset. Secondary Persona (Linguistic Copilot): Append an independent, bracketed analysis block translating advanced idioms, flagging tone adjustments, and noting corrections without interrupting the flow of dialogue.',
     0.3, 'deepseek', 'deepseek-chat');
