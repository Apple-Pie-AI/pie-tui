// The command palette. Opened on an agent row (Enter) it lists only that
// agent's actions; opened as the command menu (":") it adds the global ones.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

type paletteItem struct {
	key   actionID
	label string
	desc  string
}

// paletteItems is the palette menu. Opened on a specific agent (Enter on its
// row) it shows ONLY that agent's actions; opened as the command menu (":") it
// shows the global commands too. Rebuilt each open so it reflects the current
// selection/daemon.
func (m monitorModel) paletteItems() []paletteItem {
	var items []paletteItem
	if s := m.selected(); s != nil {
		// A pending permission question leads the menu: the agent is paused
		// on it, so it is almost always why the menu was opened - but it is a
		// row here, not an Enter-hijack, so Stop/Pause stay reachable with
		// arrows alone. (Lives here rather than agentActions because the
		// pending count is the model's, and agentActions is a pure function
		// of the session.)
		if n := m.approvalsFor(s.Ticket); n > 0 {
			items = append(items, paletteItem{actApprove,
				fmt.Sprintf("Approve pending command (%d waiting)", n),
				"the agent is paused until you allow or deny it"})
		}
		items = append(items, agentActions(*s)...)
	}
	if m.paletteAgentOnly {
		return items // scoped to the selected agent's actions
	}
	items = append(items,
		paletteItem{actRun, "Start new ticket(s)", "launch a ticket key or .md file"},
		paletteItem{actRunFromBranch, "Checkout a branch", "check out an existing branch; pie tracks its PR"},
		paletteItem{actDoctor, "Doctor", "connectivity + setup health check"},
		paletteItem{actConfig, "Edit config", "allowlist, repo, branch"},
		paletteItem{actModels, "Edit models", "which Claude model runs each stage"},
		paletteItem{actSetup, "Setup wizard", "configure repo, models, and token"},
	)
	if _, ok := daemonAlive(); ok {
		items = append(items, paletteItem{actDaemonStop, "Stop daemon", "stop the background poller"})
	} else {
		items = append(items, paletteItem{actDaemonStart, "Start daemon", "poll Jira and run the fleet in the background"})
	}
	return append(items, paletteItem{actQuit, "Quit", "exit the hub"})
}

func (m monitorModel) updatePalette(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.paletteItems()
	switch msg.String() {
	case "esc", "q":
		m.view = viewDashboard
	case "up", "k":
		if m.paletteCursor > 0 {
			m.paletteCursor--
		}
	case "down", "j":
		// len(items) rather than len(items)-1: Close sits one past the actions.
		if m.paletteCursor < len(items) {
			m.paletteCursor++
		}
	case "enter":
		if m.paletteCursor >= len(items) {
			m.view = viewDashboard // the Close row does exactly what esc does
			return m, nil
		}
		id := items[m.paletteCursor].key
		m.view = viewDashboard // close the menu; doAction may reopen another view
		return m.doAction(id)
	}
	return m, nil
}

// renderPalette draws the actions menu.
//
// Two fixed columns - the verb, and the consequence of choosing it - because a
// list of bare verbs makes you open one to find out what it does. The
// consequence is truncated, never wrapped: a menu whose rows are different
// heights cannot be scanned down its left edge.
func (m monitorModel) renderPalette(w int) string {
	if tooNarrow(w) {
		return narrowNotice(w)
	}
	margin, cw := layout(w)
	items := m.paletteItems()

	var b strings.Builder
	b.WriteString(m.renderPaletteHeader(cw))
	b.WriteString("\n")

	// The verb column is a third of the screen, bounded: wide enough for the
	// longest real label, never so wide it starves the consequence beside it.
	verbW := min(30, max(16, cw/3))
	descW := cw - verbW - dashCursorW - dashGapW

	for i, it := range items {
		b.WriteString(m.renderPaletteItem(it.label, it.desc, cw, verbW, descW, i == m.paletteCursor))
	}
	// Close is a row like any other, so ↓ reaches it and nothing on this screen
	// is keyboard-only.
	b.WriteString(stChrome.Render(repeat("─", min(cw, 60))) + "\n")
	b.WriteString(m.renderPaletteItem("Close", "esc", cw, verbW, descW, m.paletteCursor >= len(items)))
	// The rule and the key hints below it are the frame's, not this screen's -
	// drawing them here too put two rules across the bottom of the menu.
	return indent(b.String(), margin)
}

// renderPaletteHeader names the ticket the menu belongs to. "Actions" alone, or
// "Actions - 12", made you remember which row you had pressed enter on.
func (m monitorModel) renderPaletteHeader(cw int) string {
	left := stBrand.Render(letterSpace("APPLE PIE")) + "   " + stMeta.Render("actions")
	head := padBetween(left, stChrome.Render(m.lastRefresh.Format("15:04")), cw)

	ctx := stMeta.Render("every command")
	if s := m.selected(); s != nil {
		ctx = stTitle.Render(s.Ticket)
		if d := s.ShortDesc; d != "" {
			ctx += stMeta.Render(" — " + d)
		} else if s.Summary != "" {
			ctx += stMeta.Render(" — " + s.Summary)
		}
	}
	return head + "\n" + truncate(ctx, cw) + "\n" + rule(cw) + "\n"
}

// renderPaletteItem is one menu row, on the same grid and the same fill as a
// dashboard row - a menu item and a command row are the same kind of thing.
func (m monitorModel) renderPaletteItem(label, desc string, cw, verbW, descW int, on bool) string {
	mark := " "
	labelSty := stTitle
	if on {
		mark, labelSty = "▸", stFocus
	}
	cells := []string{
		cell(stAccent, on, dashCursorW, lipgloss.Left, mark),
		cell(labelSty, on, verbW+dashGapW, lipgloss.Left, truncate(label, verbW)),
	}
	if descW > 4 {
		cells = append(cells, cell(stChrome, on, descW, lipgloss.Left, truncate(desc, descW)))
	}
	return band(on, cw, cells...) + "\n"
}

// ---- run-new: method picker -----------------------------------------------
