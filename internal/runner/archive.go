// Archiving plans and reports into Apple Pie's own home (~/.pie), so they survive
// the disposable worktree without ever being committed to the target repo.
// Best-effort throughout: a failure here never blocks shipping.
package runner

import (
	"encoding/json"
	"os"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// archiveReport persists a ticket's completion report into Apple Pie's own home
// (~/.pie/reports/<ticket>.json) so it survives outside the worktree and can
// later be surfaced by a report viewer - without ever being committed to the
// target repo. Best-effort: a failure here never blocks shipping.
func archiveReport(ticket string, r *agent.Report, logf func(string, ...interface{})) {
	if err := os.MkdirAll(paths.Reports(), 0o755); err != nil {
		logf("[%s] (warn) could not create reports dir: %v", ticket, err)
		return
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(paths.ReportFor(ticket), b, 0o644); err != nil {
		logf("[%s] (warn) could not archive report: %v", ticket, err)
	}
}

// archivePlan persists the plan into Apple Pie's own home right after planning:
// ~/.pie/plans/<ticket>.md (what the TUI's "View Plan" opens) plus the raw
// .json. The worktree's .agent/ scratch stays git-excluded and disposable, so
// the plan never lands in the target repo. Best-effort.
func archivePlan(ticket, summary string, p *agent.Plan, logf func(string, ...interface{})) {
	if err := os.MkdirAll(paths.Plans(), 0o755); err != nil {
		logf("[%s] (warn) could not create plans dir: %v", ticket, err)
		return
	}
	title := ticket
	if summary != "" {
		title += " · " + summary
	}
	if err := os.WriteFile(paths.PlanMDFor(ticket), []byte(agent.PlanMarkdown(title, p)), 0o644); err != nil {
		logf("[%s] (warn) could not archive plan: %v", ticket, err)
		return
	}
	if data, err := json.MarshalIndent(p, "", "  "); err == nil {
		_ = os.WriteFile(paths.PlanJSONFor(ticket), data, 0o644)
	}
}
