// Recognizing a session that stopped on --max-budget-usd. The CLI ends such a
// run with a result event of subtype error_max_budget_usd that carries NO
// "result" text - only errors: ["Reached maximum budget ($5)"] - so the
// generic result handling in parseStream saw nothing and the stage was
// reported as whatever its missing contract file implied ("ticket too
// ambiguous", "no report"). Shape verified live on claude 2.1.295:
//
//	{"type":"result","subtype":"error_max_budget_usd","is_error":true,
//	 "terminal_reason":"budget_exhausted","total_cost_usd":0.004,
//	 "errors":["Reached maximum budget ($0.001)"], ...}
//
// Resuming the same session with --resume starts a fresh budget (also
// verified live), which is what lets the runner ask the human and continue
// instead of throwing the session's work away.
package agent

import "strings"

// BudgetSubtype is the result-event subtype the CLI emits when a session
// stops on --max-budget-usd.
const BudgetSubtype = "error_max_budget_usd"

// BudgetTool is the Tool value of an approval row that asks the human to
// extend a stage's budget rather than to run a command. The dashboard renders
// those rows as Continue / Stop instead of the allow/remember choices.
const BudgetTool = "Budget"

// BudgetExceededPrefix opens Result.ErrorText for a budget stop when the CLI
// gave no message of its own - and is what the runner's park message keys on.
const BudgetExceededPrefix = "Reached maximum budget"

// noteResultMeta records what a result event says about cost and budget: the
// session's running cost, and whether it stopped on its budget. Kept out of
// parseStream so the one event shape that carries no "result" text still
// produces an ErrorText the stages can name.
func noteResultMeta(ev map[string]interface{}, res *Result) {
	if c, ok := ev["total_cost_usd"].(float64); ok {
		res.CostUSD = c
	}
	sub, _ := ev["subtype"].(string)
	reason, _ := ev["terminal_reason"].(string)
	if sub != BudgetSubtype && reason != "budget_exhausted" {
		return
	}
	res.BudgetExceeded = true
	msg := BudgetExceededPrefix
	if errs, ok := ev["errors"].([]interface{}); ok {
		for _, e := range errs {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				msg = strings.TrimSpace(s)
				break
			}
		}
	}
	res.ErrorText = msg
}
