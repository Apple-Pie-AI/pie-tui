package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// readyWorktree makes a directory that paths.WorktreeReady accepts, so the
// resume/ship entrypoints get past their "no worktree" guard and reach the
// stages this file is about.
func readyWorktree(t *testing.T, repo, ticket string) {
	t.Helper()
	wt := filepath.Join(os.Getenv("PIE_HOME"), "worktrees", ticket)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A cancelled run must record NO lifecycle state. Whoever cancelled owns that
// write: the TUI's Pause sets needs-you and its Stop sets stopped, both BEFORE
// signalling, so any state the pipeline writes on its way out clobbers the
// specific one with a useless generic.
//
// This is the invariant that the --resume path violated in review - it reached
// verifyWithAgent, whose first statement is setState(building), with no guard in
// between - so it is asserted per entrypoint rather than once.
func TestCancelledRunRecordsNoState(t *testing.T) {
	entrypoints := map[string]func(*Task){
		"full pipeline": func(*Task) {},
		"--resume":      func(tk *Task) { tk.Resume = true },
		"--ship":        func(tk *Task) { tk.Ship = true },
		"--from-plan":   func(tk *Task) { tk.FromPlan = true },
	}

	for name, configure := range entrypoints {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PIE_HOME", t.TempDir())
			readyWorktree(t, "", "CAN-1")

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // cancelled before the pipeline even starts

			task := Task{
				Ticket: "CAN-1", Summary: "x",
				Repo: &config.Repo{Path: t.TempDir(), Branch: "ai/{ticket}"},
				Cfg:  &config.Config{},
			}
			configure(&task)

			var tr transcript
			out := Run(ctx, task, tr.hooks())

			if len(tr.states) != 0 {
				t.Errorf("a cancelled run wrote %v - it must leave the canceller's state alone", tr.states)
			}
			if out.State != store.StateStopped {
				t.Errorf("Outcome.State = %q, want %q", out.State, store.StateStopped)
			}
			if out.Err == nil {
				t.Error("Outcome.Err should carry the cancellation")
			}
		})
	}
}

// verifyWithAgent is where the violation actually lived: its first statement is
// setState(building). The guard is inside it, not only at the call sites,
// because the pipeline can be cancelled between a stage check and this call.
func TestVerifyWithAgentWritesNoStateWhenCancelled(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var states []string
	rep, ok, sid, denials, errText := verifyWithAgent(ctx,
		Task{Ticket: "V-1", Summary: "x", Cfg: &config.Config{}},
		t.TempDir(), "session-abc", false, true, "",
		func(string, ...interface{}) {},
		func(s string, _ int) { states = append(states, s) },
	)
	if denials != nil || errText != "" {
		t.Errorf("cancelled verify should report no denials/error, got %v %q", denials, errText)
	}

	if len(states) != 0 {
		t.Errorf("verifyWithAgent wrote %v on a cancelled run, want nothing", states)
	}
	if rep != nil || ok {
		t.Errorf("cancelled verify should certify nothing, got report=%v verified=%v", rep, ok)
	}
	if sid != "session-abc" {
		t.Errorf("session id = %q, want it passed through untouched", sid)
	}
}

// stopped() is the guard itself: it reports cancellation and, deliberately,
// never touches the hooks.
func TestStoppedReportsCancellationWithoutSideEffects(t *testing.T) {
	task := Task{Ticket: "S-1"}
	noop := func(string, ...interface{}) {}

	if _, done := stopped(context.Background(), task, noop); done {
		t.Error("stopped() reported a live context as cancelled")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, done := stopped(ctx, task, noop)
	if !done {
		t.Fatal("stopped() missed a cancelled context")
	}
	if out.State != store.StateStopped || out.Err == nil {
		t.Errorf("outcome = %+v, want stopped with an error", out)
	}
}
