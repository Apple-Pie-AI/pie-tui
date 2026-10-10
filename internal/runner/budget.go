// Asking before giving up on a stage that hit its budget. A session that
// reaches --max-budget-usd used to end the stage outright - and, because the
// CLI's budget result carries no result text, the park blamed the ticket or
// the build. Now the run pauses with a question in the dashboard, through the
// same approvals table the permission callback uses: Continue resumes the
// SAME session with a fresh budget (verified live: --resume starts a new
// budget and keeps the context), Stop parks the ticket naming the budget.
//
// The question has no timeout, like a permission prompt: it waits for a human
// until the run is stopped (ctx cancelled), which expires it.
package runner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// budgetPoll is how often a pending budget question re-reads the store. A var
// only so tests can shorten it.
var budgetPoll = 500 * time.Millisecond

// budgetContinuePrompt is the turn a resumed session receives after the human
// grants more budget. It must restate that the original task is still the job:
// the session's last turn ended mid-work, and an agent that reads "continue"
// as "summarize" would end the stage without its contract file.
const budgetContinuePrompt = "You were stopped because this session reached its spending limit. " +
	"The user has approved more budget. Continue the task exactly where you left off and finish it, " +
	"including writing any .agent/ file the task asks for. Do not start over."

// runStage is agent.Run for a pipeline stage, plus the budget question: when
// the session stops on its budget and there is a dashboard to ask through, it
// waits for the human and, on Continue, resumes the session with the same
// budget again - as many rounds as the human keeps approving. On Stop (or with
// no store, or no session to resume) it returns the budget-stopped result,
// whose ErrorText the stage turns into a budget park.
func runStage(ctx context.Context, t Task, stage, prompt string, opts agent.Options, logf logFn) (agent.Result, error) {
	spent := 0.0
	for {
		res, err := agent.Run(ctx, prompt, opts)
		spent += res.CostUSD
		if !res.BudgetExceeded || ctx.Err() != nil {
			return res, err
		}
		logf("[%s] ⏸ %s stage reached its %s budget (%s spent this stage)",
			t.Ticket, stage, usd(opts.MaxBudgetUSD), usd(spent))
		if t.Store == nil || res.SessionID == "" {
			return res, err
		}
		if !askToContinue(ctx, t, stage, opts.MaxBudgetUSD, spent, logf) {
			return res, err
		}
		opts.ResumeID = res.SessionID
		prompt = budgetContinuePrompt
	}
}

// askToContinue puts the budget question in the dashboard and blocks until the
// human answers it or the run is stopped. True means "continue".
func askToContinue(ctx context.Context, t Task, stage string, budget, spent float64, logf logFn) bool {
	id, err := t.Store.CreateApproval(t.Ticket, agent.BudgetTool, budgetQuestion(stage, budget, spent))
	if err != nil {
		logf("[%s] (warn) could not ask about the budget in the dashboard: %v", t.Ticket, err)
		return false
	}
	logf("[%s] ⏸ budget: waiting for your decision in the pie dashboard", t.Ticket)
	for {
		select {
		case <-ctx.Done():
			_ = t.Store.DecideApproval(id, store.ApprovalExpired, false)
			return false
		case <-time.After(budgetPoll):
		}
		state, err := t.Store.ApprovalState(id)
		switch {
		case err != nil:
			logf("[%s] (warn) lost the budget question: %v", t.Ticket, err)
			return false
		case state == store.ApprovalAllowed:
			logf("[%s] budget: continuing the %s stage with another %s", t.Ticket, stage, usd(budget))
			return true
		case state == store.ApprovalDenied:
			logf("[%s] budget: you chose to stop the %s stage", t.Ticket, stage)
			return false
		case state == store.ApprovalExpired:
			return false
		}
	}
}

// budgetQuestion is the approval row's text - what the dashboard shows the
// human, verbatim.
func budgetQuestion(stage string, budget, spent float64) string {
	return fmt.Sprintf("The %s stage reached its %s budget (%s spent so far). Continue with another %s?",
		stage, usd(budget), usd(spent), usd(budget))
}

// budgetNeedsYou parks a stage the human (or the absence of a dashboard)
// stopped at its budget: the cost, and where to raise it - never a guess about
// the ticket.
func budgetNeedsYou(t Task, h Hooks, stage, errText string) Outcome {
	key := "`max_budget_usd`"
	if k := stageKey(stage); k != "" {
		key = "`max_budget_" + k + "_usd` (or `max_budget_usd`)"
	}
	return needsYou(t, h,
		fmt.Sprintf("%s stage stopped at its budget: %s", stage, oneLine(errText, 160)),
		fmt.Sprintf("The %s stage stopped because it reached its spending limit (%s) and was not continued.\n\n"+
			"Nothing is wrong with the ticket. Raise %s in `~/.pie/config.toml`, "+
			"or choose Continue on the dashboard's budget prompt next time, then re-run the ticket.",
			stage, errText, key))
}

// isBudgetStop reports whether a stage's ErrorText is a budget stop rather than
// an API failure - the two park differently.
func isBudgetStop(errText string) bool {
	return strings.HasPrefix(strings.TrimSpace(errText), agent.BudgetExceededPrefix)
}

// stageKey maps the stage names the log uses to their per-stage config key
// suffix, "" for a stage that only has max_budget_usd.
func stageKey(stage string) string {
	switch stage {
	case "plan", "verify":
		return stage
	case "implement", "rework", "fix round":
		return "impl"
	case "self-review":
		return "review"
	case "comment fix":
		return "comment_fix"
	}
	return ""
}

// usd renders a dollar amount: cents from $1 up, and below that up to four
// decimals - a $0.003 test budget printed as "$0.00" reads as "no budget".
func usd(v float64) string {
	if v >= 1 || v == 0 {
		return fmt.Sprintf("$%.2f", v)
	}
	s := strings.TrimRight(fmt.Sprintf("%.4f", v), "0")
	if len(s) < len("0.00") {
		s = fmt.Sprintf("%.2f", v)
	}
	return "$" + s
}
