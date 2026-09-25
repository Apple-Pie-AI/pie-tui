// The full-screen plan viewer behind the plan-review gate, and the inline
// feedback input that keeps the plan visible while the user types corrections.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// submitPlanFeedback appends the human's feedback to the .md source and re-runs
// with --review-plan so the agent re-plans and pauses again for another review.
// Same local-tickets-only limitation as submitAnswer (pasted tickets always have
// a SourcePath, so the primary flow is covered).
func (m monitorModel) submitPlanFeedback(key, feedback string) tea.Cmd {
	self, st := m.selfPath, m.store
	return func() tea.Msg {
		var sourcePath string
		if st != nil {
			if sess, err := st.Get(key); err == nil && sess != nil {
				sourcePath = sess.SourcePath
			}
		}
		if sourcePath == "" {
			return answerDoneMsg{ticket: key, err: fmt.Errorf("plan feedback via TUI only works for local .md tickets - use \"Open in Claude Code\" to give feedback in the session")}
		}
		raw, err := os.ReadFile(sourcePath)
		if err != nil {
			return answerDoneMsg{ticket: key, err: fmt.Errorf("read %s: %w", sourcePath, err)}
		}
		updated := strings.TrimSpace(string(raw)) + "\n\n---\nPlan feedback:\n" + feedback
		if err := os.WriteFile(sourcePath, []byte(updated+"\n"), 0o644); err != nil {
			return answerDoneMsg{ticket: key, err: fmt.Errorf("write %s: %w", sourcePath, err)}
		}
		c := exec.Command(self, "run", sourcePath, "--review-plan")
		if err := startDetached(c); err != nil {
			return answerDoneMsg{ticket: key, err: err}
		}
		return answerDoneMsg{ticket: key}
	}
}

// ---- telemetry consent -----------------------------------------------------

// planMarkdownDoc builds a markdown document from a session's plan.json for the
// full-screen viewer, falling back to the durable ~/.pie/plans/<ticket>.md
// archive when the worktree is gone. Returns ok=false when there's no plan yet.
func planMarkdownDoc(s store.Session) (string, bool) {
	if plan, err := agent.ReadPlan(paths.WorktreeFor(s.Repo, s.Ticket)); err == nil && plan != nil {
		title := s.Ticket
		if s.Summary != "" {
			title += " · " + s.Summary
		}
		return agent.PlanMarkdown(title, plan), true
	}
	if data, err := os.ReadFile(paths.PlanMDFor(s.Ticket)); err == nil {
		return string(data), true
	}
	return "", false
}

// planChrome is the number of non-plan rows the viewer draws around the plan
// window: app header (1), title (1), menu block (2), and the frame footer (2).
// When the feedback input is open it adds its block (separator + up to 2 rows).
func (m monitorModel) planChrome() int {
	c := 6
	if m.plan.feedback {
		c += 3
	}
	return c
}

// planViewportHeight is how many rows of the plan are visible.
func (m monitorModel) planViewportHeight() int {
	h := m.height - m.planChrome()
	if h < 3 {
		h = 3
	}
	return h
}

// openPlanView renders the session's plan to markdown and switches to the
// full-screen viewer. No-op (with a notice) when there's no plan to show.
func (m monitorModel) openPlanView(s store.Session) (tea.Model, tea.Cmd) {
	md, ok := planMarkdownDoc(s)
	if !ok {
		m.notice = "no plan to show yet for " + s.Ticket
		return m, nil
	}
	m.plan = planState{ticket: s.Ticket}
	m.plan.lines = renderMarkdownLines(md, m.paneWidth())
	m.view = viewPlan
	m.notice = ""
	return m, nil
}

// openPlanFeedback opens the viewer straight into the feedback input.
func (m monitorModel) openPlanFeedback(s store.Session) (tea.Model, tea.Cmd) {
	mm, cmd := m.openPlanView(s)
	hub := mm.(monitorModel)
	if hub.view == viewPlan { // only if a plan was actually found
		hub.plan.feedback = true
	}
	return hub, cmd
}

// paneWidth is the wrap width for the plan viewer (leave a small right margin).
func (m monitorModel) paneWidth() int {
	w := m.width
	if w == 0 {
		w = 80
	}
	return w - 2
}

// clampPlanScroll keeps the scroll offset within [0, max].
func (m *monitorModel) clampPlanScroll() {
	max := len(m.plan.lines) - m.planViewportHeight()
	if max < 0 {
		max = 0
	}
	if m.plan.scroll > max {
		m.plan.scroll = max
	}
	if m.plan.scroll < 0 {
		m.plan.scroll = 0
	}
}

