// The "start new ticket(s)" wizard: picking an input method, accumulating ticket
// chips, and launching `pie run` for the batch. The per-step editors live in the
// tui_run_*.go siblings.
package tui

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

type runMethod struct{ mode, label, desc string }

// runMethods lists the ways to add tickets. Jira appears only when configured.
func (m monitorModel) runMethods() []runMethod {
	ms := []runMethod{{"content", "Write or paste the ticket content", "a markdown editor - then confirm its id + branch"}}
	if m.jira != nil {
		ms = append(ms, runMethod{"jira", "From Jira", "enter Jira ticket key(s)"})
	}
	return ms
}

func (m monitorModel) updateRunMethod(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ms := m.runMethods()
	switch msg.String() {
	case "esc", "q":
		m.view = viewDashboard
	case "up", "k":
		if m.run.methodCursor > 0 {
			m.run.methodCursor--
		}
	case "down", "j":
		if m.run.methodCursor < len(ms)-1 {
			m.run.methodCursor++
		}
	case "enter":
		if m.run.methodCursor < len(ms) {
			// Keep the picker cursor, clear everything else: this used to reset
			// five of the run wizard's twenty fields by hand and leave the stack
			// and plan-review prompts armed from a previous pass.
			mode, cursor := ms[m.run.methodCursor].mode, m.run.methodCursor
			m.run.reset()
			m.run.mode, m.run.methodCursor = mode, cursor
			m.view = viewRunInput
			if m.run.mode == "content" {
				m = m.withContentEditor()
				return m, textarea.Blink
			}
		}
	}
	return m, nil
}

func (m monitorModel) renderRunMethod(w int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Start new ticket(s)") + "\n")
	b.WriteString(dimStyle.Render("  how do you want to add tickets?") + "\n\n")
	for i, it := range m.runMethods() {
		marker, label := "   ", it.label
		if i == m.run.methodCursor {
			marker, label = " ▸ ", selStyle.Render(" "+it.label+" ")
		}
		b.WriteString(truncate(marker+label+"  "+dimStyle.Render(it.desc), w) + "\n")
	}
	b.WriteString("\n  " + dimStyle.Render("↑↓ select · enter: choose · esc: cancel"))
	return b.String()
}

// ---- run-new: input (per method) ------------------------------------------

func (m monitorModel) updateRunInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.run.idPrompt >= 0 {
		return m.updateRunIDPrompt(msg)
	}
	if m.run.branchFirstPick {
		return m.updateRunBranchPick(msg)
	}
	if m.run.branchPrompt >= 0 {
		return m.updateRunBranchPrompt(msg)
	}
	if m.run.imgPrompt >= 0 {
		return m.updateRunImgAttach(msg)
	}
	if m.run.stackPrompt >= 0 {
		return m.updateRunStack(msg)
	}
	if m.run.reviewPrompt >= 0 {
		return m.updateRunReview(msg)
	}
	switch m.run.mode {
	case "content":
		return m.updateRunContent(msg)
	case "jira":
		return m.updateRunJira(msg)
	}
	m.view = viewRunMethod // no mode set → back to the picker
	return m, nil
}

func (m monitorModel) cancelRunInput() monitorModel {
	m.view = viewDashboard
	m.run.reset()
	return m
}

func (m monitorModel) launchOrClose() (tea.Model, tea.Cmd) {
	if len(m.run.tickets) == 0 {
		return m.cancelRunInput(), nil
	}
	// Validate the repo at dispatch - not at wizard entry, which would block
	// composing a ticket the repo has nothing to do with. A broken repo path
	// can't produce a run, so bounce to the repo-fix screen (suggest a local
	// checkout, clone from a URL, or edit the path) rather than spawn a run that
	// dies at worktree creation. The composed tickets are left intact so nothing
	// typed is lost.
	if err := m.preflightRepo(); err != nil {
		m.notice = "can't launch: " + err.Error()
		m.openRepoFix(err.Error())
		return m, nil
	}
	tickets := m.run.tickets
	m = m.cancelRunInput()
	return m, m.launchRun(tickets)
}

