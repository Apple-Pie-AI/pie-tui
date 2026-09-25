// The dashboard's rows: the three command rows at the top and the ticket rows
// below them, drawn on one grid so a single cursor runs from the first line of
// the screen to the last.
//
// No row carries a leading glyph. The section header above a ticket already
// states its state and the phase word carries it again, so ⚑ ● ✓ ■ said a third
// time what two other things on the same line were already saying - and read as
// keyboard shortcuts, which this UI does not have. The ▸ cursor stays: it is the
// only selection cue that survives NO_COLOR.
package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// The command rows, in cursor order. They are part of the same ↑↓ list as the
// tickets, which is why they are rows and not a separate bar.
var dashCommands = []struct{ label, meta string }{
	{"Start new ticket(s)", "from Jira, or a description you type"},
	{"Checkout a branch", "check out an existing branch into a pie worktree"},
	{"Edit config", "allowlist, repo, branch, model fields"},
}

// renderCommandRow draws one of the three commands.
//
// A command and a menu item are the same kind of thing - a verb and the
// consequence of choosing it - so they are built the same way. Only the row the
// cursor is on is filled; both commands rendering filled at once was two cursors
// on one screen, and no way to tell which one enter would act on.
func (m monitorModel) renderCommandRow(i int, cw int, cursor bool) string {
	c := dashCommands[i]
	r := dashCols(cw)

	// The label starts in the id column, so the commands line up with the ticket
	// ids under them rather than being indented past them. It does not inherit
	// the title column's flex: a command label is a fixed phrase, and letting it
	// stretch to a wide screen left its consequence truncated with a hundred
	// empty cells to its left.
	labelW := max(r.id+dashGapW, 32)
	metaW := cw - dashCursorW - labelW

	sty := stAccent
	if i == len(dashCommands)-1 {
		sty = stMeta // Edit config is quieter than the two that start work
	}
	if cursor {
		sty = stFocus
	}
	mark := " "
	if cursor {
		mark = "▸"
	}
	cells := []string{
		cell(stAccent, cursor, dashCursorW, lipgloss.Left, mark),
		cell(sty, cursor, labelW, lipgloss.Left, truncate(c.label, labelW)),
	}
	if metaW > 4 {
		cells = append(cells, cell(stChrome, cursor, metaW, lipgloss.Left,
			truncate(c.meta, metaW)))
	}
	return band(cursor, cw, cells...)
}

// renderRow is one ticket.
func (m monitorModel) renderRow(s store.Session, cw int, selected bool) string {
	r := dashCols(cw)

	// ShortDesc (LLM-generated <10-word gist) → Summary → latest log line.
	desc := s.ShortDesc
	if desc == "" {
		desc = s.Summary
	}
	if desc == "" {
		desc = cleanActivity(tailLines(paths.LogFor(s.Ticket), 1), s.Ticket)
	}
	desc = stripEmphasis(desc)

	idSty, titleSty := stMeta, stTitle
	if selected {
		titleSty = stFocus
	}
	mark := " "
	if selected {
		mark = "▸"
	}

	cells := []string{
		cell(stAccent, selected, dashCursorW, lipgloss.Left, mark),
		cell(idSty, selected, r.id+dashGapW, lipgloss.Left, truncFront(s.Ticket, r.id)),
		cell(titleSty, selected, r.title, lipgloss.Left, truncate(desc, r.title)),
	}
	if r.phase {
		// An agent paused on a permission question outranks the state word:
		// it is live and waiting on a keystroke, not merely in a state.
		psty, word := phaseStyle(s), m.phaseWord(s)
		if m.approvalsFor(s.Ticket) > 0 {
			psty, word = stAmber, "approve? ⏸"
		}
		cells = append(cells, cell(psty, selected, dashPhaseW, lipgloss.Left,
			truncate(word, dashPhaseW)))
	}
	if r.age {
		cells = append(cells, cell(stChrome, selected, dashAgeW, lipgloss.Right,
			Age(s.UpdatedAt)))
	}
	return band(selected, cw, cells...)
}

// phaseWord is the row's phase, blank when it would only repeat the section
// header above it. "under review" on every row of READY FOR REVIEW is a column
// spent restating what the reader just read.
func (m monitorModel) phaseWord(s store.Session) string {
	label := stateLabel(s)
	if g := m.groupOf(s); g != nil && redundantPhase(g.label, label) {
		return ""
	}
	if s.Retries > 0 {
		label += fmt.Sprintf(" ×%d", s.Retries)
	}
	return label
}

// redundantPhase is whether a phase word only restates its section header.
func redundantPhase(section, phase string) bool {
	switch section {
	case "PR READY FOR REVIEW":
		return phase == "under review"
	case "STOPPED":
		return phase == "stopped"
	case "NEEDS YOU":
		return phase == "needs-you"
	case "NO PULL REQUEST YET":
		return phase == "checked out"
	}
	return false
}

// groupOf finds the section a session was filed under, so a row can tell what
// its header already said.
func (m monitorModel) groupOf(s store.Session) *group {
	for _, g := range m.groups {
		for _, gs := range g.sessions {
			if gs.Ticket == s.Ticket {
				return g
			}
		}
	}
	return nil
}

// phaseStyle colours the phase word. The state glyph carried this colour before
// it was deleted; the word carries it now, so nothing was lost with the icon.
func phaseStyle(s store.Session) lipgloss.Style {
	if s.State == store.StateStopped || isStopped(s) {
		return stRed
	}
	switch s.State {
	case store.StateReview:
		if s.OpenComments > 0 {
			return stAmber // reviewers are waiting on you, not the other way round
		}
		return stAccent
	case "merged":
		return stAccent
	case store.StateCheckedOut:
		return lipgloss.NewStyle().Foreground(colorTracking)
	case "failed":
		return stRed
	case "awaiting-answer", "needs-you", store.StatePlanReview, store.StateFixReview,
		store.StateChangeReview:
		return stAmber
	case "closed":
		return stChrome
	default:
		return stCyan // anything still working
	}
}
