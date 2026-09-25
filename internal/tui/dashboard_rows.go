// The dashboard's cursor-row model. The three pinned command rows keep their
// hardcoded indices 0-2; everything below them is a listRow - a ticket, or the
// Enter-operable header of a collapsible group. This is the one place that
// knows how cursor indices map onto sessions, so collapsing a group cannot
// desync the arithmetic scattered around the hub.
package tui

import "github.com/Apple-Pie-AI/pie-tui/internal/store"

type rowKind int

const (
	rowTicket rowKind = iota
	rowHeader
)

// listRow is one selectable line of the ticket list. group indexes m.groups;
// flat indexes m.flat (rowTicket only).
type listRow struct {
	kind  rowKind
	group int
	flat  int
}

// buildRows lays out the list rows for the current groups: a header row for
// each non-empty collapsible group, then its tickets unless collapsed. Plain
// groups contribute tickets only - their headers stay render-only, exactly as
// before collapsing existed. expanded's zero value therefore means "every
// collapsible group folded", which is the wanted default on every launch.
func buildRows(groups []*group, expanded map[string]bool) []listRow {
	var rows []listRow
	flat := 0
	for gi, g := range groups {
		if len(g.sessions) == 0 {
			continue
		}
		if g.collapsible {
			rows = append(rows, listRow{kind: rowHeader, group: gi, flat: -1})
			if !expanded[g.label] {
				flat += len(g.sessions)
				continue
			}
		}
		for range g.sessions {
			rows = append(rows, listRow{kind: rowTicket, group: gi, flat: flat})
			flat++
		}
	}
	return rows
}

// rowUnderCursor maps m.cursor onto the list row it sits on, or nil when it
// is on a command row (0-2) or out of range.
func (m *monitorModel) rowUnderCursor() *listRow {
	i := m.cursor - len(dashCommands)
	if i < 0 || i >= len(m.rows) {
		return nil
	}
	return &m.rows[i]
}

// headerUnderCursor is the group whose header row the cursor is on, or nil.
func (m *monitorModel) headerUnderCursor() *group {
	if r := m.rowUnderCursor(); r != nil && r.kind == rowHeader {
		return m.groups[r.group]
	}
	return nil
}

// selected returns the highlighted agent, or nil when the cursor is on one of
// the three pinned command rows or a section header.
func (m *monitorModel) selected() *store.Session {
	r := m.rowUnderCursor()
	if r == nil || r.kind != rowTicket {
		return nil
	}
	return &m.flat[r.flat]
}

// syncCursorTo points the selection at a ticket by id, so cursor-based actions
// act on it even if a background reload reordered the list. A ticket hidden
// inside a collapsed group stays hidden: the cursor is left where it is, the
// same as when the ticket is not found at all.
func (m *monitorModel) syncCursorTo(ticket string) {
	for i, r := range m.rows {
		if r.kind == rowTicket && m.flat[r.flat].Ticket == ticket {
			m.cursor = i + len(dashCommands)
			return
		}
	}
}
