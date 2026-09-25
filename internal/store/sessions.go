// Session rows: one per ticket, read and written concurrently by `pie run`, the
// daemon and the TUI. Every mutation stamps updated_at, which the dashboard uses
// as a liveness signal.
package store

import (
	"database/sql"
	"time"
)

// Session is one ticket's row.
type Session struct {
	Ticket     string
	Repo       string
	State      string
	Retries    int
	Branch     string
	Worktree   string
	PRURL      string
	Summary    string
	SessionID  string // Claude session ID for cross-stage resume
	PID        int    // OS pid of the process driving this session (0 = unknown)
	SourcePath string // .md file path for local tickets; empty for Jira tickets
	BaseBranch string // stacked-diff base branch name (bare, no origin/ prefix); "" = default
	ShortDesc  string // LLM-generated <10-word gist shown in the dashboard third column
	ReviewPlan bool   // pause after the plan stage so the human can approve/give feedback
	// Denials is the JSON list of tool calls the permission system refused in
	// the last run ("" = none). Opaque to the store: the runner marshals it
	// (agent.MarshalDenials) and the TUI unmarshals it, so this package stays
	// free of an agent dependency.
	Denials string
	// DenialFixable is the runner's verdict, computed at park time with the
	// real allowlist, on whether "Allow denied commands & re-run" would change
	// anything. Persisted so the why-pane and the actions menu read the same
	// answer instead of re-deriving it with different inputs (the pane used an
	// empty allowlist to avoid config I/O per render, and contradicted the menu).
	DenialFixable bool
	// ParkedFlow records which flow parked this ticket at needs-you
	// ("ship-comments", "address-comments", or "" for the main pipeline), so a
	// retry re-enters the right flow - and the ship-comments gate can tell a
	// parked retry from a ship nobody approved.
	ParkedFlow string
	// The change-review gate's bookkeeping (review-before-PR).
	// ChangeFingerprint is the worktree content fingerprint recorded at park:
	// head sha + a hash of the diff and untracked set. The approve run
	// re-verifies only when the current fingerprint differs (hand-edits,
	// reworks, reverts).
	ChangeFingerprint string
	// ChangeRound counts feedback rounds (1 = first park); ChangeRoundTree is
	// the tree sha snapshotted when the last feedback was dispatched, so the
	// screen can show "changes since your feedback".
	ChangeRound     int
	ChangeRoundTree string
	// ChangeError is why the last approve attempt failed ("" = none); the
	// change screen shows it in its header.
	ChangeError string
	// ChangeNotes is the change screen's pending feedback draft, plain text.
	// Persisted on every keystroke so leaving the screen never loses it; the
	// rework run reads it and the screen clears it once sent.
	ChangeNotes string
	// ChangeTranscript is the change screen's Plan-mode conversation so far:
	// plain text, newline-delimited "you: …" / "claude: …" turns, appended to
	// as a discuss round streams its reply. Deliberately not JSON/structured -
	// this is a transcript to read, not an event feed to replay.
	ChangeTranscript string
	// ChangeLive is the block currently being streamed - the growing text of
	// a reply still in progress, or "thinking: "+growing reasoning while the
	// agent is still working something out - overwritten wholesale on every
	// flush, never appended to. Cleared back to "" once its content lands in
	// ChangeTranscript as a committed line. Separate from ChangeTranscript so
	// the committed transcript's append-only contract never has to represent
	// "this line might still change".
	ChangeLive string

	// OpenComments counts the PR review threads still wanting attention. It is
	// derived from pr_comments on every read, deliberately not a column and
	// deliberately not a lifecycle state: `state` has exactly one writer (the
	// runner pipeline, via Hooks.OnState), and a poller writing into it would
	// race a live run - dropping the row out of IsActive mid-pipeline, which is
	// what the concurrency guard in `pie run` relies on to refuse a second
	// driver for the same worktree. Keeping the count separate also leaves the
	// daemon's merge detection (which only touches idle rows, via SetStateIf's
	// compare-and-swap) intact.
	OpenComments int

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Claim atomically inserts a queued row for ticket. It returns true if THIS call
// won the claim, false if the ticket was already present (claimed/processed).
func (s *Store) Claim(ticket, repo, summary string) (bool, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(
		`INSERT INTO sessions (ticket, repo, state, summary, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(ticket) DO NOTHING`,
		ticket, repo, StateQueued, summary, now, now,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// SetState updates the lifecycle state and retry count.
func (s *Store) SetState(ticket, state string, retries int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET state=?, retries=?, updated_at=? WHERE ticket=?`,
		state, retries, time.Now().Unix(), ticket,
	)
	return err
}

// SetStateIf is SetState as a compare-and-swap: the row is updated only if it
// is still in the state the caller read. This is what lets the daemon flip an
// idle row (a merged PR's ticket, say) without racing the one-writer rule
// above - a user can re-run the ticket between the daemon's List and its
// write, and an unconditional SetState would stomp the live pipeline's state.
// Returns whether the swap happened.
func (s *Store) SetStateIf(ticket, from, to string, retries int) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE sessions SET state=?, retries=?, updated_at=? WHERE ticket=? AND state=?`,
		to, retries, time.Now().Unix(), ticket, from,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// ClearWorktree records that a row's worktree and local branch are gone
// (reclaimed after its PR resolved). It is what keeps the daemon's CLOSED
// sweep idempotent - a cleared row gives the next poll nothing to redo. The
// PR URL survives: the CLOSED row still links to its pull request.
func (s *Store) ClearWorktree(ticket string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET worktree='', branch='', updated_at=? WHERE ticket=?`,
		time.Now().Unix(), ticket,
	)
	return err
}

// SetSessionID persists the Claude session ID for cross-stage resume.
func (s *Store) SetSessionID(ticket, sessionID string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET session_id=?, updated_at=? WHERE ticket=?`,
		sessionID, time.Now().Unix(), ticket,
	)
	return err
}

// SetSourcePath records the .md file path for local (non-Jira) tickets so that
// TUI re-launch actions (Resume, Run again) can reconstruct the right command
// without hitting the Jira API.
func (s *Store) SetSourcePath(ticket, path string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET source_path=?, updated_at=? WHERE ticket=?`,
		path, time.Now().Unix(), ticket,
	)
	return err
}

// SetBaseBranch records the (bare) base branch for a stacked-diff ticket.
func (s *Store) SetBaseBranch(ticket, base string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET base_branch=?, updated_at=? WHERE ticket=?`,
		base, time.Now().Unix(), ticket,
	)
	return err
}

// SetPID records the OS pid of the process driving this session, so the monitor
// can tell a live agent from a dead one (deterministic liveness via kill -0).
func (s *Store) SetPID(ticket string, pid int) error {
	_, err := s.db.Exec(`UPDATE sessions SET pid=? WHERE ticket=?`, pid, ticket)
	return err
}

// SetFields updates branch/worktree/pr_url (any empty string is left unchanged).
func (s *Store) SetFields(ticket, branch, worktree, prURL string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET
		   branch   = CASE WHEN ?='' THEN branch   ELSE ? END,
		   worktree = CASE WHEN ?='' THEN worktree ELSE ? END,
		   pr_url   = CASE WHEN ?='' THEN pr_url   ELSE ? END,
		   updated_at = ?
		 WHERE ticket=?`,
		branch, branch, worktree, worktree, prURL, prURL, time.Now().Unix(), ticket,
	)
	return err
}

// SetShortDesc persists a LLM-generated short description for the dashboard.
// Called at run start (and again when the async LLM upgrade finishes), so stale
// rows from prior failed runs are always refreshed.
func (s *Store) SetShortDesc(ticket, desc string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET short_desc=?, updated_at=? WHERE ticket=?`,
		desc, time.Now().Unix(), ticket,
	)
	return err
}

// SetReviewPlan records whether this ticket should pause after the plan stage.
// Persisted so TUI re-spawns (answer/feedback) keep the gate on.
func (s *Store) SetReviewPlan(ticket string, v bool) error {
	iv := 0
	if v {
		iv = 1
	}
	_, err := s.db.Exec(
		`UPDATE sessions SET review_plan=?, updated_at=? WHERE ticket=?`,
		iv, time.Now().Unix(), ticket,
	)
	return err
}

// SetDenials records (or with "" clears) the permission denials of the last
// run, as opaque JSON produced by the runner, along with the runner's verdict
// on whether an allowlist change would fix them (always false when clearing).
func (s *Store) SetDenials(ticket, denialsJSON string, fixable bool) error {
	fv := 0
	if fixable && denialsJSON != "" {
		fv = 1
	}
	_, err := s.db.Exec(
		`UPDATE sessions SET denials=?, denial_fixable=?, updated_at=? WHERE ticket=?`,
		denialsJSON, fv, time.Now().Unix(), ticket,
	)
	return err
}

// SetParkedFlow records (or with "" clears) which flow parked this ticket.
func (s *Store) SetParkedFlow(ticket, flow string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET parked_flow=?, updated_at=? WHERE ticket=?`,
		flow, time.Now().Unix(), ticket,
	)
	return err
}

// SetChangeParked records the change-review park: the content fingerprint,
// the round number, and clears any stale approve error.
func (s *Store) SetChangeParked(ticket, fingerprint string, round int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET change_fingerprint=?, change_round=?, change_error='', updated_at=? WHERE ticket=?`,
		fingerprint, round, time.Now().Unix(), ticket,
	)
	return err
}