// updatePlan handles keys in the full-screen plan viewer. Two modes: the top
// menu (←→ select · enter choose) and the inline feedback input (which keeps the
// plan visible above so the user can scroll it while writing corrections).
func (m monitorModel) updatePlan(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.plan.feedback {
		return m.updatePlanFeedback(msg)
	}
	viewH := m.planViewportHeight()
	switch msg.String() {
	case "esc", "q":
		m.view = viewDashboard
	case "left", "h", "right", "l", "tab":
		m.plan.menu = 1 - m.plan.menu // toggle between the two menu items
	case "up", "k":
		m.plan.scroll--
		m.clampPlanScroll()
	case "down", "j":
		m.plan.scroll++
		m.clampPlanScroll()
	case "pgup", "b":
		m.plan.scroll -= viewH
		m.clampPlanScroll()
	case "pgdown", " ":
		m.plan.scroll += viewH
		m.clampPlanScroll()
	case "home", "g":
		m.plan.scroll = 0
	case "end", "G":
		m.plan.scroll = len(m.plan.lines)
		m.clampPlanScroll()
	case "enter":
		if m.plan.menu == 1 { // Approve
			m.syncCursorTo(m.plan.ticket) // a background reload may have moved the row
			return m.doAction(actApprovePlan)
		}
		m.plan.feedback = true // Give feedback: open the inline input
		m.plan.fb.clear()
		m.clampPlanScroll()
	}
	return m, nil
}

// updatePlanFeedback edits the inline feedback text. Enter submits (re-plan with
// the correction), esc cancels back to the menu; up/down/PgUp/PgDn still scroll
// the plan so it stays reviewable while typing.
func (m monitorModel) updatePlanFeedback(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		m.plan.fb.paste(string(msg.Runes))
		return m, nil
	}
	viewH := m.planViewportHeight()
	// The scroll bindings come first: this screen spends ↑/↓/PgUp/PgDn on keeping
	// the plan readable while you type, so they must not reach the text input.
	switch msg.Type {
	case tea.KeyEnter:
		full := strings.TrimSpace(m.plan.fb.text())
		if full == "" { // empty submit cancels back to the menu
			m.plan.feedback = false
			return m, nil
		}
		key := m.plan.ticket
		m.plan.feedback = false
		m.plan.fb.clear()
		m.view = viewDashboard
		m.notice = "sending plan feedback to " + key + "…"
		m.syncCursorTo(key)
		return m, m.submitPlanFeedback(key, full)
	case tea.KeyEsc:
		m.plan.feedback = false // back to the menu, plan stays open
		m.plan.fb.clear()
	case tea.KeyUp:
		m.plan.scroll--
		m.clampPlanScroll()
	case tea.KeyDown:
		m.plan.scroll++
		m.clampPlanScroll()
	case tea.KeyPgUp:
		m.plan.scroll -= viewH
		m.clampPlanScroll()
	case tea.KeyPgDown:
		m.plan.scroll += viewH
		m.clampPlanScroll()
	default:
		m.plan.fb.key(msg)
	}
	return m, nil
}

// renderPlan is the full-screen plan viewer: a top action menu, a scrollable
// window over the glamour-rendered plan, and (when active) an inline feedback
// input below the plan.
func (m monitorModel) renderPlan(w int) string {
	var b strings.Builder

	// Title + scroll position.
	viewH := m.planViewportHeight()
	pct := ""
	if len(m.plan.lines) > viewH {
		bottom := m.plan.scroll + viewH
		if bottom > len(m.plan.lines) {
			bottom = len(m.plan.lines)
		}
		pct = fmt.Sprintf("  %d–%d of %d", m.plan.scroll+1, bottom, len(m.plan.lines))
	}
	b.WriteString(headerStyle.Render(truncate("  Plan · "+m.plan.ticket, w)) + dimStyle.Render(pct) + "\n")

	// Action menu: two buttons. The selected one is a bright teal button; the
	// other is muted. While typing feedback the input has focus, so highlight the
	// "Give feedback" button as active.
	items := []string{"Give feedback", "Approve"}
	active := m.plan.menu
	if m.plan.feedback {
		active = 0
	}
	var menu strings.Builder
	menu.WriteString("  ")
	for i, it := range items {
		if i == active {
			menu.WriteString(planBtnSel.Render("▸ "+it) + "  ")
		} else {
			menu.WriteString(planBtn.Render(it) + "  ")
		}
	}
	b.WriteString(menu.String() + "\n\n")

	// Plan window.
	start := m.plan.scroll
	if start > len(m.plan.lines)-viewH {
		start = len(m.plan.lines) - viewH
	}
	if start < 0 {
		start = 0
	}
	end := start + viewH
	if end > len(m.plan.lines) {
		end = len(m.plan.lines)
	}
	for _, ln := range m.plan.lines[start:end] {
		b.WriteString(ln + "\n")
	}

	// Inline feedback input, below the plan.
	if m.plan.feedback {
		b.WriteString(sepStyle.Render(strings.Repeat("─", w)) + "\n")
		const prefix = "  feedback › "
		segs := wrapLine(m.plan.fb.render(), w-len([]rune(prefix)))
		if len(segs) == 0 {
			b.WriteString(prefix + "\n")
		} else {
			for i, seg := range segs {
				if i == 0 {
					b.WriteString(prefix + seg + "\n")
				} else {
					b.WriteString("    " + seg + "\n")
				}
			}
		}
	}
	return b.String()
}
