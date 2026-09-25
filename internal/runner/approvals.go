// Wiring the permission callback into agent spawns. With it, an ask rule or
// allowlist gap pauses for a dashboard decision (or auto-approves when the
// command already matches pie's allowlist) instead of hard-denying - the
// PLEX-61263 class of park. Allowlist-mode stages only: auto mode keeps its
// classifier semantics, bypass has nothing to ask.
package runner

import "github.com/Apple-Pie-AI/pie-tui/internal/agent"

// approvalCallbackConfig writes the session's mcp-config and returns its path,
// or "" when the mode doesn't take the callback or the write fails (the run
// then behaves exactly as before the callback existed).
func approvalCallbackConfig(t Task, worktree, mode string, logf logFn) string {
	if mode == "auto" || mode == "bypass" {
		return ""
	}
	path, err := agent.WriteApprovalMCPConfig(worktree, t.Ticket)
	if err != nil {
		logf("[%s] (warn) permission callback unavailable - denials will hard-fail: %v", t.Ticket, err)
		return ""
	}
	return path
}