// chipLabel renders a pending ticket chip label (shared by renderRunInput and renderRunStack).
func chipLabel(t pendingTicket) string {
	if t.kind == "jira" {
		lbl := fmt.Sprintf("[%s]", t.id)
		if t.base != "" {
			lbl = fmt.Sprintf("[%s ⤷ %s]", t.id, t.base)
		}
		return lbl
	}
	// Content chips lead with the confirmed branch name (the user-facing identity
	// since the branch prompt replaced the id prompt); fall back to the id.
	head := t.branch
	if head == "" {
		head = t.id
	}
	if head == "" {
		head = "?"
	}
	suffix := ticket.ImgSuffix(len(t.images))
	if t.base != "" {
		suffix += " ⤷ " + t.base
	}
	return fmt.Sprintf("[%s · %s · %d line%s%s]",
		head, truncate(ticket.StripIDPrefix(t.title, t.id), 40), t.lines, ticket.Plural(t.lines), suffix)
}

func (m monitorModel) renderRunInput(w int) string {
	// branchFirstPick is the dashboard's branch-first entry, which has no
	// chip yet to key off of.
	if m.run.branchFirstPick {
		return m.renderRunBranchPick(w)
	}
	if m.run.stackPrompt >= 0 {
		return m.renderRunStack(w)
	}
	if m.run.reviewPrompt >= 0 {
		return m.renderRunReview(w)
	}
	var b strings.Builder
	chips := func() {
		for i, t := range m.run.tickets {
			label := chipLabel(t)
			if i == m.run.branchPrompt || i == m.run.imgPrompt {
				label = selStyle.Render(" " + label + " ")
			}
			b.WriteString("  " + label + "\n")
		}
		if len(m.run.tickets) > 0 {
			b.WriteString("\n")
		}
	}

	if m.run.idPrompt >= 0 && m.run.idPrompt < len(m.run.tickets) {
		t := m.run.tickets[m.run.idPrompt]
		return m.renderTicketPrompt(t.body,
			"Confirm the ticket id",
			"ticket id › "+formField{value: t.id, cursor: m.run.promptCursor}.caretView(), w)
	}
	if m.run.branchPrompt >= 0 && m.run.branchPrompt < len(m.run.tickets) {
		t := m.run.tickets[m.run.branchPrompt]
		return m.renderTicketPrompt(t.body,
			"Update or Approve the branch name",
			"branch › "+formField{value: t.branch, cursor: m.run.promptCursor}.caretView(), w)
	}
	if m.run.imgPrompt >= 0 && m.run.imgPrompt < len(m.run.tickets) {
		n := len(m.run.tickets[m.run.imgPrompt].images)
		chips()
		b.WriteString(renderBodyPreview(m.run.tickets[m.run.imgPrompt].body, w))
		b.WriteString(headerStyle.Render("  Attach images (optional)") + "\n")
		b.WriteString(dimStyle.Render("  drag image files into the terminal, then enter") + "\n\n")
		b.WriteString("  › " + formField{value: m.run.text, cursor: m.run.promptCursor}.caretView() + "\n")
		b.WriteString("\n  " + dimStyle.Render(fmt.Sprintf("%d attached · ←→ move · enter done · esc skip", n)))
		return b.String()
	}

	switch m.run.mode {
	case "jira":
		b.WriteString(headerStyle.Render("  From Jira") + "\n")
		b.WriteString(dimStyle.Render("  type/paste Jira key(s); space adds more") + "\n\n")
		chips()
		b.WriteString("  › " + m.run.text + "▌\n")
		b.WriteString("\n  " + dimStyle.Render("space add · ctrl+s stack · enter launch · esc cancel"))
	default: // content
		b.WriteString(headerStyle.Render("  Write or paste the ticket content") + "\n\n")
		chips()
		switch {
		case m.run.preview:
			viewH := m.ticketViewportHeight()
			start, end := scrollWindow(len(m.run.ticketLines), m.run.ticketScroll, viewH)
			for _, ln := range m.run.ticketLines[start:end] {
				b.WriteString(ln + "\n")
			}
			hint := "ctrl+p edit · ctrl+d confirm ticket · esc back to editing"
			if len(m.run.ticketLines) > viewH {
				hint = fmt.Sprintf("%d–%d of %d · ↑↓ scroll · %s", start+1, end, len(m.run.ticketLines), hint)
			}
			b.WriteString("\n  " + dimStyle.Render(hint))
		case m.run.content != nil:
			// Reserved row so the editor doesn't jump when focus moves to the bar.
			escHint := ""
			if !m.run.bar {
				escHint = headerStyle.Render("  Press ESC to leave the edit mode")
			}
			b.WriteString(escHint + "\n")
			b.WriteString(m.run.content.View() + "\n\n")
			// Action bar: always visible below the editor viewport (the editor
			// scrolls internally, so long content never pushes it off-screen).
			buttons := []string{"Continue to next step", "Keep editing", "Discard"}
			if strings.TrimSpace(m.run.content.Value()) == "" && len(m.run.tickets) > 0 {
				buttons[0] = "Launch" // empty editor + queued chips: Continue launches them
			}
			var bar strings.Builder
			bar.WriteString("  ")
			for i, it := range buttons {
				if m.run.bar && i == m.run.btn {
					bar.WriteString(planBtnSel.Render("▸ "+it) + "  ")
				} else {
					bar.WriteString(planBtn.Render(it) + "  ")
				}
			}
			b.WriteString(bar.String() + "\n")
			hint := "enter new line · esc: actions · ctrl+p preview · ctrl+d quick-confirm"
			if m.run.bar {
				hint = "←→ choose · enter select · esc/tab back to editing"
			}
			b.WriteString("\n  " + dimStyle.Render(hint))
		}
	}
	return b.String()
}

