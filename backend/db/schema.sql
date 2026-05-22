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
