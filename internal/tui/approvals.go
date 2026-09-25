// The in-TUI permission approval flow. While an agent session is paused on a
// command its allowlist doesn't cover (or an ask rule in the user's Claude
// settings requires a human), pie's mcp-approve callback holds a pending row
// in the store and this overlay is where the human answers it - allow once,
// allow and remember the rule, or deny - without leaving the dashboard. The
// agent continues in the same session the moment the row is decided.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// approvalOverlay is the pending-approval prompt. open guards it; ticket
// scopes it to one session ("" = whatever is oldest across all sessions).
type approvalOverlay struct {
	open   bool
	ticket string
	cursor int
}

func (a *approvalOverlay) reset() { *a = approvalOverlay{} }

// reapGhostApprovals expires pending approvals that no live agent is waiting
// behind, and returns only the answerable ones. A prompt's driver can die
// without running any cleanup hook - a group-kill from an old binary, a
// reboot, a crash of the whole tree - and field QA hit exactly that: prompts
// from the previous day survived their agents overnight, the user answered
// them, and nothing happened. The dashboard is the one place guaranteed to
// look at every prompt before a human acts on it, so it reaps here: a ticket
// whose session is not in an active state, or whose recorded driver pid is
// no longer alive, cannot have an agent polling for the answer.
func reapGhostApprovals(st *store.Store, sessions []store.Session, pending []store.Approval) []store.Approval {
	if len(pending) == 0 {
		return pending
	}
	live := map[string]bool{}
	for _, s := range sessions {
		if store.IsActive(s.State) && proc.Alive(s.PID) {
			live[s.Ticket] = true
		}
	}
	var kept []store.Approval
	reaped := map[string]bool{}
	for _, a := range pending {
		if live[a.Ticket] {
			kept = append(kept, a)
			continue
		}
		if !reaped[a.Ticket] {
			_ = st.ExpirePendingApprovals(a.Ticket)
			reaped[a.Ticket] = true
		}
	}
	return kept
}

// approvalsFor counts pending approvals for one ticket.
func (m *monitorModel) approvalsFor(ticket string) int {
	n := 0
	for _, a := range m.pendingApprovals {
		if a.Ticket == ticket {
			n++
		}
	}
	return n
}

// currentApproval is the oldest pending approval in the overlay's scope.
func (m *monitorModel) currentApproval() *store.Approval {
	for i := range m.pendingApprovals {
		if m.approval.ticket == "" || m.pendingApprovals[i].Ticket == m.approval.ticket {
			return &m.pendingApprovals[i]
		}
	}
	return nil
}

// openApprovals opens the overlay scoped to ticket ("" = all).
func (m *monitorModel) openApprovals(ticket string) {
	m.approval = approvalOverlay{open: true, ticket: ticket}
}

// approvalRow is one choice on the overlay. state/rules is what pressing
// Enter on this row writes: the exact rules shown in label, never a
// re-derived broader or narrower set - what the human saw is what gets
// written. rules == nil means allow/deny once, no config write.
type approvalRow struct {
	label string
	state string
	rules []string
}

// approvalRows builds the overlay's choices for one pending approval.
// RememberOptions can offer either scope, both, or neither - only the
// non-empty ones become rows, so a guarded or prose-shaped command never
// shows a remember option that would silently degrade to allow-once.
func approvalRows(a store.Approval) []approvalRow {
	rows := []approvalRow{{label: "Allow once", state: store.ApprovalAllowed}}
	exact, general := agent.RememberOptions(a.Tool, a.Command)
	if exact != "" {
		rows = append(rows, approvalRow{
			label: "Allow & remember exact → " + exact,
			state: store.ApprovalAllowed, rules: []string{exact},
		})
	}
	if len(general) > 0 {
		rows = append(rows, approvalRow{
			label: "Allow & remember all → " + strings.Join(general, " "),
			state: store.ApprovalAllowed, rules: general,
		})
	}
	rows = append(rows, approvalRow{label: "Deny", state: store.ApprovalDenied})
	return rows
}