// SetChangeRoundTree records the worktree tree snapshot taken when feedback
// was dispatched, the baseline for "changes since your feedback".
func (s *Store) SetChangeRoundTree(ticket, tree string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET change_round_tree=?, updated_at=? WHERE ticket=?`,
		tree, time.Now().Unix(), ticket,
	)
	return err
}

// SetChangeError records (or with "" clears) why the last approve failed.
func (s *Store) SetChangeError(ticket, msg string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET change_error=?, updated_at=? WHERE ticket=?`,
		msg, time.Now().Unix(), ticket,
	)
	return err
}

// SetChangeNotes persists the change screen's pending notes/reverts JSON.
func (s *Store) SetChangeNotes(ticket, notes string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET change_notes=?, updated_at=? WHERE ticket=?`,
		notes, time.Now().Unix(), ticket,
	)
	return err
}

// SetChangeTranscript overwrites the change screen's Plan-mode conversation
// (used to clear it, or to fully replace it - AppendChangeTranscript is what
// a discuss round calls turn by turn).
func (s *Store) SetChangeTranscript(ticket, transcript string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET change_transcript=?, updated_at=? WHERE ticket=?`,
		transcript, time.Now().Unix(), ticket,
	)
	return err
}

// SetChangeLive overwrites the block currently streaming in (see
// Session.ChangeLive) - the runner calls this on a throttle, not per delta,
// to bound write frequency regardless of how chatty the source deltas are.
func (s *Store) SetChangeLive(ticket, text string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET change_live=?, updated_at=? WHERE ticket=?`,
		text, time.Now().Unix(), ticket,
	)
	return err
}

// AppendChangeTranscript adds one line to the change screen's running
// conversation - read-modify-write rather than SQL string concatenation, so
// callers pass a plain line ("you: …" / "claude: …") and never think about
// the existing content's shape.
func (s *Store) AppendChangeTranscript(ticket, line string) error {
	sess, err := s.Get(ticket)
	if err != nil {
		return err
	}
	if sess == nil {
		return nil
	}
	next := line
	if sess.ChangeTranscript != "" {
		next = sess.ChangeTranscript + "\n" + line
	}
	return s.SetChangeTranscript(ticket, next)
}

// sessionCols is the shared SELECT list. open_comments is a correlated
// subquery rather than a stored count so it can never drift from the rows it
// summarizes - every write to pr_comments changes it for free.
const sessionCols = `ticket, repo, state, retries, branch, worktree, pr_url, summary, session_id,
	pid, source_path, created_at, updated_at, base_branch, short_desc, review_plan, denials,
	denial_fixable, parked_flow, change_fingerprint, change_round, change_round_tree,
	change_error, change_notes, change_transcript, change_live,
	(SELECT COUNT(*) FROM pr_comments c
	   WHERE c.ticket = sessions.ticket
	     AND c.resolved = 0 AND c.skipped = 0 AND c.addressed_at = 0 AND c.advisory = 0)`

// ByBranch returns the newest session working this branch, or nil. The
// checkout flow asks before minting a session: a branch that already has one
// (from the run that opened its PR) must be adopted, not twinned - pr_comments
// rows are owned by whichever ticket fetched them first, and twin sessions
// split one PR's threads into two partial counts.
func (s *Store) ByBranch(branch string) (*Session, error) {
	if branch == "" {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE branch=? ORDER BY created_at DESC LIMIT 1`, branch)
	sess, err := scan(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sess, err
}

