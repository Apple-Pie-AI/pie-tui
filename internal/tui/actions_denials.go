// Denial recovery: the "Allow denied commands & re-run" action and the
// flow-aware retry that backs it (and Resume). Split from actions.go - one
// concern, and that file was at the size guideline.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// allowRerunAvailable reports whether "Allow denied commands & re-run" makes
// sense for this session: the last run recorded denials AND at least one
// allowlist rule would actually change the outcome. The suggester escalates
// to exact-command rules when the generalized heads are already present, so
// this only goes false once the ladder is exhausted - a denial no rule can
// express (guarded, prose, or a settings-layer ask/deny) must not offer an
// action that writes nothing and re-runs identically.
func allowRerunAvailable(s store.Session) bool {
	denials := agent.UnmarshalDenials(s.Denials)
	if len(denials) == 0 {
		return false
	}
	cfg, err := config.Load()
	if err != nil {
		return false
	}
	return len(agent.SuggestAllowRules(denials, cfg.EffectiveAllowedTools())) > 0
}

// retryArgs is how a parked ticket re-enters the pipeline: the flow that
// parked it. A ship-comments park must retry as ship-comments (the bare
// resume re-verified the worktree but never shipped the approved replies) and
// an address-comments park must re-run the fix; only main-pipeline parks
// resume.
func retryArgs(s store.Session) []string {
	switch s.ParkedFlow {
	case "ship-comments":
		return []string{runArg(s), "--ship-comments"}
	case "address-comments":
		return []string{runArg(s), "--address-comments"}
	case "ship-change":
		// A red approve-verify or failed ship from the change-review gate:
		// the approval stands, retry the gated ship - a bare --resume would
		// ship without the gate.
		return []string{runArg(s), "--ship-change"}
	case "rework-change":
		// The notes live in the store until a rework round consumes them, so
		// a dead rework retries as itself and replays them.
		return []string{runArg(s), "--rework"}
	default:
		return []string{runArg(s), "--resume"}
	}
}

// doAllowRerun appends the suggested rules to extra_allowed_tools (the
// append-only key, so the grant survives default upgrades and applies to every
// future run), clears the stored denials, and retries the flow that parked.
func (m monitorModel) doAllowRerun(s store.Session) (tea.Model, tea.Cmd) {
	denials := agent.UnmarshalDenials(s.Denials)
	cfg, err := config.Load()
	if err != nil {
		m.notice = "config: " + err.Error()
		return m, nil
	}
	rules := agent.SuggestAllowRules(denials, cfg.EffectiveAllowedTools())
	if len(rules) == 0 {
		m.notice = "denied commands already match the allowlist - see pie logs " + s.Ticket
		return m, nil
	}
	cfg.ExtraAllowedTools = strings.TrimSpace(cfg.ExtraAllowedTools + " " + strings.Join(rules, " "))
	if err := config.Save(cfg); err != nil {
		m.notice = "save config: " + err.Error()
		return m, nil
	}
	_ = m.store.SetDenials(s.Ticket, "", false)
	m.notice = fmt.Sprintf("allowed %d command rule(s) - retrying %s…", len(rules), s.Ticket)
	return m, m.spawnRun(retryArgs(s)...)
}
