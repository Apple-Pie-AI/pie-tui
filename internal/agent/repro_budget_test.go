//go:build repro

// Repro pinning the two CLI behaviors the budget question is built on,
// against the REAL claude through Run/buildArgs/parseStream:
//  1. a session that hits --max-budget-usd ends with a result event Run
//     recognizes (BudgetExceeded, an ErrorText starting with
//     BudgetExceededPrefix, and a session id to resume);
//  2. --resume of that session runs under a FRESH budget - which is what makes
//     "Continue" in the dashboard possible at all.
//
// If a CLI upgrade breaks either, this fails before a ticket does. It spends
// about a cent. Run with:
//
//	GOTOOLCHAIN=auto go test -tags repro -run TestReproBudget -count=1 -v ./internal/agent
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestReproBudgetStopAndResume(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	logf := func(format string, a ...interface{}) { t.Logf(format, a...) }

	// A budget far below what even one turn costs: the stop is certain.
	stop, err := Run(ctx,
		"Count from 1 to 400, one number per line, then explain each number in one sentence.",
		Options{WorktreeDir: dir, AllowedTools: "Read", Model: "haiku", MaxBudgetUSD: 0.001, Logf: logf})
	t.Logf("stop: err=%v BudgetExceeded=%v ErrorText=%q CostUSD=%v session=%s",
		err, stop.BudgetExceeded, stop.ErrorText, stop.CostUSD, stop.SessionID)
	if !stop.BudgetExceeded {
		t.Fatal("the CLI's budget stop was not recognized - check its result event shape (subtype/terminal_reason)")
	}
	if !strings.HasPrefix(stop.ErrorText, BudgetExceededPrefix) {
		t.Errorf("ErrorText %q no longer starts with %q - the runner's park routing keys on it", stop.ErrorText, BudgetExceededPrefix)
	}
	if stop.SessionID == "" {
		t.Fatal("no session id on a budget stop - Continue has nothing to resume")
	}

	resumed, err := Run(ctx, "Reply with the single word OK.",
		Options{WorktreeDir: dir, AllowedTools: "Read", Model: "haiku", MaxBudgetUSD: 0.25,
			ResumeID: stop.SessionID, Logf: logf})
	t.Logf("resume: err=%v BudgetExceeded=%v ErrorText=%q CostUSD=%v", err, resumed.BudgetExceeded, resumed.ErrorText, resumed.CostUSD)
	if resumed.BudgetExceeded {
		t.Fatal("--resume inherited the exhausted budget: Continue would stop again at once")
	}
	if resumed.SessionID != stop.SessionID {
		t.Errorf("resume ran session %q, want the same session %q (context would be lost)", resumed.SessionID, stop.SessionID)
	}
}

// The premise of budgetRule: the agent knows its budget only because the
// harness passes --max-budget-usd (Claude Code then shows it every turn). If a
// CLI stops exposing it, the rule is dead weight; if one starts exposing it
// without the flag, removing the flag would no longer hide it.
func TestReproBudgetVisibleToAgent(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	ask := "Do you have a spending budget for this session? Reply with only YES or NO."
	var withFlag, without string
	_, _ = Run(ctx, ask, Options{WorktreeDir: t.TempDir(), AllowedTools: "Read", Model: "haiku",
		MaxBudgetUSD: 5, OnText: func(s string) { withFlag = s }})
	_, _ = Run(ctx, ask, Options{WorktreeDir: t.TempDir(), AllowedTools: "Read", Model: "haiku",
		OnText: func(s string) { without = s }})
	t.Logf("with --max-budget-usd: %q · without: %q", withFlag, without)
	if !strings.Contains(strings.ToUpper(withFlag), "YES") {
		t.Errorf("with --max-budget-usd the agent should see a budget, said %q", withFlag)
	}
	if !strings.Contains(strings.ToUpper(without), "NO") {
		t.Errorf("without the flag the agent should see no budget, said %q", without)
	}
}

// budgetRule against a real agent on a tight budget: a multi-step task whose
// budget runs low partway. With the rule the agent must not report
// needs_human for budget reasons - it keeps working (finishing, or being cut
// off by the hard stop, which the runner turns into the Continue question).
// The run without the rule is logged for comparison only: whether an unguided
// agent rations is model behavior, not something to assert on.
func TestReproBudgetRuleStopsRationing(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("no claude on PATH")
	}
	task := "Create 25 files named f01.txt to f25.txt in the current directory, one Write call per file, " +
		"each containing one sentence about its number. Then write .agent/report.json as " +
		`{"status":"ready_for_build","summary":"<what you did>"} - or, if you decide not to finish, ` +
		`{"status":"needs_human","summary":"<why>"}.`
	run := func(prompt string) (Result, *Report) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		dir := t.TempDir()
		res, _ := Run(ctx, prompt, Options{WorktreeDir: dir, AllowedTools: "Write Read", Model: "haiku",
			MaxBudgetUSD: 0.005, Logf: func(string, ...interface{}) {}})
		rep, _ := ReadReport(dir)
		return res, rep
	}
	describe := func(res Result, rep *Report) string {
		if rep == nil {
			return fmt.Sprintf("no report · hard stop=%v · $%.4f", res.BudgetExceeded, res.CostUSD)
		}
		return fmt.Sprintf("report %s %q · hard stop=%v · $%.4f", rep.Status, rep.Summary, res.BudgetExceeded, res.CostUSD)
	}

	plainRes, plainRep := run(task)
	t.Logf("without the rule: %s", describe(plainRes, plainRep))

	ruledRes, ruledRep := run(budgetRule + "\n\n" + task)
	t.Logf("with the rule:    %s", describe(ruledRes, ruledRep))
	if ruledRep != nil && ruledRep.Status == "needs_human" &&
		strings.Contains(strings.ToLower(ruledRep.Summary), "budget") {
		t.Errorf("with budgetRule the agent still gave up for budget: %q", ruledRep.Summary)
	}
}
