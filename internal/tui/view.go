// The render router - View's switch over m.view is the mirror of dispatchKey's,
// and the two must stay in step.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m monitorModel) View() string {
	w := m.width
	if w == 0 {
		w = 80
	}
	var b strings.Builder

	needs, running := 0, 0
	for _, g := range m.groups {
		switch g.label {
		case "NEEDS YOU":
			needs += len(g.sessions)
		case "RUNNING":
			running += len(g.sessions)
		}
	}
	daemon := "○ daemon stopped"
	if pid, ok := daemonAlive(); ok {
		daemon = fmt.Sprintf("● daemon pid %d", pid)
	}
	// The restyled screens carry their own header: the brand, what screen this
	// is, and that screen's own counts. A second bar above it - bold, brightest
	// on the screen, and counting a different scope - contradicted the numbers
	// directly below it, so it is suppressed there rather than competing.
	if !m.ownsHeader() {
		b.WriteString(headerStyle.Render(fmt.Sprintf("Apple Pie · %d running · %d needs you", running, needs)))
		b.WriteString(dimStyle.Render("   "+m.lastRefresh.Format("15:04:05")+"  ·  "+daemon) + "\n")
	}

	if m.err != nil {
		b.WriteString(lipgloss.NewStyle().Foreground(colorNeedsYou).Render("  error: "+m.err.Error()) + "\n")
		return b.String()
	}

	// Body for the active view; the action bar is appended at the bottom by frame.
	var body string
	switch m.view {
	case viewConsent:
		body = m.renderConsent(w)
	case viewPalette:
		body = m.renderPalette(w)
	case viewRunMethod:
		body = m.renderRunMethod(w)
	case viewRunInput:
		body = m.renderRunInput(w)
	case viewOutput:
		body = m.renderOutput(w)
	case viewPlan:
		body = m.renderPlan(w)
	case viewForm:
		if m.form != nil {
			body = m.form.render(w)
		}
	case viewRepoFix:
		body = m.renderRepoFix(w)
	case viewComments:
		body = m.renderComments(w)
	case viewEditConfig:
		body = m.renderEditConfig(w)
	case viewPermissions:
		body = m.renderPermissions(w)
	case viewEditModels:
		body = m.renderEditModels(w)
	case viewChange:
		body = m.renderChange(w)
	default:
		body = m.renderDashboardBody(w)
	}

	notice := ""
	if m.notice != "" && m.confirm.ticket == "" {
		notice = whyStyle.Render(truncate("  "+m.notice, w))
	}
	// Footer hint. Modal views render their own hints in the body.
	footer := ""
	if m.view == viewDashboard && !m.answer.open && m.confirm.ticket == "" {
		margin, cw := layout(w)
		left := "↑↓ move   enter menu   esc quit"
		right := ": commands"
		if lipWidth(left)+lipWidth(right)+2 > cw {
			right = ""
		}
		footer = strings.Repeat(" ", margin) +
			padBetween(stChrome.Render(truncate(left, cw)), stChrome.Render(right), cw)
	} else if m.view == viewPalette {
		margin, cw := layout(w)
		footer = strings.Repeat(" ", margin) +
			stChrome.Render(truncate("↑↓ select   enter choose   esc close", cw))
	} else if m.view == viewConsent {
		footer = dimStyle.Render("  y: share · n: skip")
	} else if m.confirm.ticket != "" {
		footer = dimStyle.Render("  ↑↓ move · enter select · esc cancel")
	} else if m.view == viewPlan && m.plan.feedback {
		footer = dimStyle.Render("  type your correction · enter: send · esc: cancel · ↑↓: scroll plan")
	} else if m.view == viewPlan {
		footer = dimStyle.Render("  ←→ select · enter: choose · ↑↓/PgUp/PgDn: scroll · esc: back")
	} else if m.view == viewComments && m.comments.mode == modeReplying {
		footer = m.commentsFooter(w, "type your reply (empty = resolve only)   enter post   esc cancel")
	} else if m.view == viewComments && m.comments.mode == modeEditing {
		footer = m.commentsFooter(w, "type your instruction   enter save   esc cancel")
	} else if m.view == viewComments && m.comments.mode == modeMenu {
		footer = m.commentsFooter(w, "↑↓ choose   enter run it   esc close the menu")
	} else if m.view == viewComments && m.phase() == phaseRunning {
		footer = dimStyle.Render("  esc  back to the dashboard (the run carries on)")
	} else if m.view == viewComments && m.phase() == phaseDone && m.onRunBar() {
		footer = dimStyle.Render("  up down  move      enter  done")
	} else if m.view == viewComments && m.phase() == phaseDone {
		footer = dimStyle.Render("  up down  move      esc  back")
	} else if m.view == viewComments && m.phase() == phaseApprove {
		footer = "" // the cards draw their own keys and tally
	} else if m.view == viewComments {
		// enter means one thing per row, so the footer says which - and the right
		// half is the legend for the boxes, the one thing this screen has to tell
		// a newcomer.
		verb := "open the menu"
		switch {
		case m.onAllRow():
			verb = "check or uncheck all"
		case m.onRunBar():
			verb = "run it"
		}
		margin, cw := layout(w)
		left := truncate("↑↓ move   enter "+verb+"   esc back", cw)
		// The legend is the first thing given up when the two halves will not fit:
		// a narrow terminal keeps the keys and loses the explanation of them.
		right := "[x] fixes it · [ ] left alone · ✎ has an instruction"
		if lipWidth(left)+lipWidth(right)+2 > cw {
			right = ""
		}
		footer = strings.Repeat(" ", margin) +
			padBetween(stChrome.Render(left), stChrome.Render(right), cw)
	} else if m.answer.open {
		footer = dimStyle.Render("  enter: send & resume · esc: cancel")
	}
	// The confirming state replaces the body with a mini palette.
	if m.confirm.ticket != "" {
		body = m.renderConfirm(w)
	}
	// So does a pending permission approval, once opened: an agent is paused
	// mid-session on this exact answer.
	if m.approval.open {
		body = m.renderApprovals(w)
		footer = dimStyle.Render("  ↑↓ choose   enter decide   esc leave it pending")
	}
	return m.frame(b.String(), body, notice, footer, w)
}

