// Tests for the budget question: a stage that stops on --max-budget-usd asks
// in the dashboard (an approvals row) instead of ending, and Continue resumes
// the same session with a fresh budget. Offline and deterministic: claude is a
// stub on PATH, the "human" is a goroutine answering through the real store.
package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// budgetStubScript is a fake claude: it answers --version (the runner's
// preflight), appends each session's argv to $BUDGET_STUB_LOG as one line,
// stops on its budget for the first $BUDGET_STUB_STOPS invocations (with the
// result-event shape claude 2.1.295 emits - no "result" text, errors[] only),
// and finishes normally after that, writing $BUDGET_STUB_PLAN to
// .agent/plan.json when set.
const budgetStubScript = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "2.1.295 (stub)"; exit 0; fi
printf '%s' "$*" | tr '\n' ' ' >> "$BUDGET_STUB_LOG"
echo >> "$BUDGET_STUB_LOG"
calls=$(wc -l < "$BUDGET_STUB_LOG" | tr -d ' ')
echo '{"type":"system","subtype":"init","session_id":"sess-budget"}'
if [ "$calls" -le "${BUDGET_STUB_STOPS:-1}" ]; then
  echo '{"type":"result","subtype":"error_max_budget_usd","is_error":true,"terminal_reason":"budget_exhausted","total_cost_usd":1.5,"session_id":"sess-budget","errors":["Reached maximum budget ($1.5)"]}'
  exit 1
fi
if [ -n "$BUDGET_STUB_PLAN" ]; then
  mkdir -p .agent
  printf '%s' "$BUDGET_STUB_PLAN" > .agent/plan.json
