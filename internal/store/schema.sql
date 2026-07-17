CREATE TABLE IF NOT EXISTS settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS polls (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  slug       TEXT NOT NULL UNIQUE,
  title      TEXT NOT NULL,
  note       TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS options (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  poll_id  INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  day      TEXT NOT NULL,
  start_at TEXT NOT NULL DEFAULT '',
  end_at   TEXT NOT NULL DEFAULT '',
  position INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_options_poll ON options(poll_id, position);

CREATE TABLE IF NOT EXISTS votes (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  poll_id    INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

-- Jedna přezdívka na hlasování. NOCASE, aby "Vojta" a "vojta" byl týž člověk.
CREATE UNIQUE INDEX IF NOT EXISTS idx_votes_poll_name ON votes(poll_id, name COLLATE NOCASE);

CREATE TABLE IF NOT EXISTS choices (
  vote_id   INTEGER NOT NULL REFERENCES votes(id) ON DELETE CASCADE,
  option_id INTEGER NOT NULL REFERENCES options(id) ON DELETE CASCADE,
  value     TEXT NOT NULL CHECK (value IN ('yes', 'maybe', 'no')),
  PRIMARY KEY (vote_id, option_id)
);
