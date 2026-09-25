// The approval screen, as cards: one fix per screen - the comment, what
// changed, the reply that will post - and four actions in one ↑↓ list. Enter
// decides the card and jumps to the next undecided fix; ←→ browse without
// deciding; after the last decision, the ship card, which is the only place
// anything leaves the machine.
//
// This replaced a screen with five competing surfaces: a fix list, a detail
// pane, a horizontal action rail, a four-verb button and three status readouts
// all saying the same thing. The rail made enter mean different things
// depending on an invisible left-right position; the button bundled a cheap
// local decision with an irreversible remote one. Cards undo both.
package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Card decisions. "" is undecided.
const (
	cardOK   = "ok"
	cardSkip = "skip"
	cardRedo = "redo" // sent back to the agent
)

// Card modes: which input owns the keyboard.
const (
	cardView = iota
	cardReply
	cardAsk
	cardDiff    // the full-screen diff viewer
	cardResolve // typing a reply to post and resolve by hand (issue #16)
)

// cardFixes is the batch under approval: every queued comment, agent-declined
// ones first (they need a decision before anything else is worth reading),
// then file order so the mental model matches the review screen.
func (m monitorModel) cardFixes() []store.Comment {
	var out []store.Comment
	for _, c := range m.comments.rows {
		if c.Queued || m.comments.ran[c.ID] {
			out = append(out, c)
		}
	}
	declined := func(c store.Comment) bool {
		return c.DraftReply == "" && strings.TrimSpace(c.AgentNote) != ""
	}
	sort.SliceStable(out, func(i, j int) bool {
		if di, dj := declined(out[i]), declined(out[j]); di != dj {
			return di
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func (m monitorModel) cardCurrent() *store.Comment {
	fixes := m.cardFixes()
	if m.comments.cardIdx < 0 || m.comments.cardIdx >= len(fixes) {
		return nil
	}
	c := fixes[m.comments.cardIdx]
	return &c
}

// cardTally is the whole progress UI: approved · skipped · sent back · left.
func (m monitorModel) cardTally() (ok, skip, redo, left int) {
	for _, c := range m.cardFixes() {
		switch m.comments.decisions[c.ID] {
		case cardOK:
			ok++
		case cardSkip:
			skip++
		case cardRedo:
			redo++
		default:
			left++
		}
	}
	return
}

// nextUndecided is what enter advances to: the next fix without a decision,
// wrapping, and the ship card when none are left. This is what makes a 20-fix
// queue finish - you never land on a card you already handled.
func (m monitorModel) nextUndecided(from int) int {
	fixes := m.cardFixes()
	for i := 1; i <= len(fixes); i++ {
		idx := (from + i) % len(fixes)
		if m.comments.decisions[fixes[idx].ID] == "" {
			return idx
		}
	}
	return len(fixes) // the ship card
}

// updateCards routes keys for the card screen.
func (m monitorModel) updateCards(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.comments.cardMode == cardReply || m.comments.cardMode == cardAsk || m.comments.cardMode == cardResolve {
		return m.updateCardInput(msg)
	}
	fixes := m.cardFixes()
	if m.comments.cardMode == cardDiff {
		switch msg.String() {
		case "esc", "enter":
			// Give the terminal its mouse back the moment the viewer closes:
			// everywhere else in the app, the mouse belongs to select-and-copy.
			m.comments.cardMode = cardView
			return m, tea.DisableMouse
		case "up":
			m.comments.diffScroll--
		case "down":
			m.comments.diffScroll++
		case "pgup":
			m.comments.diffScroll -= m.height - 6
		case "pgdown":
			m.comments.diffScroll += m.height - 6
		}
		if m.comments.diffScroll < 0 {
			m.comments.diffScroll = 0
		}
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.view = viewDashboard
	case "up":
		if m.comments.cardSel > 0 {
			m.comments.cardSel--
		}
	case "down":
		if m.comments.cardSel < m.cardActionCount()-1 {
			m.comments.cardSel++
		}
	case "pgup":
		m.scrollCardDiff(-(m.height / 2))
	case "pgdown":
		m.scrollCardDiff(m.height / 2)
	case "left", "right":
		// Navigation, never a decision: browse every card in plain order,
		// wrapping through the ship card so → from the end and ← from the ship
		// card are inverses. Only with something to browse to.
		if len(fixes) > 1 {
			d := 1
			if msg.String() == "left" {
				d = -1
			}
			span := len(fixes) + 1
			m.comments.cardIdx = (m.comments.cardIdx + d + span) % span
			m.comments.cardSel = 0
			m.comments.diffScroll = 0 // a new card starts at the top of its diff
		}
	case "enter":
		return m.cardChoose()
	}
	return m, nil
}

// cardActionCount is how many rows the ↑↓ cursor can reach on this card.
func (m monitorModel) cardActionCount() int {
	if m.comments.cardIdx >= len(m.cardFixes()) {
		return 2 // SHIP IT (or can't) + the one secondary row
	}
	n := 5 // primary, Reply & resolve, Ask, Edit the reply, Skip
	if m.cardDiffTruncated() {
		n++ // See the whole diff
	}
	return n
}

// cardChoose acts on the highlighted row.
func (m monitorModel) cardChoose() (tea.Model, tea.Cmd) {
	fixes := m.cardFixes()
	if m.comments.cardIdx >= len(fixes) {
		return m.shipCardChoose()
	}
	c := fixes[m.comments.cardIdx]
	switch m.comments.cardSel {
	case 0: // the primary
		switch m.comments.decisions[c.ID] {
		case cardOK:
			// Undo does not advance - you stayed to change your mind, so you
			// stay to see the result.
			delete(m.comments.decisions, c.ID)
			return m, nil
		case cardRedo:
			return m, nil // waiting on the agent; nothing to press
		}
		if c.DraftReply == "" && strings.TrimSpace(c.AgentNote) != "" {
			// The agent answered instead of fixing (a question, an out-of-scope
			// call). The primary posts that answer as the reply: it opens the
			// reply field pre-filled with the agent's note so the human reviews
			// the words before they leave the machine. Posting resolves the
			// thread and drops it from the batch - fix cards ship separately.
			// A review body has no reply target on GitHub, so there the button
			// stays inert and the meta says why.
			if c.Kind != review.KindThread || m.comments.posting[c.ID] {
				return m, nil
			}
			m.comments.cardMode = cardResolve
			m.comments.nb.setText(strings.TrimSpace(c.AgentNote))
			return m, nil
		}
		m.comments.decisions[c.ID] = cardOK
		if len(fixes) == 1 {
			return m.shipApproved() // one fix: approve IS the ship; no ceremony
		}
		return m.cardAdvance()
	case 1: // Reply & resolve - the human's own answer, no agent (issue #16)
		if m.comments.posting[c.ID] {
			return m, nil // one request in flight per thread
		}
		m.comments.cardMode = cardResolve
		m.comments.nb.clear()
	case 2: // Ask for a different fix
		m.comments.cardMode = cardAsk
		m.comments.nb.clear()
	case 3: // Edit the reply - on an answer card, the reply IS the agent's note
		m.comments.cardMode = cardReply
		if c.DraftReply == "" && strings.TrimSpace(c.AgentNote) != "" {
			m.comments.nb.setText(strings.TrimSpace(c.AgentNote))
		} else {
			m.comments.nb.setText(c.DraftReply)
		}
	case 4: // Skip this one
		m.comments.decisions[c.ID] = cardSkip
		return m.cardAdvance()
	case 5: // See the whole diff
		// The one place mouse reporting turns on: the viewer has no action rows
		// for the wheel to fight, and the card - where copying matters - keeps
		// native selection because the mouse stays the terminal's there.
		m.comments.cardMode = cardDiff
		m.comments.diffScroll = 0
		return m, tea.EnableMouseCellMotion
	}
	return m, nil
}

// cardAdvance jumps to the next undecided card and dispatches any pending
// sent-back notes when the agent is free.
func (m monitorModel) cardAdvance() (tea.Model, tea.Cmd) {
	m.comments.cardIdx = m.nextUndecided(m.comments.cardIdx)
	m.comments.cardSel = 0
	m.comments.diffScroll = 0
	return m, m.dispatchRedos()
}

// dispatchRedos sends every not-yet-sent "different fix" note back to the
// agent in one revise run - immediately when the session is idle, otherwise on
// a later advance/tick once the current run finishes. The user keeps deciding
// other cards throughout; the ship gate holds until the rework returns.
func (m *monitorModel) dispatchRedos() tea.Cmd {
	s := m.sessionByTicket(m.comments.ticket)
	if s == nil || store.IsActive(s.State) {
		return nil
	}
	var notes []string
	for _, c := range m.cardFixes() {
		if m.comments.decisions[c.ID] == cardRedo && !m.comments.redoSent[c.ID] {
			notes = append(notes, fmt.Sprintf("The fix for the comment at %s must change: %s",
				locationOf(c), m.comments.redoNotes[c.ID]))
			m.comments.redoSent[c.ID] = true
		}
	}
	if len(notes) == 0 {
		return nil
	}
	m.comments.redoInFlight = true
	m.invalidateApproveDiff()
	return m.spawnRun(runArg(*s), "--address-comments", "--feedback", strings.Join(notes, "\n"))
}

// cardReworkArrived is called when the session parks back at fix-review: every
// sent-back card becomes undecided again, carrying a note that it was reworked,
// so the enter-jump reaches it.
func (m *monitorModel) cardReworkArrived() {
	for id, d := range m.comments.decisions {
		if d == cardRedo && m.comments.redoSent[id] {
			delete(m.comments.decisions, id)
			delete(m.comments.redoSent, id)
			m.comments.reworked[id] = true
		}
	}
}

// shipCardChoose is the ship card's two rows.
func (m monitorModel) shipCardChoose() (tea.Model, tea.Cmd) {
	_, _, redo, left := m.cardTally()
	if m.comments.cardSel == 1 {
		if left > 0 || redo > 0 {
			// Go to the first undecided fix - never make the user hunt for it.
			for i, c := range m.cardFixes() {
				if m.comments.decisions[c.ID] == "" {
					m.comments.cardIdx, m.comments.cardSel = i, 0
					return m, nil
				}
			}
			return m, nil
		}
		// Go back and re-check: start again at fix 1, decisions intact.
		m.comments.cardIdx, m.comments.cardSel = 0, 0
		return m, nil
	}
	if left > 0 || redo > 0 {
		return m, nil // CAN'T SHIP YET
	}
	return m.shipApproved()
}

// shipApproved queues exactly the approved fixes and spawns the ship run -
// the one action that leaves the machine - then hands off to the dashboard,
// where a failed ship is a NEEDS YOU row that says why.
func (m monitorModel) shipApproved() (tea.Model, tea.Cmd) {
	s := m.sessionByTicket(m.comments.ticket)
	if s == nil {
		m.notice = "no session for " + m.comments.ticket
		return m, nil
	}
	var approved []string
	m.comments.ran = map[string]bool{}
	for _, c := range m.cardFixes() {
		if m.comments.decisions[c.ID] == cardOK {
			approved = append(approved, c.ID)
			m.comments.ran[c.ID] = true
		}
	}
	if len(approved) == 0 {
		// Every fix was skipped/declined: SHIP IT still has to do SOMETHING,
		// or the ticket is stuck at fix-review forever with a live-looking
		// button that silently no-ops. Nothing was approved, so nothing
		// ships - release the ticket back to plain review instead, the same
		// way comments_fetch.go already releases it when GitHub resolves
		// every queued thread out from under the batch. CAS from
		// fix-review only, so a run started meanwhile is never stomped.
		if m.store != nil {
			_ = m.store.ClearQueued(m.comments.ticket)
			_, _ = m.store.SetStateIf(m.comments.ticket, store.StateFixReview, store.StateReview, s.Retries)
		}
		m.invalidateApproveDiff()
		m.view = viewDashboard
		m.notice = "nothing approved - " + m.comments.ticket + " released back to review"
		return m, nil
	}
	if m.store != nil {
		if err := m.store.QueueComments(m.comments.ticket, approved); err != nil {
			m.notice = "queueing the approved fixes: " + err.Error()
			return m, nil
		}
	}
	m.invalidateApproveDiff()
	m.view = viewDashboard
	m.notice = fmt.Sprintf("shipping %s on %s - verify, commit, push & reply",
		plural(len(approved), "fix"), m.comments.ticket)
	return m, m.spawnRun(runArg(*s), "--ship-comments")
}

// updateCardInput owns the keyboard while a field is open at the block it
// modifies - the reply in place, or the what-should-it-do-instead note.
func (m monitorModel) updateCardInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		m.comments.nb.paste(string(msg.Runes))
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.comments.cardMode = cardView
		m.comments.nb.clear()
	case tea.KeyEnter:
		text := strings.TrimSpace(m.comments.nb.text())
		mode := m.comments.cardMode
		m.comments.cardMode = cardView
		m.comments.nb.clear()
		c := m.cardCurrent()
		if c == nil {
			return m, nil
		}
		if mode == cardResolve {
			return m.postReply(*c, text)
		}
		if mode == cardReply {
			if text == "" {
				return m, nil
			}
			if m.store != nil {
				// An answer card's reply lives in AgentNote; writing it to
				// DraftReply would silently turn the card into a fix card and
				// ship the answer with a bogus "Fixed in <sha>" suffix.
				var err error
				if c.DraftReply == "" && strings.TrimSpace(c.AgentNote) != "" {
					err = m.store.SetCommentAgentNote(c.ID, text)
				} else {
					err = m.store.SetDraftReply(c.ID, text)
				}
				if err != nil {
					m.notice = "saving the reply: " + err.Error()
					return m, nil
				}
			}
			m.loadComments()
			return m, nil
		}
		// cardAsk: mark sent back and move on; the agent reworks in the
		// background while the remaining cards get decided.
		if text == "" {
			return m, nil
		}
		m.comments.decisions[c.ID] = cardRedo
		m.comments.redoNotes[c.ID] = text
		return m.cardAdvance()
	default:
		m.comments.nb.key(msg)
	}
	return m, nil
}

// tickCards watches the rework round trip: the revise run going active, then
// the session parking back at fix-review, at which point every sent-back card
// becomes undecided again with its reworked note.
func (m *monitorModel) tickCards() {
	if m.comments.redoInFlight {
		s := m.sessionByTicket(m.comments.ticket)
		if s != nil && store.IsActive(s.State) {
			m.comments.redoWasActive = true
		} else if m.comments.redoWasActive && m.phase() == phaseApprove {
			m.comments.redoInFlight, m.comments.redoWasActive = false, false
			m.cardReworkArrived()
			m.invalidateApproveDiff()
		}
	}
}

// cardsActive is whether the card screen owns the comments view right now -
// the approve phase, or a revise run spawned from the cards.
func (m monitorModel) cardsActive() bool {
	return m.phase() == phaseApprove ||
		(m.phase() == phaseRunning && len(m.comments.decisions) > 0)
}

// scrollCardDiff moves the inline diff window, clamped to its content.
func (m *monitorModel) scrollCardDiff(delta int) {
	c := m.cardCurrent()
	if c == nil {
		return
	}
	rows, budget := m.cardDiffWindowSpec(c, contentWidthFor(m.width))
	maxScroll := len(rows) - budget
	if maxScroll < 0 {
		maxScroll = 0
	}
	m.comments.diffScroll += delta
	if m.comments.diffScroll > maxScroll {
		m.comments.diffScroll = maxScroll
	}
	if m.comments.diffScroll < 0 {
		m.comments.diffScroll = 0
	}
}