fi
echo '{"type":"result","subtype":"success","result":"done","total_cost_usd":0.25,"session_id":"sess-budget"}'
`

// installBudgetStub puts the fake claude first on PATH and returns the path
// of its argv log.
func installBudgetStub(t *testing.T, stops string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(budgetStubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("BUDGET_STUB_LOG", log)
	t.Setenv("BUDGET_STUB_STOPS", stops)
	return log
}

func stubCalls(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func budgetStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// fastBudgetPoll shortens the question's poll for the test's duration.
func fastBudgetPoll(t *testing.T) {
	t.Helper()
	old := budgetPoll
	budgetPoll = 5 * time.Millisecond
	t.Cleanup(func() { budgetPoll = old })
}

// dashboardHuman answers budget questions as they appear, one decision per
// question, in order, and records each question's text. It stands in for the
// TUI overlay, writing through the same store call.
type dashboardHuman struct {
	mu        sync.Mutex
	questions []store.Approval
}

func (d *dashboardHuman) answer(ctx context.Context, t *testing.T, st *store.Store, decisions ...string) {
	go func() {
		for _, decision := range decisions {
			for {
				if ctx.Err() != nil {
					return
				}
				pending, err := st.PendingApprovals("")
				if err == nil && len(pending) > 0 {
					d.mu.Lock()
					d.questions = append(d.questions, pending[0])
					d.mu.Unlock()
					_ = st.DecideApproval(pending[0].ID, decision, false)
					break
				}
				time.Sleep(2 * time.Millisecond)
			}
		}
	}()
}

func (d *dashboardHuman) asked() []store.Approval {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]store.Approval(nil), d.questions...)
}

func budgetTask(st *store.Store) Task {
	return Task{Ticket: "BUD-1", Summary: "x", Cfg: &config.Config{MaxBudgetUSD: 1.5}, Store: st}
}

// Continue: the stage is asked about, then resumed - same session, a fresh
// budget of the same size, and the continuation prompt instead of the task
// prompt (re-sending the task would make the agent start over).
func TestRunStageContinueResumesSameSession(t *testing.T) {
	fastBudgetPoll(t)
	log := installBudgetStub(t, "1")
	st := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, store.ApprovalAllowed)

	res, err := runStage(ctx, budgetTask(st), "plan", "ORIGINAL TASK PROMPT",
		agent.Options{WorktreeDir: t.TempDir(), MaxBudgetUSD: 1.5}, quiet, nil)
	if err != nil {
		t.Fatalf("continued run errored: %v", err)
	}
	if res.BudgetExceeded {
		t.Fatal("the resumed run finished; BudgetExceeded must be false")
	}

	calls := stubCalls(t, log)
	if len(calls) != 2 {
		t.Fatalf("claude invoked %d times, want 2 (stop, then resume):\n%s", len(calls), strings.Join(calls, "\n"))
	}
	if strings.Contains(calls[0], "--resume") || !strings.Contains(calls[0], "ORIGINAL TASK PROMPT") {
		t.Errorf("first call should be the fresh task: %s", calls[0])
	}
	for _, want := range []string{"--resume sess-budget", "--max-budget-usd 1.5", "spending limit"} {
		if !strings.Contains(calls[1], want) {
			t.Errorf("continuation missing %q: %s", want, calls[1])
		}
	}
	if strings.Contains(calls[1], "ORIGINAL TASK PROMPT") {
		t.Errorf("continuation re-sent the task prompt (the agent would start over): %s", calls[1])
	}

	asked := human.asked()
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(asked))
	}
	q := asked[0]
	if q.Ticket != "BUD-1" || q.Tool != agent.BudgetTool {
		t.Errorf("question row = %+v, want ticket BUD-1, tool %q", q, agent.BudgetTool)
	}
	for _, want := range []string{"plan stage", "$1.50 budget", "$1.50 spent", "another $1.50"} {
		if !strings.Contains(q.Command, want) {
			t.Errorf("question %q missing %q", q.Command, want)
		}
	}
}

// Every round asks again: two budget stops, two Continues, three invocations -
// and the question reports what the stage has spent across rounds.
func TestRunStageAsksEveryRound(t *testing.T) {
	fastBudgetPoll(t)
	log := installBudgetStub(t, "2")
	st := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, store.ApprovalAllowed, store.ApprovalAllowed)

	res, _ := runStage(ctx, budgetTask(st), "implement", "TASK",
		agent.Options{WorktreeDir: t.TempDir(), MaxBudgetUSD: 1.5}, quiet, nil)
	if res.BudgetExceeded {
		t.Fatal("finished after two continues; BudgetExceeded must be false")
	}
	if n := len(stubCalls(t, log)); n != 3 {
		t.Fatalf("claude invoked %d times, want 3", n)
	}
	asked := human.asked()
	if len(asked) != 2 {
		t.Fatalf("asked %d times, want 2", len(asked))
	}
	if !strings.Contains(asked[1].Command, "$3.00 spent") {
		t.Errorf("second question should report both rounds' spend: %q", asked[1].Command)
	}
}

// Stop: no resume, the budget-stopped result comes back for the stage to
// park, and the question is recorded as denied.
func TestRunStageStopReturnsBudgetResult(t *testing.T) {
	fastBudgetPoll(t)
	log := installBudgetStub(t, "1")
	st := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, store.ApprovalDenied)

	res, _ := runStage(ctx, budgetTask(st), "verify", "TASK",
		agent.Options{WorktreeDir: t.TempDir(), MaxBudgetUSD: 1.5}, quiet, nil)
	if !res.BudgetExceeded || !isBudgetStop(res.ErrorText) {
		t.Fatalf("Stop must return the budget stop: BudgetExceeded=%v ErrorText=%q", res.BudgetExceeded, res.ErrorText)
	}
	if n := len(stubCalls(t, log)); n != 1 {
		t.Errorf("claude invoked %d times after Stop, want 1", n)
	}
	if p, _ := st.PendingApprovals(""); len(p) != 0 {
		t.Errorf("question still pending after Stop: %+v", p)
	}
}

// Stopping the run while the question waits expires it - a prompt with no
// agent behind it must not stay on the dashboard - and returns promptly.
func TestRunStageCancelWhileAskingExpiresQuestion(t *testing.T) {
	fastBudgetPoll(t)
	installBudgetStub(t, "1")
	st := budgetStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if p, _ := st.PendingApprovals(""); len(p) > 0 {
				cancel()
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	done := make(chan agent.Result, 1)
	go func() {
		res, _ := runStage(ctx, budgetTask(st), "plan", "TASK",
			agent.Options{WorktreeDir: t.TempDir(), MaxBudgetUSD: 1.5}, quiet, nil)
		done <- res
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runStage kept waiting after the run was cancelled")
	}
	var ids []int64
	rows, _ := st.PendingApprovals("")
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if len(ids) != 0 {
		t.Fatalf("question left pending after cancel: %v", ids)
	}
	if state, err := st.ApprovalState(1); err != nil || state != store.ApprovalExpired {
		t.Errorf("question state = %q (%v), want expired", state, err)
	}
}

// Without a store there is no dashboard to ask through: the stop comes back
// at once (the old behavior, now with an honest ErrorText).
func TestRunStageWithoutStoreDoesNotWait(t *testing.T) {
	log := installBudgetStub(t, "1")
	task := budgetTask(nil)
	res, _ := runStage(context.Background(), task, "plan", "TASK",
		agent.Options{WorktreeDir: t.TempDir(), MaxBudgetUSD: 1.5}, quiet, nil)
	if !res.BudgetExceeded {
		t.Fatal("want the budget stop returned")
	}
	if n := len(stubCalls(t, log)); n != 1 {
		t.Errorf("claude invoked %d times, want 1", n)
	}
}

// The park for a stopped stage names the budget and the key to raise - never
// the API-problem text it used to borrow - and API errors still park as such.
func TestBudgetParkMessages(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	var said []string
	h := Hooks{Comment: func(s string) { said = append(said, s) }}
	task := Task{Ticket: "BUD-1", Cfg: &config.Config{}}

	out := apiNeedsYou(task, h, "plan", "Reached maximum budget ($1.5)")
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q", out.State)
	}
	msg := said[len(said)-1]
	for _, want := range []string{"spending limit", "max_budget_plan_usd", "Nothing is wrong with the ticket"} {
		if !strings.Contains(msg, want) {
			t.Errorf("budget park missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "Claude API rejected") {
		t.Errorf("budget park still blames the API:\n%s", msg)
	}

	apiNeedsYou(task, h, "verify", "API Error: Request rejected (429)")
	if !strings.Contains(said[len(said)-1], "Claude API rejected") {
		t.Errorf("a real API error must keep its own park:\n%s", said[len(said)-1])
	}

	for stage, key := range map[string]string{
		"implement": "max_budget_impl_usd", "self-review": "max_budget_review_usd",
		"comment fix": "max_budget_comment_fix_usd", "verify": "max_budget_verify_usd",
	} {
		budgetNeedsYou(task, h, stage, "Reached maximum budget ($5)")
		if !strings.Contains(said[len(said)-1], key) {
			t.Errorf("%s park should name %s:\n%s", stage, key, said[len(said)-1])
		}
	}
}

// Small budgets keep their precision; ordinary ones read as dollars and cents.
func TestUSD(t *testing.T) {
	for in, want := range map[float64]string{
		0: "$0.00", 0.003: "$0.003", 0.00393701: "$0.0039", 0.25: "$0.25", 0.5: "$0.50",
		1.5: "$1.50", 5: "$5.00", 5.0234: "$5.02",
	} {
		if got := usd(in); got != want {
			t.Errorf("usd(%v) = %q, want %q", in, got, want)
		}
	}
}
