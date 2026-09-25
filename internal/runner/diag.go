// Per-run diagnostics: the facts a permission post-mortem needs, logged once
// where every flow passes. The field dead-end was undiagnosable from `pie
// logs` because nothing recorded which claude CLI ran or what allowlist was
// actually passed - the log said "permissions:allowlist" and nothing else.
package runner

import "github.com/Apple-Pie-AI/pie-tui/internal/agent"

// logRunDiagnostics records the claude CLI version and the effective
// allowlist (count + verbatim, one line each) at the top of every runner.Run
// - one call site covers fresh runs, resume, ship, and both comment flows,
// for the CLI and the daemon alike.
func logRunDiagnostics(t Task, logf logFn) {
	if t.Cfg == nil {
		return // test tasks; nothing meaningful to record
	}
	allowed := t.Cfg.EffectiveAllowedTools()
	logf("[%s] claude CLI: %s", t.Ticket, agent.CLIVersion())
	logf("[%s] allowlist: %d rule(s): %s", t.Ticket, agent.CountRules(allowed), allowed)
}
