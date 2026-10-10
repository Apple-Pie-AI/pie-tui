// Pipeline-level regression for the budget question: the real Run, against a
// real git repo and SQLite store, with a stub claude whose plan stage runs out
// of budget. Before this change the same run parked "the ticket may be too
// ambiguous" - the CLI's budget result carries no text, so the stop was
// invisible and the missing plan.json was blamed on the ticket.
package runner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// A plan whose blocking question ends the run at awaiting-answer - the first
// stop after planning, so the test proves the plan stage completed without
// needing implement/verify/ship stubs.
const budgetStubPlan = `{"plan":"Add the screen.","questions":["Which screen?"],` +
	`"confidence":"high","type":"feature","needs_emulator":false}`

func runBudgetPipeline(t *testing.T, decision string) (Outcome, []string, []store.Approval, []string) {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	t.Setenv("BUDGET_STUB_PLAN", budgetStubPlan)
	fastBudgetPoll(t)
	log := installBudgetStub(t, "1")
	repo := repoWithBranch(t)
	st := budgetStore(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, decision)

	var said []string
	out := Run(ctx, Task{
		Ticket: "BUD-7", Summary: "Add a settings screen",
		Repo:  &config.Repo{Path: repo, Branch: "ai/{ticket}"},
		Cfg:   &config.Config{MaxBudgetUSD: 5, MaxBudgetPlanUSD: 1.5},
		Store: st,
	}, Hooks{Comment: func(s string) { said = append(said, s) }})
	return out, said, human.asked(), stubCalls(t, log)
}

// Continue: the plan stage resumes, writes its plan, and the ticket reaches
// the next real gate (its blocking question) - not needs-you.
func TestPipelinePlanBudgetContinue(t *testing.T) {
	out, _, asked, calls := runBudgetPipeline(t, store.ApprovalAllowed)
	if out.State != store.StateAwaiting {
		t.Fatalf("state = %q (err %v), want %q - the plan should have finished after Continue",
			out.State, out.Err, store.StateAwaiting)
	}
	if len(asked) != 1 || asked[0].Tool != agent.BudgetTool || asked[0].Ticket != "BUD-7" {
		t.Fatalf("budget questions = %+v, want one for BUD-7", asked)
	}
	if !strings.Contains(asked[0].Command, "plan stage reached its $1.50 budget") {
		t.Errorf("question should use the plan stage's own budget: %q", asked[0].Command)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "--resume sess-budget") ||
		!strings.Contains(calls[1], "--max-budget-usd 1.5") {
		t.Errorf("want the plan resumed once with a fresh $1.5 budget, got:\n%s", strings.Join(calls, "\n"))
	}
}

// Stop: the ticket parks at needs-you, and the human is told it was the
// budget and which key to raise - not that the ticket is ambiguous.
func TestPipelinePlanBudgetStop(t *testing.T) {
	out, said, asked, calls := runBudgetPipeline(t, store.ApprovalDenied)
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want %q", out.State, store.StateNeedsYou)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(asked))
	}
	if len(calls) != 1 {
		t.Errorf("claude invoked %d times after Stop, want 1", len(calls))
	}
	all := strings.Join(said, "\n")
	for _, want := range []string{"spending limit", "max_budget_plan_usd"} {
		if !strings.Contains(all, want) {
			t.Errorf("park comment missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "too ambiguous") {
		t.Errorf("a budget stop was blamed on the ticket again:\n%s", all)
	}
}
