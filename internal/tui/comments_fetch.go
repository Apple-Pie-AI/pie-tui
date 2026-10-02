// Getting review comments in, and getting a fix run out.
//
// Both directions are off the render path: the GitHub read is a tea.Cmd so the
// gh subprocess never blocks the 1Hz loop, and the fix run is a detached
// process that receives its input through the store rather than through
// arguments.
package tui

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// reviewPollInterval is how often the hub refreshes review comments. Far slower
// than the 1Hz store tick: each pass is a gh subprocess per open PR, and review
// comments arrive at human speed.
const reviewPollInterval = 2 * time.Minute

// commentsFreshFor is how long a fetch counts as current, so re-opening the
// screen doesn't hit GitHub every time.
const commentsFreshFor = time.Minute

// commentsFetchedMsg carries the result of a GitHub refresh. resolved is set
// (to merged/closed) when the fetch found the PR no longer open and filed the
// row accordingly - threads don't matter on a resolved PR.
type commentsFetchedMsg struct {
	ticket   string
	n        int
	resolved string
	err      error
	// dequeued is how many queued threads this fetch found resolved on
	// GitHub and dropped from the batch; unparked means that emptied the
	// batch and the ticket left fix-review for review.
	dequeued int
	unparked bool
}

// reviewTickMsg drives the slow background refresh.
type reviewTickMsg time.Time

func reviewTick() tea.Cmd {
	return tea.Tick(reviewPollInterval, func(t time.Time) tea.Msg { return reviewTickMsg(t) })
}

// fetchThreadsCmd reads one PR's review threads and stores them. Captures only
// values (s is a copy), never the model, so the command is safe to run after
// the model has moved on.
//
// The fetch already learns the PR's state on the same GraphQL call, so a PR
// that turned out merged/closed is filed under CLOSED right here instead of
// waiting for a daemon poll that may not be running - "Check for review
// comments" on a closed PR used to shrug and leave the row in review forever.
// The flip is a compare-and-swap from the review state the fetch was keyed on,
// so a run the user started in the meantime is never stomped - and only a won
// swap reclaims the worktree + local branch (dirty worktrees are spared; the
// daemon's sweep retries them and heals any reclaim that fails here).
func fetchThreadsCmd(st *store.Store, s store.Session) tea.Cmd {
	return func() tea.Msg {
		pr, err := review.Fetch(s.Repo, s.PRURL)
		if err != nil {
			return commentsFetchedMsg{ticket: s.Ticket, err: err}
		}
		if pr.State == "MERGED" || pr.State == "CLOSED" {
			next := store.StateClosed
			if pr.State == "MERGED" {
				next = store.StateMerged
			}
			swapped, _ := st.SetStateIf(s.Ticket, store.StateReview, next, s.Retries)
			if swapped {
				if removed, err := git.ReclaimResolved(s.Repo, s.Worktree, s.Branch); err == nil && removed {
					_ = st.ClearWorktree(s.Ticket)
				}
			}
			return commentsFetchedMsg{ticket: s.Ticket, resolved: next}
		}
		// A truncated page is not authoritative about what exists, so the store
		// must not treat the threads it didn't see as deleted.
		if err := st.UpsertThreads(s.Ticket, s.PRURL, pr.Threads, !pr.Truncated); err != nil {
			return commentsFetchedMsg{ticket: s.Ticket, err: err}
		}
		msg := commentsFetchedMsg{ticket: s.Ticket, n: len(pr.Threads)}
		// A thread the reviewer resolved on GitHub while the fix sat at
		// fix-review has nothing left to ship. Drop it from the batch; if
		// that leaves nothing to approve, the park is over (CAS from
		// fix-review only - a run started meanwhile is never stomped).
		msg.dequeued, _ = st.DequeueResolved(s.Ticket)
		if msg.dequeued > 0 && s.State == store.StateFixReview {
			if q, err := st.QueuedComments(s.Ticket); err == nil && len(q) == 0 {
				msg.unparked, _ = st.SetStateIf(s.Ticket, store.StateFixReview, store.StateReview, s.Retries)
			}
		}
		return msg
	}
}

// pollable reports whether a session has an open PR worth asking GitHub about:
// parked at review, or at fix-review - the PR is just as open there, and the
// reviewer may resolve threads while the human decides (ticket 2 sat blocked
// on a resolved thread because only review was polled).
func pollable(s store.Session) bool {
	return (s.State == store.StateReview || s.State == store.StateFixReview) &&
		s.PRURL != "" && s.Repo != ""
}

// fetchComments refreshes one session's review threads. force skips the
// freshness check.
func (m monitorModel) fetchComments(s store.Session, force bool) tea.Cmd {
	if m.demo != nil {
		// The demo's threads are fixtures: there is no GitHub to ask, so the
		// "fetch" succeeds at once with what the store already holds.
		n := s.OpenComments
		return func() tea.Msg { return commentsFetchedMsg{ticket: s.Ticket, n: n} }
	}
	if m.store == nil || !pollable(s) {
		return nil
	}
	if m.comments.fetching {
		return nil // a slow gh must not stack up behind itself
	}
	if !force && time.Since(m.comments.lastFetch) < commentsFreshFor {
		return nil
	}
	return fetchThreadsCmd(m.store, s)
}

// refreshAllComments polls every session with an open PR. This is what makes a
// badge appear without the user opening anything first.
func (m monitorModel) refreshAllComments() tea.Cmd {
	if m.store == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, s := range m.flat {
		if pollable(s) {
			cmds = append(cmds, fetchThreadsCmd(m.store, s))
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// queueAndFix writes the selection to the store and spawns the fix run.
//
// The order matters: the hub and `pie run --address-comments` are separate
// processes with no channel between them but the database, so the queue must be
// written before the run is spawned.
func (m monitorModel) queueAndFix() (tea.Model, tea.Cmd) {
	ids := m.selectedIDs()
	if len(ids) == 0 {
		m.notice = "nothing selected - open a comment and choose \"include this comment\""
		return m, nil
	}
	s := m.sessionByTicket(m.comments.ticket)
	if s == nil {
		m.notice = "no session for " + m.comments.ticket
		return m, nil
	}
	if err := m.store.QueueComments(m.comments.ticket, ids); err != nil {
		m.notice = "queueing comments: " + err.Error()
		return m, nil
	}
	// Remember the batch: once a comment is fixed it is no longer "open", so
	// without this the result of the run would vanish the moment it succeeded.
	m.comments.ran = map[string]bool{}
	for _, id := range ids {
		m.comments.ran[id] = true
	}
	m.comments.startedAt = time.Now()
	m.comments.cursor = 0
	// Back to the dashboard: the fix run is minutes long, and parking the user
	// on a log tail blocks the rest of the fleet. The dashboard files the
	// ticket under RUNNING with this run's log in its detail pane, and the run
	// parks at "fixes ready" (NEEDS YOU) when the preview is ready - the
	// comments screen state above survives, so re-entering lands on the live
	// run, not a reset triage list.
	m.view = viewDashboard
	m.notice = fmt.Sprintf("fixing %s on %s - nothing ships until you approve the result",
		plural(len(ids), "comment"), m.comments.ticket)
	return m, m.spawnRun(runArg(*s), "--address-comments")
}

// openCommentURL opens the focused comment on GitHub - the escape hatch for
// anything this screen renders badly or the agent gets wrong.
func openCommentURL(url string) tea.Cmd {
	return func() tea.Msg {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, url).Start(); err != nil {
			return commentsFetchedMsg{err: err}
		}
		return nil
	}
}
