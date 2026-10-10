// Tests for the self-rationing case: an agent that sees its budget running
// down and quits unfinished, with no budget event at all. The fixture is
// PLEX-64819's implement session: $4.98 of $5 spent on research, report.json
// status needs_human "Ran out of agent budget ... no code was changed". It
// used to park "the agent determined it cannot safely complete this ticket";
// now it asks, and Continue resumes the same session to finish the work.
package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// rationStubScript is a stage-aware fake claude. It tells stages apart by
// their prompts and logs each session's argv as one line to $RATION_STUB_LOG.
// The implement session behaves per $IMPL_MODE at cost $IMPL_COST:
//
//	ration  - PLEX-64819: writes a needs_human report, changes no code
//	nothing - writes nothing at all
//	done    - finishes: code plus a ready_for_build report
//
// A session resumed with the rationed-continue prompt finishes the work.
const rationStubScript = `#!/bin/sh
if [ "$1" = "--version" ]; then echo "2.1.295 (stub)"; exit 0; fi
args="$*"
printf '%s' "$args" | tr '\n' ' ' >> "$RATION_STUB_LOG"
echo >> "$RATION_STUB_LOG"
mkdir -p .agent
emit() {
  echo '{"type":"system","subtype":"init","session_id":"'"$1"'"}'
  echo '{"type":"result","subtype":"success","result":"done","total_cost_usd":'"$2"',"session_id":"'"$1"'"}'
}
ready() {
  echo 'class TaskListViewModel' > TaskListViewModel.kt
  printf '%s' '{"status":"ready_for_build","summary":"Task list screen and ViewModel","filesChanged":["TaskListViewModel.kt"],"branch":"","tests":"unit","prBody":""}' > .agent/report.json
}
case "$args" in
*"Plan the implementation of a ticket"*)
  printf '%s' '{"plan":"Build the task list screen and ViewModel.","questions":[],"confidence":"medium","type":"feature","needs_emulator":false}' > .agent/plan.json
  emit plan-sess 0.5 ;;
*"judged the spending limit"*)
  ready
  emit impl-sess 2.0 ;;
*"implementing a pre-approved plan"*)
  case "${IMPL_MODE:-ration}" in
  ration)
    printf '%s' '{"status":"needs_human","summary":"Not implemented: PLEX-64819 task list screen and ViewModel. Ran out of agent budget after researching the APIs; no code was changed.","filesChanged":[],"branch":"","tests":"","prBody":""}' > .agent/report.json ;;
  done) ready ;;
  esac
  emit impl-sess "${IMPL_COST:-4.98}" ;;
*"adversarially reviewing"*)
  printf '%s' '{"verdict":"pass","issues":[]}' > .agent/review.json
  emit impl-sess 0.2 ;;
*"You are verifying that the change"*)
  printf '%s' '{"status":"ready_for_build","summary":"Task list screen and ViewModel","filesChanged":["TaskListViewModel.kt"],"branch":"","tests":"unit","prBody":"","verified":true,"verifyLog":"./gradlew test: BUILD SUCCESSFUL"}' > .agent/report.json
  emit impl-sess 0.5 ;;
*)
  emit other-sess 0.1 ;;
esac
`

func installRationStub(t *testing.T, mode, cost string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(rationStubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("RATION_STUB_LOG", log)
	t.Setenv("IMPL_MODE", mode)
	t.Setenv("IMPL_COST", cost)
	return log
}

const implPrompt = "You are an autonomous Android engineer implementing a pre-approved plan"

func runImplStage(t *testing.T, st *store.Store, wt string) agent.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, _ := runStage(ctx, budgetTask(st), "implement", implPrompt,
		agent.Options{WorktreeDir: wt, MaxBudgetUSD: 5}, quiet, reportUnfinished(wt))
	return res
}

// PLEX-64819, unit level: the needs_human-at-$4.98 session is asked about,
// and Continue resumes the SAME session with the prompt that tells it the
// budget isn't its to manage - which finishes the work.
func TestRunStageRationedAsksAndResumes(t *testing.T) {
	fastBudgetPoll(t)
	log := installRationStub(t, "ration", "4.98")
	st := budgetStore(t)
	wt := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, store.ApprovalAllowed)

	res := runImplStage(t, st, wt)
	if res.BudgetExceeded {
		t.Fatalf("finished after Continue; BudgetExceeded must be false (ErrorText %q)", res.ErrorText)
	}
	rep, err := agent.ReadReport(wt)
	if err != nil || rep.Status != "ready_for_build" {
		t.Fatalf("report after Continue = %+v (%v), want ready_for_build", rep, err)
	}
	asked := human.asked()
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(asked))
	}
	for _, want := range []string{"implement stage stopped without finishing", "$5.00 budget", "$4.98 spent"} {
		if !strings.Contains(asked[0].Command, want) {
			t.Errorf("question %q missing %q", asked[0].Command, want)
		}
	}
	calls := stubCalls(t, log)
	if len(calls) != 2 {
		t.Fatalf("claude invoked %d times, want 2", len(calls))
	}
	for _, want := range []string{"--resume impl-sess", "--max-budget-usd 5", "judged the spending limit", "descoped"} {
		if !strings.Contains(calls[1], want) {
			t.Errorf("continuation missing %q: %s", want, calls[1])
		}
	}
}

