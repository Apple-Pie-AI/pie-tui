// The two full-screen modals that pre-empt whatever view is active: the stop
// confirmation and the first-run telemetry consent prompt.
package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// updateConfirming handles the palette-style stop-confirmation prompt.
// ↑/↓ moves the cursor; Enter selects; y/n are kept for backwards compat.
func (m monitorModel) updateConfirming(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	confirmYes := func() (tea.Model, tea.Cmd) {
		key := m.confirm.ticket
		m.confirm.reset()
		var target *store.Session
		for i := range m.flat {
			if m.flat[i].Ticket == key {
				target = &m.flat[i]
				break
			}
		}
		if target == nil {
			return m, nil
		}
		m.notice = "stopping " + key + "…"
		return m, m.cancelAgent(*target)
	}
	switch msg.String() {
	case "up", "k":
		m.confirm.cursor = 0
	case "down", "j":
		m.confirm.cursor = 1
	case "enter":
		if m.confirm.cursor == 0 {
			return confirmYes()
		}
		m.confirm.reset()
	case "y", "Y":
		return confirmYes()
	case "n", "N", "esc", "q":
		m.confirm.reset()
	}
	return m, nil
}

// renderConfirm renders the palette-style stop-confirmation view.
func (m monitorModel) renderConfirm(w int) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  Stop "+m.confirm.ticket+"?") + "\n")
	b.WriteString(dimStyle.Render("  kills the process and removes the worktree") + "\n\n")
	items := []string{"Yes, stop and clean up", "No, keep running"}
	for i, item := range items {
		if i == m.confirm.cursor {
			b.WriteString(selStyle.Render(" "+item) + "\n")
		} else {
			b.WriteString("  " + item + "\n")
		}
	}
	return b.String()
}

// killGrace is how long a signalled run gets to unwind - cancel its agent,
// release the emulator lease, close its log - before it is SIGKILLed. Generous
// because the thing being waited on is a claude process shutting down, and cheap
// because both callers run as a tea.Cmd off the render loop.
const killGrace = 10 * time.Second

// stopRun signals a session's driver process, but only when there is one to
// signal: the state must still claim to be running AND the recorded pid must
// answer. That is the same rule cmd/run.go uses to refuse a second run, and it
// matters more here than there - "Stop & clean up" is offered on needs-you,
// failed and stopped rows, where the driver exited long ago and macOS may well
// have recycled its pid onto something unrelated. Signalling a whole process
// group and then SIGKILLing it is not something to do on a guess.
func stopRun(s store.Session) {
	if store.IsActive(s.State) && proc.Alive(s.PID) {
		proc.TerminateGroup(s.PID, killGrace)
	}
}

// pauseAgent halts a live run: it stops the driving process (and everything it
// spawned) but KEEPS the worktree, and drops the session to NEEDS YOU so the
// human can take over by hand and later resume.
func (m monitorModel) pauseAgent(s store.Session) tea.Cmd {
	st := m.store
	return func() tea.Msg {
		// State first, signal second. The runner deliberately writes no lifecycle
		// state on the cancellation path so that this value is the one that
		// survives; writing it after the kill would leave a window in which the
		// row still claims to be working.
		_ = st.SetState(s.Ticket, store.StateNeedsYou, s.Retries)
		stopRun(s)
		// The kill takes the approval server with it (same process group), so
		// any prompt it was holding must be expired here - a pending question
		// with no agent behind it would sit on the dashboard forever.
		_ = st.ExpirePendingApprovals(s.Ticket)
		return pauseDoneMsg{ticket: s.Ticket}
	}
}

// cancelAgent stops the driving process, removes the worktree, and marks the
// session stopped so it leaves NEEDS YOU for STOPPED.
func (m monitorModel) cancelAgent(s store.Session) tea.Cmd {
	st := m.store
	return func() tea.Msg {
		_ = st.SetState(s.Ticket, store.StateStopped, s.Retries)
		// Wait for the run to actually be gone before touching the worktree. The
		// agent is editing files in there and now unwinds gracefully instead of
		// dying on the spot, so removing the directory on the same tick raced it.
		stopRun(s)
		_ = st.ExpirePendingApprovals(s.Ticket) // no orphaned prompts (see pauseAgent)
		wt := s.Worktree
		if wt == "" {
			wt = paths.WorktreeFor(s.Repo, s.Ticket)
		}
		if s.Repo != "" {
			_ = git.RemoveWorktree(s.Repo, wt)
		}
		return cancelDoneMsg{ticket: s.Ticket}
	}
}

func (m monitorModel) updateConsent(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "y":
		return m, func() tea.Msg { return applyConsent(true) }
	case "n", "esc", "q":
		return m, func() tea.Msg { return applyConsent(false) }
	}
	return m, nil
}

func applyConsent(enabled bool) tea.Msg {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}
	cfg.TelemetryEnabled = &enabled
	if enabled && cfg.DeviceID == "" {
		cfg.DeviceID = config.GenerateDeviceID()
	}
	_ = config.Save(cfg)
	return consentDoneMsg{enabled: enabled, deviceID: cfg.DeviceID}
}

func (m monitorModel) renderConsent(w int) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  Help improve Apple Pie?") + "\n\n")
	b.WriteString("  Share anonymous usage data so we can understand how the tool is used.\n")
	b.WriteString("  We never collect ticket content, file paths, or personal information.\n\n")
	b.WriteString("  What gets shared:\n")
	b.WriteString(dimStyle.Render("    • which commands run (run, doctor, etc.)") + "\n")
	b.WriteString(dimStyle.Render("    • run outcome (success / failed / needs-human)") + "\n")
	b.WriteString(dimStyle.Render("    • OS type and Apple Pie version") + "\n\n")
	b.WriteString("  " + ctaStyle.Render(" Y ") + "  yes - help make Apple Pie better\n\n")
	b.WriteString("  " + dimStyle.Render("N") + "  no thanks\n")
	return b.String()
}

// ---- view ------------------------------------------------------------------