// chipRunFlags maps one chip's choices onto `pie run` flags. Split out so the
// flag shape (--from-branch vs --branch in particular) is pinned by a test
// without exec'ing anything.
func chipRunFlags(t pendingTicket) []string {
	var flags []string
	if t.base != "" {
		flags = append(flags, "--base", t.base)
	}
	if t.branch != "" {
		flags = append(flags, "--branch", t.branch)
	}
	// reviewPlanSet means the picker was actually answered (only content
	// tickets reach it) - pass the choice explicitly either way, "false"
	// included, so a deliberate "No" can override an enabled config default
	// instead of silently omitting the flag and falling back to it. A chip
	// that never asked (e.g. jira) passes nothing, deferring to config as before.
	if t.reviewPlanSet {
		if t.reviewPlan {
			flags = append(flags, "--review-plan")
		} else {
			flags = append(flags, "--review-plan=false")
		}
	}
	return flags
}

// launchRun spawns `pie run <args…>` detached. When any ticket has a stack
// base set, each ticket gets its own subprocess (so --base can be per-ticket);
// otherwise all tickets share one batched call.
func (m monitorModel) launchRun(tickets []pendingTicket) tea.Cmd {
	self := m.selfPath
	return func() tea.Msg {
		// A per-ticket base, plan-review answer, or branch name forces the
		// per-ticket subprocess path (batching one `run` call can't carry
		// per-chip flags) - reviewPlanSet, not just reviewPlan, because an
		// explicit "No" needs its own --review-plan=false just as much as a
		// "Yes" needs --review-plan.
		anyStacked := false
		for _, t := range tickets {
			if t.base != "" || t.reviewPlanSet || t.branch != "" {
				anyStacked = true
				break
			}
		}

		ticketArg := func(t pendingTicket) (string, error) {
			switch t.kind {
			case "content":
				return ticket.WritePasted(t.id, t.body, t.images)
			default:
				return t.id, nil
			}
		}

		if anyStacked {
			// One subprocess per ticket so --base can differ per chip.
			for _, t := range tickets {
				arg, err := ticketArg(t)
				if err != nil {
					return runLaunchedMsg{err: err}
				}
				c := exec.Command(self, append([]string{"run", arg}, chipRunFlags(t)...)...)
				if err := startDetached(c); err != nil {
					return runLaunchedMsg{err: err}
				}
			}
			return runLaunchedMsg{text: fmt.Sprintf("%d ticket(s)", len(tickets))}
		}

		// Batch: no stacking, one process.
		args := []string{"run"}
		for _, t := range tickets {
			arg, err := ticketArg(t)
			if err != nil {
				return runLaunchedMsg{err: err}
			}
			args = append(args, arg)
		}
		if len(args) == 1 {
			return runLaunchedMsg{err: fmt.Errorf("no tickets to launch")}
		}
		c := exec.Command(self, args...)
		return runLaunchedMsg{text: fmt.Sprintf("%d ticket(s)", len(tickets)), err: startDetached(c)}
	}
}

// spawnRun launches `pie run <args…>` detached with pre-split args (no
// whitespace splitting, so paths with spaces survive). Used for resume/ship/rerun
// of an existing ticket, where args are a ticket key/path plus optional flags.
func (m monitorModel) spawnRun(args ...string) tea.Cmd {
	if m.demo != nil {
		return func() tea.Msg { return runLaunchedMsg{err: fmt.Errorf("not available in the demo")} }
	}
	self := m.selfPath
	full := append([]string{"run"}, args...)
	return func() tea.Msg {
		c := exec.Command(self, full...)
		return runLaunchedMsg{text: strings.Join(args, " "), err: startDetached(c)}
	}
}
