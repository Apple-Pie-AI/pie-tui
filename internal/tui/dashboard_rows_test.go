package tui

import (
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// flatRows is the row slice reload would build when every session sits in a
// plain (non-collapsible) group - the shape most hand-built fixtures need.
func flatRows(n int) []listRow {
	rows := make([]listRow, n)
	for i := range rows {
		rows[i] = listRow{kind: rowTicket, flat: i}
	}
	return rows
}

// rowGroups builds groups with sessions already filed, mirroring reload.
func rowGroups(sessions ...store.Session) []*group {
	groups := newGroups()
	for _, s := range sessions {
		for _, g := range groups {
			if g.match(s) {
				g.sessions = append(g.sessions, s)
				break
			}
		}
	}
	return groups
}

// The zero-value expanded map means "every collapsible group folded" - the
// default posture on every launch, with no init code to forget.
func TestBuildRowsDefaultCollapsed(t *testing.T) {
	groups := rowGroups(
		store.Session{Ticket: "A", State: store.StateNeedsYou},
		store.Session{Ticket: "B", State: store.StateReview},
		store.Session{Ticket: "C", State: store.StateStopped},
		store.Session{Ticket: "D", State: store.StateMerged},
		store.Session{Ticket: "E", State: store.StateClosed},
	)
	rows := buildRows(groups, nil)

	// 2 tickets visible (A, B) + 2 header rows (STOPPED, CLOSED); C/D/E hidden.
	var tickets, headers int
	for _, r := range rows {
		switch r.kind {
		case rowTicket:
			tickets++
		case rowHeader:
			headers++
		}
	}
	if tickets != 2 || headers != 2 {
		t.Fatalf("rows = %d tickets + %d headers, want 2 + 2", tickets, headers)
	}
}

func TestBuildRowsExpanded(t *testing.T) {
	groups := rowGroups(
		store.Session{Ticket: "A", State: store.StateNeedsYou},
		store.Session{Ticket: "C", State: store.StateStopped},
		store.Session{Ticket: "D", State: store.StateMerged},
		store.Session{Ticket: "E", State: store.StateClosed},
	)
	rows := buildRows(groups, map[string]bool{"CLOSED": true})

	// A + CLOSED header + D + E + STOPPED header (folded). CLOSED sits above
	// STOPPED: a resolved PR is an outcome, a stopped run is debris.
	if len(rows) != 5 {
		t.Fatalf("len(rows) = %d, want 5", len(rows))
	}
	if rows[4].kind != rowHeader || groups[rows[4].group].label != "STOPPED" {
		t.Fatal("the folded STOPPED header should be the last row, below CLOSED")
	}
	// The expanded CLOSED tickets must map to the right flat slots even with
	// other groups folded around them.
	flat := []store.Session{}
	for _, g := range groups {
		flat = append(flat, g.sessions...)
	}
	for _, r := range rows[2:4] {
		if r.kind != rowTicket {
			t.Fatalf("rows after the CLOSED header should be tickets, got kind %d", r.kind)
		}
		if got := flat[r.flat].State; got != store.StateMerged && got != store.StateClosed {
			t.Fatalf("CLOSED section row maps to a %q session - flat indexing skewed by folded groups", got)
		}
	}
}

// Toggling a header must keep the cursor on that header: expansion inserts
// rows only after it, collapse removes only after it.
func TestToggleKeepsCursorOnHeader(t *testing.T) {
	m := monitorModel{
		groups: rowGroups(
			store.Session{Ticket: "A", State: store.StateNeedsYou},
			store.Session{Ticket: "D", State: store.StateMerged},
		),
	}
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}
	m.rows = buildRows(m.groups, m.expanded)
	m.cursor = len(dashCommands) + 1 // A, then the CLOSED header

	g := m.headerUnderCursor()
	if g == nil || g.label != "CLOSED" {
		t.Fatalf("cursor should sit on the CLOSED header, got %+v", g)
	}
	m.expanded = map[string]bool{"CLOSED": true}
	m.rows = buildRows(m.groups, m.expanded)
	if got := m.headerUnderCursor(); got == nil || got.label != "CLOSED" {
		t.Fatal("after expanding, the same cursor index must still be the CLOSED header")
	}
	if sel := m.selected(); sel != nil {
		t.Fatalf("selected() on a header = %v, want nil", sel.Ticket)
	}
}

// syncCursorTo must not move the cursor to a ticket hidden in a folded group.
func TestSyncCursorToHiddenTicket(t *testing.T) {
	m := monitorModel{
		groups: rowGroups(
			store.Session{Ticket: "A", State: store.StateNeedsYou},
			store.Session{Ticket: "D", State: store.StateMerged},
		),
	}
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}
	m.rows = buildRows(m.groups, m.expanded)

	m.cursor = 0
	m.syncCursorTo("D") // hidden behind the folded CLOSED header
	if m.cursor != 0 {
		t.Fatalf("cursor moved to %d for a hidden ticket, want unchanged", m.cursor)
	}
	m.syncCursorTo("A")
	if sel := m.selected(); sel == nil || sel.Ticket != "A" {
		t.Fatal("syncCursorTo should land on a visible ticket")
	}
}

// The list renderer counts one line per row it draws (plus plain headers), and
// that count budgets the log tail - a collapsed group must not inflate it.
func TestListLinesCollapsedVsExpanded(t *testing.T) {
	m := monitorModel{
		groups: rowGroups(
			store.Session{Ticket: "A", State: store.StateNeedsYou},
			store.Session{Ticket: "D", State: store.StateMerged},
			store.Session{Ticket: "E", State: store.StateClosed},
		),
	}
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}

	m.rows = buildRows(m.groups, nil)
	_, collapsed := m.renderTicketList(100)
	// NEEDS YOU header + A + CLOSED header row.
	if collapsed != 3 {
		t.Fatalf("collapsed listLines = %d, want 3", collapsed)
	}

	m.expanded = map[string]bool{"CLOSED": true}
	m.rows = buildRows(m.groups, m.expanded)
	_, expanded := m.renderTicketList(100)
	if expanded != 5 {
		t.Fatalf("expanded listLines = %d, want 5", expanded)
	}
}