func (m monitorModel) updateApprovals(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	a := m.currentApproval()
	if a == nil {
		m.approval.reset()
		return m, nil
	}
	rows := approvalRows(*a)
	switch msg.String() {
	case "up", "k":
		if m.approval.cursor > 0 {
			m.approval.cursor--
		}
	case "down", "j":
		if m.approval.cursor < len(rows)-1 {
			m.approval.cursor++
		}
	case "enter":
		row := rows[m.approval.cursor]
		m.decideApproval(*a, row.state, row.rules)
		m.reload()
		if m.currentApproval() == nil {
			m.approval.reset()
		} else {
			m.approval.cursor = 0 // the next approval's row count may differ
		}
	case "esc", "q":
		// Leaves the request pending: the callback keeps waiting for a human
		// verdict, and the banner keeps pointing back here.
		m.approval.reset()
	}
	return m, nil
}

// decideApproval writes the verdict. rules, when non-empty, is written
// VERBATIM to extra_allowed_tools - it is exactly what the chosen overlay
// row displayed, never re-derived, so there is no gap between what the
// human consented to and what gets persisted.
func (m *monitorModel) decideApproval(a store.Approval, state string, rules []string) {
	remember := len(rules) > 0
	if remember {
		if cfg, err := config.Load(); err == nil {
			cfg.ExtraAllowedTools = strings.TrimSpace(cfg.ExtraAllowedTools + " " + strings.Join(rules, " "))
			if err := config.Save(cfg); err == nil {
				m.notice = "allowed & remembered: " + strings.Join(rules, " ")
			} else {
				m.notice = "allowed once (config save failed: " + err.Error() + ")"
			}
		} else {
			m.notice = "allowed once (config load failed: " + err.Error() + ")"
		}
	} else if state == store.ApprovalAllowed {
		m.notice = "allowed once: " + oneLineCmd(a.Command, 60)
	} else {
		m.notice = "denied: " + oneLineCmd(a.Command, 60)
	}
	if err := m.store.DecideApproval(a.ID, state, remember); err != nil {
		m.notice = "could not record the decision: " + err.Error()
	}
}

// renderApprovals draws the overlay: the waiting command verbatim, then the
// choices approvalRows built for it, in the same mini-palette shape the stop
// confirmation uses.
func (m monitorModel) renderApprovals(w int) string {
	a := m.currentApproval()
	if a == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %s wants to run a command", a.Ticket)) + "\n")
	b.WriteString(dimStyle.Render("  the agent is paused on this until you decide - it will wait for you") + "\n\n")
	for _, line := range wrapCommand(a.Tool, a.Command, w-6) {
		b.WriteString("    " + cmdApprovalStyle.Render(line) + "\n")
	}
	b.WriteString("\n")
	rowW := w - 4
	for i, row := range approvalRows(*a) {
		label := truncate(row.label, rowW)
		if i == m.approval.cursor {
			b.WriteString(selStyle.Render(" "+label) + "\n")
		} else {
			b.WriteString("  " + label + "\n")
		}
	}
	if rest := len(m.pendingApprovals) - 1; rest > 0 {
		b.WriteString("\n" + dimStyle.Render(fmt.Sprintf("  %d more waiting after this one", rest)) + "\n")
	}
	return b.String()
}

// wrapCommand hard-wraps a command for display without altering it - the human
// must judge the exact string the agent asked for, not an elided one.
func wrapCommand(tool, cmd string, width int) []string {
	if width < 20 {
		width = 20
	}
	full := tool + ": " + cmd
	var out []string
	for len(full) > width {
		out = append(out, full[:width])
		full = full[width:]
	}
	return append(out, full)
}

// oneLineCmd compresses a command for the one-line notice.
func oneLineCmd(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