// Stop on a rationed stage returns a budget stop the parks recognize.
func TestRunStageRationedStop(t *testing.T) {
	fastBudgetPoll(t)
	installRationStub(t, "ration", "4.98")
	st := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, store.ApprovalDenied)

	res := runImplStage(t, st, t.TempDir())
	if !res.BudgetExceeded || !isBudgetStop(res.ErrorText) {
		t.Fatalf("want a budget stop, got BudgetExceeded=%v ErrorText=%q", res.BudgetExceeded, res.ErrorText)
	}
	if !strings.Contains(res.ErrorText, "$4.98 of its $5.00 budget") {
		t.Errorf("ErrorText should state the spend: %q", res.ErrorText)
	}
}

// What must NOT ask: a cheap needs_human is a real blocker, and an expensive
// session that finished is just an expensive stage.
func TestRunStageDoesNotAskWhenNotRationing(t *testing.T) {
	for _, tc := range []struct{ name, mode, cost string }{
		{"genuine blocker, cheap", "ration", "0.40"},
		{"finished near the cap", "done", "4.98"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastBudgetPoll(t)
			log := installRationStub(t, tc.mode, tc.cost)
			st := budgetStore(t)
			res := runImplStage(t, st, t.TempDir())
			if res.BudgetExceeded {
				t.Errorf("treated as a budget stop: %q", res.ErrorText)
			}
			if p, _ := st.PendingApprovals(""); len(p) != 0 {
				t.Errorf("asked a budget question: %+v", p)
			}
			if n := len(stubCalls(t, log)); n != 1 {
				t.Errorf("claude invoked %d times, want 1", n)
			}
		})
	}
}

// A report left over from an earlier run doesn't count as finishing: the
// session that wrote nothing at $4.98 is still asked about.
func TestRunStageStaleReportIsUnfinished(t *testing.T) {
	fastBudgetPoll(t)
	installRationStub(t, "nothing", "4.98")
	st := budgetStore(t)
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(wt, ".agent", "report.json")
	if err := os.WriteFile(old, []byte(`{"status":"ready_for_build","summary":"yesterday"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, store.ApprovalDenied)

	if res := runImplStage(t, st, wt); !res.BudgetExceeded {
		t.Fatal("a stale ready report hid an unfinished session")
	}
	if len(human.asked()) != 1 {
		t.Errorf("asked %d times, want 1", len(human.asked()))
	}
}

// Pipeline regression, PLEX-64819 end to end: the real Run on a real repo and
// store. Continue → the implement session finishes, self-review and verify
// run, and the ticket reaches the human review gate before any PR.
func TestPipelineRationedImplementContinue(t *testing.T) {
	out, said, asked, calls := runRationPipeline(t, store.ApprovalAllowed)
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q (err %v), want %q\ncomments:\n%s\ncalls:\n%s",
			out.State, out.Err, store.StateChangeReview, strings.Join(said, "\n"), strings.Join(calls, "\n"))
	}
	if len(asked) != 1 || !strings.Contains(asked[0].Command, "implement stage stopped without finishing") {
		t.Fatalf("budget questions = %+v, want one about the implement stage", asked)
	}
	var resumed bool
	for _, c := range calls {
		if strings.Contains(c, "judged the spending limit") && strings.Contains(c, "--resume impl-sess") {
			resumed = true
		}
	}
	if !resumed {
		t.Errorf("the rationed implement session was never resumed:\n%s", strings.Join(calls, "\n"))
	}
}

// Pipeline regression, Stop: needs-you naming the budget - not "the agent
// determined it cannot safely complete this ticket", which is what
// PLEX-64819 said and sent the human looking at the ticket.
func TestPipelineRationedImplementStop(t *testing.T) {
	out, said, asked, _ := runRationPipeline(t, store.ApprovalDenied)
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want %q", out.State, store.StateNeedsYou)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(asked))
	}
	all := strings.Join(said, "\n")
	for _, want := range []string{"spending limit", "max_budget_impl_usd", "Nothing is wrong with the ticket"} {
		if !strings.Contains(all, want) {
			t.Errorf("park missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "cannot safely complete") {
		t.Errorf("a budget give-up was reported as the agent's judgment of the ticket:\n%s", all)
	}
}

func runRationPipeline(t *testing.T, decision string) (Outcome, []string, []store.Approval, []string) {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	fastBudgetPoll(t)
	log := installRationStub(t, "ration", "4.98")
	repo := repoWithBranch(t)
	st := budgetStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var human dashboardHuman
	human.answer(ctx, t, st, decision)

	var said []string
	out := Run(ctx, Task{
		Ticket: "PLEX-64819", Summary: "Part II task-list-ui",
		ReviewChange: true, // pie run's default: park for review before any PR
		Repo:         &config.Repo{Path: repo, Branch: "ai/{ticket}"},
		Cfg:          &config.Config{MaxBudgetUSD: 5},
		Store:        st,
	}, Hooks{Comment: func(s string) { said = append(said, s) }})
	return out, said, human.asked(), stubCalls(t, log)
}
