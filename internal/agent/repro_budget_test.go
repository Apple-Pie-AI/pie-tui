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
