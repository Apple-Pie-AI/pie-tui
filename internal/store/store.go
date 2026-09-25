// Package store is the durable session/ticket state (Decision 2).
//
// A single SQLite file gives us transactional "claim-if-unclaimed" semantics so
// a ticket is never processed twice across concurrent workers, survives daemon
// restarts, and makes `pie status` a simple query.
package store

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite connection.
type Store struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  ticket      TEXT PRIMARY KEY,
  repo        TEXT NOT NULL DEFAULT '',
  state       TEXT NOT NULL,
  retries     INTEGER NOT NULL DEFAULT 0,
  branch      TEXT NOT NULL DEFAULT '',
  worktree    TEXT NOT NULL DEFAULT '',
  pr_url      TEXT NOT NULL DEFAULT '',
  summary     TEXT NOT NULL DEFAULT '',
  session_id  TEXT NOT NULL DEFAULT '',
  pid         INTEGER NOT NULL DEFAULT 0,
  source_path TEXT NOT NULL DEFAULT '',
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS emulators (
  avd_name     TEXT PRIMARY KEY,
  state        TEXT NOT NULL DEFAULT 'idle',
  holder       TEXT NOT NULL DEFAULT '',
  pid          INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  updated_at   INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS approvals (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  ticket       TEXT NOT NULL,
  tool         TEXT NOT NULL,
  command      TEXT NOT NULL DEFAULT '',
  state        TEXT NOT NULL DEFAULT 'pending',
  remember     INTEGER NOT NULL DEFAULT 0,
  requested_at INTEGER NOT NULL,
  decided_at   INTEGER NOT NULL DEFAULT 0
);`

// Open opens (and migrates) the state DB at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// CREATE TABLE IF NOT EXISTS is the whole migration for a DB that predates
	// review comments. Columns added to pr_comments *after* it first shipped
	// still need an ALTER below, same as any other table.
	if _, err := db.Exec(commentsSchema); err != nil {
		db.Close()
		return nil, err
	}
	// Migrate existing DBs (ignore "duplicate column" errors on re-run).
	db.Exec(`ALTER TABLE sessions ADD COLUMN session_id TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE sessions ADD COLUMN pid INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE sessions ADD COLUMN source_path TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE sessions ADD COLUMN base_branch TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE pr_comments ADD COLUMN draft_reply TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE pr_comments ADD COLUMN advisory INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN short_desc TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN review_plan INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN denials TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN denial_fixable INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN parked_flow TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_fingerprint TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_round INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_round_tree TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_error TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_notes TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_transcript TEXT NOT NULL DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE sessions ADD COLUMN change_live TEXT NOT NULL DEFAULT ''`)
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }
