// PR review threads: what the reviewers asked for, and what we've done about
// it. One row per thread (GitHub's own unit of review), keyed by the GraphQL
// node id so re-polling is idempotent.
//
// This table is also the IPC channel between the hub and the runner. They are
// separate OS processes - the TUI spawns `pie run --address-comments` detached
// and cannot hand it a Go value - so the selection the user makes is written
// here as `queued`, and the runner reads it back.
package store

import (
	"strconv"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
)

const commentsSchema = `
CREATE TABLE IF NOT EXISTS pr_comments (
  id            TEXT PRIMARY KEY,
  ticket        TEXT NOT NULL,
  pr_url        TEXT NOT NULL DEFAULT '',
  kind          TEXT NOT NULL DEFAULT 'thread',
  author        TEXT NOT NULL DEFAULT '',
  is_bot        INTEGER NOT NULL DEFAULT 0,
  path          TEXT NOT NULL DEFAULT '',
  line          INTEGER NOT NULL DEFAULT 0,
  diff_hunk     TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL DEFAULT '',
  url           TEXT NOT NULL DEFAULT '',
  outdated      INTEGER NOT NULL DEFAULT 0,
  resolved      INTEGER NOT NULL DEFAULT 0,
  comment_count INTEGER NOT NULL DEFAULT 1,
  last_comment  TEXT NOT NULL DEFAULT '',
  user_note     TEXT NOT NULL DEFAULT '',
  agent_note    TEXT NOT NULL DEFAULT '',
  draft_reply   TEXT NOT NULL DEFAULT '',
  queued        INTEGER NOT NULL DEFAULT 0,
  skipped       INTEGER NOT NULL DEFAULT 0,
  addressed_at  INTEGER NOT NULL DEFAULT 0,
  addressed_sha TEXT NOT NULL DEFAULT '',
  replied       INTEGER NOT NULL DEFAULT 0,
  advisory      INTEGER NOT NULL DEFAULT 0,
  first_seen_at INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pr_comments_ticket ON pr_comments(ticket);`

// Comment is one stored review thread.
type Comment struct {
	ID     string
	Ticket string
	PRURL  string
	Kind   string // review.KindThread | review.KindReview
	// Advisory: a review the reviewer posted as "Comment", not "Request
	// changes". Listed and fixable like anything else, but never counted as
	// wanting attention - GitHub has no way to resolve a review body, so a
	// counted one would hold the ticket in NEEDS YOU forever.
	Advisory bool
	Author   string
	Bot      bool
	Path     string
	Line     int
	DiffHunk string
	Body     string
	URL      string
	Outdated bool
	Resolved bool // resolved on GitHub

	CommentCount  int
	LastCommentID string

	UserNote  string // the human's extra guidance, folded into the fix prompt
	AgentNote string // why the agent skipped it, when it did
	// DraftReply is what will be posted on the thread when the fix ships: the
	// agent drafts it during the local fix, the human may rewrite it in the
	// approve screen, and the ship run reads whatever is here at that moment.
	DraftReply string
	Queued     bool // selected for the next fix run
	Skipped    bool // dismissed by the human; stays hidden until a new reply

	AddressedAt  time.Time // zero = still open
	AddressedSHA string
	Replied      bool

	FirstSeenAt time.Time
	UpdatedAt   time.Time
}

// Open reports whether the thread still wants attention.
func (c Comment) Open() bool {
	return !c.Resolved && !c.Skipped && c.AddressedAt.IsZero()
}

// Location renders the thread's anchor: "path:line", "path" when the line is
// unknown, or "—" for a review summary that has no file.
func (c Comment) Location() string {
	if c.Path == "" {
		return "—"
	}
	if c.Line <= 0 {
		return c.Path
	}
	return c.Path + ":" + strconv.Itoa(c.Line)
}

// Gist is the first non-empty line of the body, for a one-line list row.
func (c Comment) Gist() string {
	for _, ln := range strings.Split(c.Body, "\n") {
		if s := strings.TrimSpace(strings.TrimLeft(ln, ">#*- ")); s != "" {
			return s
		}
	}
	return ""
}

// commentCols is the shared SELECT list, so the scan below can't drift from it.
const commentCols = `id, ticket, pr_url, kind, author, is_bot, path, line, diff_hunk, body, url,
	outdated, resolved, comment_count, last_comment, user_note, agent_note, draft_reply, queued, skipped,
	addressed_at, addressed_sha, replied, advisory, first_seen_at, updated_at`

// UpsertThreads records a fetch. complete must be false when the fetch was
// truncated: threads missing from a partial page are on the next one, not gone,
// and deleting them would erase the user's decisions about them.
func (s *Store) UpsertThreads(ticket, prURL string, ths []review.Thread, complete bool) error {
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, th := range ths {
		// The CASE arms are the whole memory of the feature. A poll must not
		// undo what the human (or a finished run) decided - but a NEW comment in
		// the thread means the reviewer came back, so the thread reopens. That
		// pivot is last_comment: unchanged means nothing happened since we
		// looked, changed means there is something new to read.
		//
		// user_note and agent_note are never touched by a poll at all.
		if _, err := tx.Exec(`
			INSERT INTO pr_comments
			  (id, ticket, pr_url, kind, author, is_bot, path, line, diff_hunk, body, url,
			   outdated, resolved, comment_count, last_comment, advisory, first_seen_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET
			  advisory      = excluded.advisory,
			  body          = excluded.body,
			  diff_hunk     = excluded.diff_hunk,
			  path          = excluded.path,
			  line          = excluded.line,
			  url           = excluded.url,
			  outdated      = excluded.outdated,
			  resolved      = excluded.resolved,
			  comment_count = excluded.comment_count,
			  last_comment  = excluded.last_comment,
			  updated_at    = excluded.updated_at,
			  addressed_at  = CASE WHEN pr_comments.last_comment = excluded.last_comment
			                       THEN pr_comments.addressed_at ELSE 0 END,
			  addressed_sha = CASE WHEN pr_comments.last_comment = excluded.last_comment
			                       THEN pr_comments.addressed_sha ELSE '' END,
			  replied       = CASE WHEN pr_comments.last_comment = excluded.last_comment
			                       THEN pr_comments.replied ELSE 0 END,
			  skipped       = CASE WHEN pr_comments.last_comment = excluded.last_comment
			                       THEN pr_comments.skipped ELSE 0 END`,
			th.ID, ticket, prURL, th.Kind, th.Author, boolInt(th.Bot), th.Path, th.Line,
			th.DiffHunk, th.Body, th.URL, boolInt(th.Outdated), boolInt(th.Resolved),
			th.CommentCount, th.LastCommentID, boolInt(th.Advisory), now, now,
		); err != nil {
			return err
		}
	}

	if complete {
		// Every thread vanished (all deleted on GitHub). This needs its own arm:
		// `id NOT IN (NULL)` is NULL, not true, so the generic form below would
		// silently match nothing and leave the stale rows behind.
		if len(ths) == 0 {
			if _, err := tx.Exec(`DELETE FROM pr_comments WHERE ticket=?`, ticket); err != nil {
				return err
			}
			return tx.Commit()
		}
		args := []any{ticket}
		for _, th := range ths {
			args = append(args, th.ID)
		}
		q := `DELETE FROM pr_comments WHERE ticket=? AND id NOT IN (` + placeholders(len(ths)) + `)`
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Comments returns every stored thread for a ticket, oldest first so a review
// reads in the order it was written.
func (s *Store) Comments(ticket string) ([]Comment, error) {
	return s.queryComments(`SELECT `+commentCols+`
		FROM pr_comments WHERE ticket=? ORDER BY first_seen_at, kind DESC, path, line`, ticket)
}

// OpenComments returns the threads still wanting attention.
func (s *Store) OpenComments(ticket string) ([]Comment, error) {
	return s.queryComments(`SELECT `+commentCols+`
		FROM pr_comments
		WHERE ticket=? AND resolved=0 AND skipped=0 AND addressed_at=0 AND advisory=0
		ORDER BY first_seen_at, kind DESC, path, line`, ticket)
}

// QueuedComments returns what the user selected for the next fix run. This is
// the runner's side of the IPC channel.
func (s *Store) QueuedComments(ticket string) ([]Comment, error) {
	return s.queryComments(`SELECT `+commentCols+`
		FROM pr_comments WHERE ticket=? AND queued=1
		ORDER BY first_seen_at, kind DESC, path, line`, ticket)
}

// QueueComments marks exactly ids as queued for ticket and clears the rest, so
// a second selection never inherits the first one's leftovers.
func (s *Store) QueueComments(ticket string, ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE pr_comments SET queued=0 WHERE ticket=?`, ticket); err != nil {
		return err
	}
	if len(ids) > 0 {
		args := []any{ticket}
		for _, id := range ids {
			args = append(args, id)
		}
		if _, err := tx.Exec(
			`UPDATE pr_comments SET queued=1 WHERE ticket=? AND id IN (`+placeholders(len(ids))+`)`,
			args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearQueued drops the whole selection, called once a run has consumed it.
func (s *Store) ClearQueued(ticket string) error {
	_, err := s.db.Exec(`UPDATE pr_comments SET queued=0 WHERE ticket=?`, ticket)
	return err
}

// DequeueResolved drops from the batch every queued thread GitHub now reports
// resolved, and returns how many. A poll records the resolution but must not
// touch a human's selection in general; this is the one case where the
// selection is moot - a resolved thread has nothing left to fix or answer,
// and leaving it queued held ticket 2 at fix-review demanding a decision on
// a thread the reviewer had already closed.
func (s *Store) DequeueResolved(ticket string) (int, error) {
	res, err := s.db.Exec(`UPDATE pr_comments SET queued=0, updated_at=?
		WHERE ticket=? AND queued=1 AND resolved=1`, time.Now().Unix(), ticket)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SetCommentQueued includes or excludes one thread from the currently parked
// approval batch. It does not mark the thread skipped: an excluded comment
// stays visible and can be selected again in a later pass.
func (s *Store) SetCommentQueued(id string, queued bool) error {
	_, err := s.db.Exec(`UPDATE pr_comments SET queued=?, updated_at=? WHERE id=?`,
		queued, time.Now().Unix(), id)
	return err
}

// MarkAddressed records that a fix for these threads landed in sha. Only the
// threads the agent actually reported fixing should be passed - marking a
// skipped one addressed hides a request nobody answered.
func (s *Store) MarkAddressed(ids []string, sha string) error {
	if len(ids) == 0 {
		return nil
	}
	args := []any{time.Now().Unix(), sha, time.Now().Unix()}
	for _, id := range ids {
		args = append(args, id)
	}
	_, err := s.db.Exec(
		`UPDATE pr_comments SET addressed_at=?, addressed_sha=?, queued=0, updated_at=?
		 WHERE id IN (`+placeholders(len(ids))+`)`, args...)
	return err
}

// MarkReplied records that we posted our reply on these threads, so a retry
// can't double-post - and advances each thread's last_comment to our own reply.
//
// The advance is what keeps an addressed thread addressed: the poll's reopen
// pivot is "the last comment changed", and without this our own reply trips it
// on the next fetch, resetting addressed_at and dragging the ticket back to
// NEEDS YOU within two minutes of the fix landing. An empty lastComment (the
// reply happened but its id could not be read) records the reply and leaves the
// pivot alone - reopening is the safe failure, silently staying closed on a
// reviewer's real follow-up is not.
func (s *Store) MarkReplied(replies map[string]string) error {
	if len(replies) == 0 {
		return nil
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for id, lastComment := range replies {
		if lastComment != "" {
			_, err = tx.Exec(`UPDATE pr_comments SET replied=1, last_comment=?, updated_at=? WHERE id=?`,
				lastComment, now, id)
		} else {
			_, err = tx.Exec(`UPDATE pr_comments SET replied=1, updated_at=? WHERE id=?`, now, id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkResolvedByHand records that the human answered a thread from the hub
// themselves (issue #16): replied, resolved when the resolve went through,
// out of any batch it was queued in, and un-skipped. lastComment is our
// reply's id when known - the pivot that keeps the next poll from reading our
// own reply as the reviewer coming back; empty (resolve-only, or an id gh did
// not return) leaves the pivot alone, so reopening is the safe failure.
func (s *Store) MarkResolvedByHand(id, lastComment string, resolved bool) error {
	now := time.Now().Unix()
	if lastComment != "" {
		_, err := s.db.Exec(`UPDATE pr_comments SET replied=1, resolved=?, queued=0, skipped=0,
			last_comment=?, updated_at=? WHERE id=?`, boolInt(resolved), lastComment, now, id)
		return err
	}
	_, err := s.db.Exec(`UPDATE pr_comments SET replied=1, resolved=?, queued=0, skipped=0,
		updated_at=? WHERE id=?`, boolInt(resolved), now, id)
	return err
}

// SetCommentNote stores the human's extra guidance for one thread.
func (s *Store) SetCommentNote(id, note string) error {
	_, err := s.db.Exec(`UPDATE pr_comments SET user_note=?, updated_at=? WHERE id=?`,
		strings.TrimSpace(note), time.Now().Unix(), id)
	return err
}

// SetCommentAgentNote stores why the agent declined to act on a thread.
func (s *Store) SetCommentAgentNote(id, note string) error {
	_, err := s.db.Exec(`UPDATE pr_comments SET agent_note=?, updated_at=? WHERE id=?`,
		strings.TrimSpace(note), time.Now().Unix(), id)
	return err
}

// SetDraftReply stores the reply that will be posted on a thread when the fix
// ships. The agent writes the first draft; the human's edit overwrites it, and
// whoever wrote last wins - the ship run just reads the column.
func (s *Store) SetDraftReply(id, reply string) error {
	_, err := s.db.Exec(`UPDATE pr_comments SET draft_reply=?, updated_at=? WHERE id=?`,
		strings.TrimSpace(reply), time.Now().Unix(), id)
	return err
}

// SetCommentSkipped dismisses (or un-dismisses) a thread. A skipped thread
// stays hidden until the reviewer replies to it again.
func (s *Store) SetCommentSkipped(id string, v bool) error {
	_, err := s.db.Exec(`UPDATE pr_comments SET skipped=?, queued=0, updated_at=? WHERE id=?`,
		boolInt(v), time.Now().Unix(), id)
	return err
}

func (s *Store) queryComments(q string, args ...any) ([]Comment, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Comment
	for rows.Next() {
		var c Comment
		var bot, outdated, resolved, queued, skipped, replied, advisory int
		var addressed, firstSeen, updated int64
		if err := rows.Scan(&c.ID, &c.Ticket, &c.PRURL, &c.Kind, &c.Author, &bot, &c.Path,
			&c.Line, &c.DiffHunk, &c.Body, &c.URL, &outdated, &resolved, &c.CommentCount,
			&c.LastCommentID, &c.UserNote, &c.AgentNote, &c.DraftReply, &queued, &skipped, &addressed,
			&c.AddressedSHA, &replied, &advisory, &firstSeen, &updated); err != nil {
			return nil, err
		}
		c.Bot, c.Outdated, c.Resolved = bot != 0, outdated != 0, resolved != 0
		c.Queued, c.Skipped, c.Replied = queued != 0, skipped != 0, replied != 0
		c.Advisory = advisory != 0
		if addressed != 0 {
			c.AddressedAt = time.Unix(addressed, 0)
		}
		c.FirstSeenAt, c.UpdatedAt = time.Unix(firstSeen, 0), time.Unix(updated, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

// placeholders returns "?,?,?" for n. n==0 yields NULL so that `IN ()`, which
// is a syntax error, becomes `IN (NULL)` - falsy, i.e. matches nothing, which
// is what an empty id list means. Note this trick is only sound for IN: `NOT IN
// (NULL)` is NULL rather than true, so callers that negate must special-case an
// empty list instead (see UpsertThreads).
func placeholders(n int) string {
	if n == 0 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
