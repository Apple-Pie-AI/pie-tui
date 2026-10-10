// Asking before giving up on a stage that hit its budget. A session that
// reaches --max-budget-usd used to end the stage outright - and, because the
// CLI's budget result carries no result text, the park blamed the ticket or
// the build. Now the run pauses with a question in the dashboard, through the
// same approvals table the permission callback uses: Continue resumes the
// SAME session with a fresh budget (verified live: --resume starts a new
// budget and keeps the context), Stop parks the ticket naming the budget.
//
// A session can also stop SHORT of its budget: Claude Code shows the agent its
// remaining budget every turn, and agents ration against it - PLEX-64819's
// implement session cut scope as the money ran down and quit with
// needs_human at $4.98 of $5, no code written. That ends "successfully", so
// there is no budget event; runStage treats a stage that ends unfinished after
// spending most of its budget the same way, and asks.
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

// rationedContinuePrompt resumes a session that stopped itself near its limit.
// It has to undo the agent's own conclusion: the report it just wrote says the
// work can't be done, and an agent that keeps believing that would rewrite the
// same report.
const rationedContinuePrompt = "You stopped before finishing because you judged the spending limit too low to continue. " +
	"The user has approved more budget, and the limit is not yours to manage. Continue the task from where you left off " +
	"and complete all of it - including the work you skipped or descoped to save budget. Do not start over. " +
	"If you wrote a .agent/ report marking the work not done because of budget, replace it when you finish."

// rationShare is how much of its budget a session must have spent for an
// unfinished stop to count as budget-driven rather than a genuine blocker.
const rationShare = 0.9

// rationedPrefix opens the ErrorText runStage gives a self-stopped session -
// recognized by isBudgetStop alongside the CLI's own budget message.
const rationedPrefix = "Stopped near its budget"

// unfinishedFn reports whether a stage's session ended without finishing its
// task, given when that session started (contract files older than that are
// leftovers). nil means the stage has no such check: only the CLI's hard
// budget stop is caught.
type unfinishedFn func(since time.Time) bool

// runStage is agent.Run for a pipeline stage, plus the budget question: when
// the session stops on its budget - the CLI's hard stop, or (with unfinished
// set) the agent quitting unfinished after spending rationShare of it - and
// there is a dashboard to ask through, it waits for the human and, on
// Continue, resumes the session with the same budget again, as many rounds as
// the human keeps approving. On Stop (or with no store, or no session to
// resume) it returns the result marked BudgetExceeded, whose ErrorText the
// stage turns into a budget park.
func runStage(ctx context.Context, t Task, stage, prompt string, opts agent.Options, logf logFn, unfinished unfinishedFn) (agent.Result, error) {
	spent := 0.0
	for {
		// Truncated like the verify freshness check: a coarse filesystem
		// mtime must not make this session's own contract file look older.
		start := time.Now().Truncate(time.Second)
		res, err := agent.Run(ctx, prompt, opts)
		spent += res.CostUSD
		if ctx.Err() != nil {
			return res, err
		}
		rationed := !res.BudgetExceeded && stoppedShort(res, opts.MaxBudgetUSD, unfinished, start)
		if rationed {
			res.BudgetExceeded = true
			res.ErrorText = fmt.Sprintf("%s: the agent stopped without finishing after spending %s of its %s budget",
				rationedPrefix, usd(res.CostUSD), usd(opts.MaxBudgetUSD))
		}
		if !res.BudgetExceeded {
			return res, err
		}
		if rationed {
			logf("[%s] ⏸ %s stage stopped unfinished at %s of its %s budget - treating it as a budget stop",
				t.Ticket, stage, usd(res.CostUSD), usd(opts.MaxBudgetUSD))
		} else {
			logf("[%s] ⏸ %s stage reached its %s budget (%s spent this stage)",
				t.Ticket, stage, usd(opts.MaxBudgetUSD), usd(spent))
		}
		if t.Store == nil || res.SessionID == "" {
			return res, err
		}
		if !askToContinue(ctx, t, stage, budgetQuestion(stage, opts.MaxBudgetUSD, spent, rationed), opts.MaxBudgetUSD, logf) {
			return res, err
		}
		opts.ResumeID = res.SessionID
		prompt = budgetContinuePrompt
		if rationed {
			prompt = rationedContinuePrompt
		}
	}
}

// planUnfinished: the plan stage finished iff it wrote plan.json this session.
func planUnfinished(worktree string) unfinishedFn {
	return func(since time.Time) bool { return !agent.ContractFresh(worktree, "plan.json", since) }
}

// reportUnfinished is the implement/verify check: no report.json written this
// session, or one that gives up (needs_human) - PLEX-64819's exact shape.
func reportUnfinished(worktree string) unfinishedFn {
	return func(since time.Time) bool {
		if !agent.ContractFresh(worktree, "report.json", since) {
			return true
		}
		rep, err := agent.ReadReport(worktree)
		return err != nil || rep.Status == "needs_human"
	}
}

// stoppedShort is the self-rationing test: the session spent most of its
// budget and its stage did not finish. Both halves are needed - an unfinished
// cheap session is a real blocker (the agent's needs_human stands), and an
// expensive finished one is just an expensive stage.
func stoppedShort(res agent.Result, budget float64, unfinished unfinishedFn, since time.Time) bool {
	if unfinished == nil || budget <= 0 || res.CostUSD < budget*rationShare {
		return false
	}
	return unfinished(since)
}

// askToContinue puts the budget question in the dashboard and blocks until the
// human answers it or the run is stopped. True means "continue".
func askToContinue(ctx context.Context, t Task, stage, question string, budget float64, logf logFn) bool {
	id, err := t.Store.CreateApproval(t.Ticket, agent.BudgetTool, question)
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
func budgetQuestion(stage string, budget, spent float64, rationed bool) string {
	if rationed {
		return fmt.Sprintf("The %s stage stopped without finishing near its %s budget (%s spent so far). Continue with another %s?",
			stage, usd(budget), usd(spent), usd(budget))
	}
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
		fmt.Sprintf("The %s stage stopped at its spending limit and was not continued:\n\n> %s\n\n"+
			"Nothing is wrong with the ticket. Raise %s in `~/.pie/config.toml`, "+
			"or choose Continue on the dashboard's budget prompt next time, then re-run the ticket.",
			stage, errText, key))
}

// isBudgetStop reports whether a stage's ErrorText is a budget stop rather than
// an API failure - the two park differently.
func isBudgetStop(errText string) bool {
	e := strings.TrimSpace(errText)
	return strings.HasPrefix(e, agent.BudgetExceededPrefix) || strings.HasPrefix(e, rationedPrefix)
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
