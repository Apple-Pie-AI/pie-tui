// Run app jobs. A run is its own detached `pie run-app` process (it outlives
// the TUI); this row is how the dashboard follows it: the current step, the
// outcome, and the pid that tells a live run from an interrupted one.
package store

import (
	"database/sql"
	"errors"
)

const appRunsSchema = `
CREATE TABLE IF NOT EXISTS app_runs (
  id         TEXT PRIMARY KEY,
  target     TEXT NOT NULL,              -- "main" or a ticket id
  dir        TEXT NOT NULL,
  module     TEXT NOT NULL DEFAULT '',
  variant    TEXT NOT NULL DEFAULT '',
  device     TEXT NOT NULL DEFAULT '',
  step       TEXT NOT NULL DEFAULT 'prepare',
  state      TEXT NOT NULL DEFAULT 'running', -- running | done | failed | stopped
  detail     TEXT NOT NULL DEFAULT '',
  repaired   INTEGER NOT NULL DEFAULT 0,
  pid        INTEGER NOT NULL DEFAULT 0,
  log_path   TEXT NOT NULL DEFAULT '',
  started_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);`

// App run states.
const (
	AppRunRunning = "running"
	AppRunDone    = "done"
	AppRunFailed  = "failed"
	AppRunStopped = "stopped"
)

// AppRun is one row.
type AppRun struct {
	ID, Target, Dir, Module, Variant, Device, Step, State, Detail, LogPath string
	Repaired                                                               bool
	PID                                                                    int
	StartedAt, UpdatedAt                                                   int64
}

// ErrAppRunBusy: a live run is already building that checkout.
var ErrAppRunBusy = errors.New("a Run app is already running for that code")

// CreateAppRun records a new run - refused while another live run builds the
// same directory (two Gradle builds in one checkout collide).
func (s *Store) CreateAppRun(r AppRun, isAlive func(int) bool) error {
	var other AppRun
	err := s.db.QueryRow(`SELECT id, pid FROM app_runs WHERE dir = ? AND state = ? ORDER BY started_at DESC LIMIT 1`,
		r.Dir, AppRunRunning).Scan(&other.ID, &other.PID)
	if err == nil && other.ID != r.ID && isAlive(other.PID) {
		return ErrAppRunBusy
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	t := now()
	_, err = s.db.Exec(`INSERT OR REPLACE INTO app_runs (id, target, dir, module, variant, device, step, state, pid,
		log_path, started_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.ID, r.Target, r.Dir, r.Module, r.Variant, r.Device, "prepare", AppRunRunning, r.PID, r.LogPath, t, t)
	return err
}

// SetAppRunStep records progress.
func (s *Store) SetAppRunStep(id, step, detail string) error {
	_, err := s.db.Exec(`UPDATE app_runs SET step = ?, detail = ?, updated_at = ? WHERE id = ? AND state = ?`,
		step, detail, now(), id, AppRunRunning)
	return err
}

// SetAppRunChoice records what the run resolved to build.
func (s *Store) SetAppRunChoice(id, module, variant, device string) error {
	_, err := s.db.Exec(`UPDATE app_runs SET module = ?, variant = ?, device = ? WHERE id = ?`, module, variant, device, id)
	return err
}

// FinishAppRun records the outcome.
func (s *Store) FinishAppRun(id, state, detail string, repaired bool) error {
	_, err := s.db.Exec(`UPDATE app_runs SET state = ?, detail = ?, repaired = ?, updated_at = ? WHERE id = ?`,
		state, detail, boolToInt(repaired), now(), id)
	return err
}

// GetAppRun reads one run.
func (s *Store) GetAppRun(id string) (AppRun, error) {
	return s.scanAppRun(s.db.QueryRow(appRunCols+` WHERE id = ?`, id))
}

// LatestAppRun is the most recent run (sql.ErrNoRows when none).
func (s *Store) LatestAppRun() (AppRun, error) {
	return s.scanAppRun(s.db.QueryRow(appRunCols + ` ORDER BY started_at DESC, rowid DESC LIMIT 1`))
}

const appRunCols = `SELECT id, target, dir, module, variant, device, step, state, detail, repaired, pid, log_path,
	started_at, updated_at FROM app_runs`

func (s *Store) scanAppRun(row *sql.Row) (AppRun, error) {
	var r AppRun
	var rep int
	err := row.Scan(&r.ID, &r.Target, &r.Dir, &r.Module, &r.Variant, &r.Device, &r.Step, &r.State, &r.Detail,
		&rep, &r.PID, &r.LogPath, &r.StartedAt, &r.UpdatedAt)
	r.Repaired = rep != 0
	return r, err
}
