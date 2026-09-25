package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// changeFixture is a ticket parked at the change-review gate: a worktree with
// one commit and an uncommitted edit, a session row, and the park's
// fingerprint recorded (matching or stale per the test).
type changeFixture struct {
	wt   string
	st   *store.Store
	task Task
	logs *[]string
	hook Hooks
}

func newChangeFixture(t *testing.T) changeFixture {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	wt := changeWorktree(t) // one commit + uncommitted edit (change_test.go)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Claim("C-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("C-1", "b", wt, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeParked("C-1", git.ChangeFingerprint(wt), 1); err != nil {
		t.Fatal(err)
	}
	var logs []string
	return changeFixture{
		wt: wt, st: st, logs: &logs,
		task: Task{Ticket: "C-1", Repo: &config.Repo{Path: "/repo"}, Cfg: &config.Config{},
			Store: st, DryRun: true, PriorState: store.StateChangeReview},
		hook: Hooks{Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }},
	}
}

// stubClaude puts a claude on PATH that writes a report and echoes; verdict
// is the verified value it certifies.
func stubClaude(t *testing.T, wt string, verified bool) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nmkdir -p '" + filepath.Join(wt, ".agent") + "'\n" +
		"cat > '" + filepath.Join(wt, ".agent", "report.json") + "' <<'EOJ'\n" +
		fmt.Sprintf(`{"status":"ready_for_build","verified":%v,"summary":"s","verifyLog":"the tests are red","prBody":"body"}`, verified) + "\n" +
		"EOJ\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stubClaudeDropsPRBody puts a claude on PATH that always writes a report
// with NO prBody field at all - simulating BuildChangeReworkPrompt's
// "preserve every other field (prBody especially)" instruction going
// unenforced, the exact PLEX-60299 class of bug. Every invocation (the
// rework spawn AND its own verify) drops it, so a fix that only guards one
// of the two would still fail this.
func stubClaudeDropsPRBody(t *testing.T, wt string, verified bool) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nmkdir -p '" + filepath.Join(wt, ".agent") + "'\n" +
		"cat > '" + filepath.Join(wt, ".agent", "report.json") + "' <<'EOJ'\n" +
		fmt.Sprintf(`{"status":"ready_for_build","verified":%v,"summary":"s","verifyLog":"ok"}`, verified) + "\n" +
		"EOJ\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// The gate: a run without the park behind it bounces; the launcher's captured
// PriorState carries the answer past the row reset, like ship-comments.
func TestShipChangeGateBounces(t *testing.T) {
	fx := newChangeFixture(t)
	fx.task.PriorState = store.StateQueued // no reviewable row behind it
	out := shipChange(t.Context(), fx.task, fx.hook)
	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you (gate refusal)", out.State)
	}
	if !strings.Contains(strings.Join(*fx.logs, "\n"), "before the change was parked") {
		t.Errorf("gate refusal not named:\n%s", strings.Join(*fx.logs, "\n"))
	}
}

// An untouched change ships on the parking verify: no claude spawn at all.
// The fixture deliberately puts NO claude on PATH - a spawn would fail loudly.
func TestShipChangeUnchangedSkipsVerify(t *testing.T) {
	fx := newChangeFixture(t)
	out := shipChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if out.State != store.StateReview {
		t.Fatalf("state = %q, want review (dry-run ship):\n%s", out.State, joined)
	}
	if !strings.Contains(joined, "the parking verify still stands") {
		t.Errorf("unchanged path not taken:\n%s", joined)
	}
	sess, _ := fx.st.Get("C-1")
	if sess.ChangeError != "" {
		t.Errorf("a green ship must clear the error, got %q", sess.ChangeError)
	}
}