// Get returns one session.
func (s *Store) Get(ticket string) (*Session, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE ticket=?`, ticket)
	return scan(row)
}

// List returns all sessions, most recently updated first.
func (s *Store) List() ([]Session, error) {
	rows, err := s.db.Query(`SELECT ` + sessionCols + ` FROM sessions ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scan(r scanner) (*Session, error) {
	var s Session
	var created, updated int64
	var reviewPlan, denialFixable int
	if err := r.Scan(&s.Ticket, &s.Repo, &s.State, &s.Retries, &s.Branch,
		&s.Worktree, &s.PRURL, &s.Summary, &s.SessionID, &s.PID, &s.SourcePath,
		&created, &updated, &s.BaseBranch, &s.ShortDesc, &reviewPlan, &s.Denials,
		&denialFixable, &s.ParkedFlow, &s.ChangeFingerprint, &s.ChangeRound,
		&s.ChangeRoundTree, &s.ChangeError, &s.ChangeNotes, &s.ChangeTranscript, &s.ChangeLive, &s.OpenComments); err != nil {
		return nil, err
	}
	s.ReviewPlan = reviewPlan != 0
	s.DenialFixable = denialFixable != 0
	s.CreatedAt = time.Unix(created, 0)
	s.UpdatedAt = time.Unix(updated, 0)
	return &s, nil
}