// ownsHeader is whether the active screen draws its own header. The restyled
// screens do; the rest still take the frame's.
func (m monitorModel) ownsHeader() bool {
	return m.view == viewComments || m.view == viewDashboard || m.view == viewPalette ||
		m.view == viewChange
}

// frameRule is the separator above the footer. On a screen with its own content
// width it matches that width and inset, rather than running the whole terminal
// - a rule wider than everything above it is the loudest thing on the screen.
func (m monitorModel) frameRule(w int) string {
	if m.ownsHeader() {
		margin, cw := layout(w)
		return strings.Repeat(" ", margin) + rule(cw)
	}
	return sepStyle.Render(strings.Repeat("─", w))
}

// frame composes header + body + a bottom-pinned footer (separator, optional
// notice, action bar). Padding keeps the action bar on the last row so clicks
// hit it (mouse Y == height-1).
func (m monitorModel) frame(head, body, notice, bar string, w int) string {
	var b strings.Builder
	b.WriteString(head)
	if head != "" && !strings.HasSuffix(head, "\n") {
		b.WriteString("\n")
	}
	footerLines := 2 // separator + bar
	if notice != "" {
		footerLines++
	}
	if body != "" {
		body = strings.TrimRight(body, "\n")
		// Overflowing the terminal scrolls the whole frame and loses the top of
		// the screen, so a body that will not fit is cut instead. This should not
		// fire - each screen budgets its own elastic region - but a screen that
		// mis-counts by one should lose its last line, not its header.
		if m.height > 0 {
			avail := m.height - strings.Count(b.String(), "\n") - footerLines
			if lines := strings.Split(body, "\n"); avail > 0 && len(lines) > avail {
				body = strings.Join(lines[:avail], "\n")
			}
		}
		b.WriteString(body + "\n")
	}
	if m.height > 0 {
		for i := strings.Count(b.String(), "\n") + footerLines; i < m.height; i++ {
			b.WriteString("\n")
		}
	}
	b.WriteString(m.frameRule(w) + "\n")
	if notice != "" {
		b.WriteString(notice + "\n")
	}
	b.WriteString(bar)
	return b.String()
}
