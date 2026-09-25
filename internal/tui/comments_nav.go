// Moving around the triage screen, and the one place it accepts typing.
//
// The whole screen is four keys - up, down, left, right - plus enter and esc.
// There are no letter shortcuts by design: anything the screen can do is drawn
// as a row in the one list, so what you can reach is what you can see. Split
// out of comments.go, which owns the screen's state.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// updateComments routes keys. Three, plus esc: up and down move, enter opens
// whatever the cursor is on, esc backs out one level.
func (m monitorModel) updateComments(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.comments.mode {
	case modeEditing:
		return m.updateInstruction(msg)
	case modeReplying:
		return m.updateReply(msg)
	case modeMenu:
		return m.updateMenu(msg)
	}
	if m.phase() == phaseApprove {
		return m.updateCards(msg)
	}
	if m.phase() == phaseRunning {
		if len(m.comments.decisions) > 0 {
			return m.updateCards(msg) // deciding continues while a revise runs
		}
		// Frozen: the batch is already with the agent, so there is nothing here
		// left to decide. esc returns to the dashboard and the run carries on.
		if msg.String() == "esc" {
			m.view = viewDashboard
		}
		return m, nil
	}
	if m.comments.cardMode == cardDiff {
		// The full-screen viewer, borrowed from the cards: ↑↓/PgUp/PgDn
		// scroll, esc or enter returns to the triage list.
		switch msg.String() {
		case "esc", "enter":
			m.comments.cardMode = cardView
		case "up":
			m.comments.diffScroll--
		case "down":
			m.comments.diffScroll++
		case "pgup":
			m.comments.diffScroll -= m.height / 2
		case "pgdown":
			m.comments.diffScroll += m.height / 2
		}
		if m.comments.diffScroll < 0 {
			m.comments.diffScroll = 0
		}
		return m, nil
	}
	// Three keys, one meaning each. No letters: every one of them would be a
	// shortcut for something already on screen as a row.
	switch msg.String() {
	case "esc":
		m.view = viewDashboard
	case "up":
		m.moveCursor(-1)
	case "down":
		m.moveCursor(1)
	case "pgup":
		m.comments.scroll -= m.bodyHeight()
		m.clampCommentScroll()
	case "pgdown":
		m.comments.scroll += m.bodyHeight()
		m.clampCommentScroll()
	case "enter":
		return m.activate()
	}
	return m, nil
}

// moveCursor walks the aggregate row, the comments, and then the run bar,
// clamping at both ends.
func (m *monitorModel) moveCursor(delta int) {
	last := len(m.visible()) // the run bar
	next := m.comments.cursor + delta
	if next < m.cursorFloor() || next > last {
		return
	}
	m.comments.cursor = next
	m.renderFocused()
}

// activate runs whatever the cursor is on: the run bar, the focused row's
// checkbox, or the picked rail action.
func (m monitorModel) activate() (tea.Model, tea.Cmd) {
	// The aggregate row: same box, same enter, applied to the whole list.
	if m.onAllRow() {
		m.toggleAll()
		return m, nil
	}
	if m.onRunBar() {
		if m.phase() == phaseDone {
			m.view = viewDashboard
			return m, nil
		}
		return m.queueAndFix()
	}
	// Once the run is done the batch is history: there is nothing to select or
	// annotate, so a comment row is only a thing to read.
	c := m.focused()
	if c == nil || m.phase() == phaseDone {
		return m, nil
	}
	// A comment has several things you might do to it, so enter opens its menu.
	if m.phase() == phaseIdle {
		m.openMenu()
		return m, nil
	}
	return m, nil
}
