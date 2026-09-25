// Reply & resolve by hand (issue #16): the human answers a review thread from
// the hub and closes it, exactly what GitHub lets them do with one reply, with
// no agent involved. Offered first on both the triage menu and the approve
// card; this file owns the field, the GitHub round trip and its result.
package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// threadResolvedMsg is the outcome of one hand-written reply. replied and
// resolved are separate so "the reply landed, the resolve did not" reads as
// exactly that; text rides along so a failure can hand it back for a retry.
type threadResolvedMsg struct {
	ticket, id, location, text string
	commentID                  string
	replied, resolved          bool
	wasQueued                  bool // it sat in a parked fix-review batch
	unparked                   bool // the batch emptied: fix-review -> review
	err                        error
}

// replyResolveLabel is the action's name: what it does, which depends on the
// team's review_resolve setting.
func (m monitorModel) replyResolveLabel() string {
	if m.noResolve {
		return "Reply"
	}
	return "Reply & resolve"
}

// updateReply routes keys while the reply field is open on the triage screen.
// Enter posts (an empty reply resolves without a word, like GitHub's Resolve
// button); Esc cancels and posts nothing.
func (m monitorModel) updateReply(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		m.comments.nb.paste(string(msg.Runes))
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.comments.mode = modeBrowsing
		m.comments.nb.clear()
	case tea.KeyEnter:
		c := m.focused()
		text := strings.TrimSpace(m.comments.nb.text())
		m.comments.mode = modeBrowsing
		m.comments.nb.clear()
		if c == nil {
			return m, nil
		}
		return m.postReply(*c, text)
	default:
		m.comments.nb.key(msg)
	}
	return m, nil
}

// postReply spawns the GitHub round trip for one thread, once.
func (m monitorModel) postReply(c store.Comment, text string) (tea.Model, tea.Cmd) {
	if m.comments.posting[c.ID] {
		return m, nil
	}
	s := m.sessionByTicket(m.comments.ticket)
	if m.store == nil || s == nil {
		m.notice = "cannot reply: this ticket has no session on record"
		return m, nil
	}
	m.comments.posting[c.ID] = true
	return m, replyResolveCmd(m.store, *s, c, text, !m.noResolve)
}

// replyResolveCmd posts the reply (when there is text), resolves the thread
// (when resolve is on), records the outcome, and ends a parked fix-review
// whose batch it just emptied. Captures values only - never the model - so
// it is safe to run after the screen has moved on.
func replyResolveCmd(st *store.Store, s store.Session, c store.Comment, text string, resolve bool) tea.Cmd {
	return func() tea.Msg {
		msg := threadResolvedMsg{ticket: s.Ticket, id: c.ID, location: c.Location(), text: text, wasQueued: c.Queued}
		if text != "" {
			id, err := review.Reply(s.Repo, c.ID, text)
			if err != nil {
				msg.err = err
				return msg
			}
			msg.replied, msg.commentID = true, id
		}
		if resolve {
			if err := review.Resolve(s.Repo, c.ID); err != nil {
				msg.err = err
			} else {
				msg.resolved = true
			}
		}
		if !msg.replied && !msg.resolved {
			return msg // nothing happened on GitHub: record nothing
		}
		if err := st.MarkResolvedByHand(c.ID, msg.commentID, msg.resolved); err != nil && msg.err == nil {
			msg.err = err
		}
		if s.State == store.StateFixReview {
			if q, err := st.QueuedComments(s.Ticket); err == nil && len(q) == 0 {
				msg.unparked, _ = st.SetStateIf(s.Ticket, store.StateFixReview, store.StateReview, s.Retries)
			}
		}
		appendTicketLog(s.Ticket, msg.logLine())
		return msg
	}
}

// logLine is the ticket-log record of what the human did.
func (r threadResolvedMsg) logLine() string {
	switch {
	case r.replied && r.resolved:
		return fmt.Sprintf("[%s] you replied on %s and resolved it", r.ticket, r.location)
	case r.replied:
		return fmt.Sprintf("[%s] you replied on %s", r.ticket, r.location)
	default:
		return fmt.Sprintf("[%s] you resolved %s", r.ticket, r.location)
	}
}

// appendTicketLog adds one line to the ticket's log so `pie logs` shows the
// human's action next to the agent's.
func appendTicketLog(ticket, line string) {
	p := paths.LogFor(ticket)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line + "\n")
}

// handleThreadResolved reports the outcome and refreshes what the screen shows.
// A failure hands the typed reply back into the field so Enter retries it.
func (m monitorModel) handleThreadResolved(msg threadResolvedMsg) (tea.Model, tea.Cmd) {
	delete(m.comments.posting, msg.id)
	onScreen := m.view == viewComments && m.comments.ticket == msg.ticket
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, review.ErrNoAuth):
			m.notice = review.ErrNoAuth.Error()
		case msg.replied:
			m.notice = fmt.Sprintf("%s: replied on %s, but resolving failed: %v", msg.ticket, msg.location, msg.err)
		default:
			m.notice = fmt.Sprintf("%s: replying on GitHub: %v", msg.ticket, msg.err)
		}
		if onScreen && !msg.replied {
			// Nothing landed: the reply comes back for a retry.
			if m.phase() == phaseApprove {
				m.comments.cardMode = cardResolve
			} else {
				m.comments.mode = modeReplying
			}
			m.comments.nb.setText(msg.text)
			return m, nil
		}
	} else {
		var b strings.Builder
		fmt.Fprintf(&b, "%s: %s", msg.ticket, strings.TrimPrefix(msg.logLine(), "["+msg.ticket+"] you "))
		if msg.unparked {
			b.WriteString(" - nothing left to approve, back to review")
		}
		if msg.wasQueued {
			b.WriteString(" (the agent's local edits for it are still in the worktree)")
		}
		m.notice = b.String()
	}
	m.reload()
	if onScreen {
		m.loadComments()
		if n := len(m.cardFixes()); m.comments.cardIdx > n {
			m.comments.cardIdx = n // the batch shrank under the cursor
		}
		m.comments.cardSel = 0
		m.renderFocused()
	}
	return m, nil
}