// A change that moved since the park re-verifies before shipping.
func TestShipChangeMovedReverifies(t *testing.T) {
	fx := newChangeFixture(t)
	stubClaude(t, fx.wt, true)
	if err := os.WriteFile(filepath.Join(fx.wt, "a.kt"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := shipChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if out.State != store.StateReview {
		t.Fatalf("state = %q, want review:\n%s", out.State, joined)
	}
	if !strings.Contains(joined, "re-verifying before the PR") {
		t.Errorf("moved change did not re-verify:\n%s", joined)
	}
}

// Regression, same PLEX-60299 class as TestReworkChangeRestoresADroppedPRBody:
// a hand-edit-triggered re-verify (e.g. edited in Android Studio after the
// park) whose agent drops prBody must not ship a PR with an empty/generic
// body either - defense-in-depth alongside verifyWithAgent's own guard.
func TestShipChangeMovedReverifyRestoresADroppedPRBody(t *testing.T) {
	fx := newChangeFixture(t)
	verified := true
	if err := agent.WriteReport(fx.wt, &agent.Report{
		Status: "ready_for_build", PRBody: "the real template body", Verified: &verified,
	}); err != nil {
		t.Fatal(err)
	}
	stubClaudeDropsPRBody(t, fx.wt, true)
	if err := os.WriteFile(filepath.Join(fx.wt, "a.kt"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := shipChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if out.State != store.StateReview {
		t.Fatalf("state = %q, want review:\n%s", out.State, joined)
	}

	got, err := agent.ReadReport(fx.wt)
	if err != nil {
		t.Fatal(err)
	}
	if got.PRBody != "the real template body" {
		t.Fatalf("PRBody = %q, want the restored template body", got.PRBody)
	}
}

// A red re-verify returns the ticket TO THE GATE with the error stored - the
// screen shows it; nothing ships and nothing scatters to a dead end.
func TestShipChangeRedVerifyReparksWithError(t *testing.T) {
	fx := newChangeFixture(t)
	stubClaude(t, fx.wt, false)
	if err := os.WriteFile(filepath.Join(fx.wt, "a.kt"), []byte("hand-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var states []string
	fx.hook.OnState = func(s string, _ int) { states = append(states, s) }
	out := shipChange(t.Context(), fx.task, fx.hook)
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review (back to the gate)", out.State)
	}
	sess, _ := fx.st.Get("C-1")
	if !strings.Contains(sess.ChangeError, "the tests are red") {
		t.Errorf("ChangeError = %q, want the verify reason", sess.ChangeError)
	}
	if len(states) == 0 || states[len(states)-1] != store.StateChangeReview {
		t.Errorf("final recorded state = %v, want change-review", states)
	}
	// cmd/run.go's own completion banner reads Outcome.Err, not the store -
	// without it a failed ship printed the same "✓ ... waiting for your
	// review" line a real success would have.
	if out.Err == nil {
		t.Error("a red re-verify must set Outcome.Err, or the run's own completion banner reports it as a success")
	}
}

// The rework round: the plain-text draft resumes the session, is consumed on
// success, and the next round parks with a fresh baseline.
func TestReworkChangeParksNextRound(t *testing.T) {
	fx := newChangeFixture(t)
	stubClaude(t, fx.wt, true) // serves both the rework spawn and its verify
	if err := fx.st.SetChangeNotes("C-1", "@a.kt:2 rename the thing"); err != nil {
		t.Fatal(err)
	}
	fx.task.Rework = true
	out := reworkChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review (round 2):\n%s", out.State, joined)
	}
	sess, _ := fx.st.Get("C-1")
	if sess.ChangeRound != 2 {
		t.Errorf("round = %d, want 2", sess.ChangeRound)
	}
	if sess.ChangeRoundTree == "" {
		t.Error("round baseline tree not recorded")
	}
	if sess.ChangeNotes != "" {
		t.Errorf("draft not consumed: %q", sess.ChangeNotes)
	}
	if !strings.Contains(joined, "stage:rework") {
		t.Errorf("no rework stage ran:\n%s", joined)
	}
}

// Regression (user-reported: "Apple Pie is not respecting the PR template"):
// a rework round that drops prBody (the agent didn't preserve it, despite
// BuildChangeReworkPrompt's instruction to) must not park/ship a PR with an
// empty or generic body - the last known-good, template-filled body (from
// before this round touched anything) is restored via adoptVerifyReport.
// This is the same PLEX-60299 class PR #9 fixed for the original ship flow;
// reworkVerifyAndPark had never been given the same guard.
func TestReworkChangeRestoresADroppedPRBody(t *testing.T) {
	fx := newChangeFixture(t)
	verified := true
	// The park this round starts from already has a real, template-filled
	// body - what a previous round or the original implementation left.
	if err := agent.WriteReport(fx.wt, &agent.Report{
		Status: "ready_for_build", PRBody: "the real template body", Verified: &verified,
	}); err != nil {
		t.Fatal(err)
	}
	stubClaudeDropsPRBody(t, fx.wt, true) // both the rework spawn and its own verify drop prBody
	if err := fx.st.SetChangeNotes("C-1", "@a.kt:2 rename the thing"); err != nil {
		t.Fatal(err)
	}
	fx.task.Rework = true
	out := reworkChange(t.Context(), fx.task, fx.hook)
	joined := strings.Join(*fx.logs, "\n")
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review (round 2):\n%s", out.State, joined)
	}

	got, err := agent.ReadReport(fx.wt)
	if err != nil {
		t.Fatal(err)
	}
	if got.PRBody != "the real template body" {
		t.Fatalf("PRBody = %q, want the restored template body - a rework that dropped it must not "+
			"park with an empty PR description", got.PRBody)
	}
	if !strings.Contains(joined, "a later stage dropped the PR body") {
		t.Errorf("the restore must be logged (visible in pie logs):\n%s", joined)
	}
}

// A rework with no feedback at all is a no-op that says so at the gate.
func TestReworkChangeWithNothingPending(t *testing.T) {
	fx := newChangeFixture(t)
	fx.task.Rework = true
	out := reworkChange(t.Context(), fx.task, fx.hook)
	if out.State != store.StateChangeReview {
		t.Fatalf("state = %q, want change-review", out.State)
	}
	sess, _ := fx.st.Get("C-1")
	if !strings.Contains(sess.ChangeError, "nothing to do") {
		t.Errorf("ChangeError = %q, want the no-op named", sess.ChangeError)
	}
}

// The revert helper's two shapes: a tracked file returns to HEAD, an
// untracked addition is deleted.
func TestRevertFile(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	wt := changeWorktree(t)
	if err := git.RevertFile(wt, "a.kt"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(wt, "a.kt"))
	if string(b) != "base\n" {
		t.Errorf("tracked revert = %q, want base content", b)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.kt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := git.RevertFile(wt, "new.kt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "new.kt")); !os.IsNotExist(err) {
		t.Error("untracked revert must delete the file")
	}
}

// SnapshotTree captures untracked content without touching the worktree or
// the real index, and two snapshots differ iff the content moved.
func TestSnapshotTree(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	wt := changeWorktree(t)
	t1, err := git.SnapshotTree(wt)
	if err != nil || t1 == "" {
		t.Fatalf("snapshot: %q %v", t1, err)
	}
	t2, _ := git.SnapshotTree(wt)
	if t1 != t2 {
		t.Error("unchanged content must snapshot to the same tree")
	}
	if err := os.WriteFile(filepath.Join(wt, "extra.kt"), []byte("u\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t3, _ := git.SnapshotTree(wt)
	if t3 == t1 {
		t.Error("an untracked addition must change the snapshot")
	}
	// The worktree itself is untouched: still dirty, nothing staged.
	out, err := exec.Command("git", "-C", wt, "status", "--porcelain").Output()
	if err != nil || !strings.Contains(string(out), "a.kt") {
		t.Errorf("worktree state disturbed: %s (%v)", out, err)
	}
}
