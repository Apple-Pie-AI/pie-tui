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
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// changeWorktree builds a real repo worktree with one commit and an
// uncommitted edit - the state the pipeline is in at the park point.
func changeWorktree(t *testing.T) string {
	t.Helper()
	wt := paths.WorktreeFor("", "C-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "b")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("the change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt
}

// The park persists the in-memory report to BOTH the worktree and the archive
// (finishShip normally does this; skipping it would strand the approve process
// and the change screen), records the fingerprint, and sets the state.
func TestParkChangeReviewPersistsReportAndFingerprint(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	wt := changeWorktree(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("C-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}

	task := Task{Ticket: "C-1", Repo: &config.Repo{Path: "/repo"}, Cfg: &config.Config{}, Store: st}
	var gotState string
	report := &agent.Report{Status: "ready_for_build", Summary: "did the thing", PRBody: "# template body"}
	out := parkChangeReview(task, Hooks{OnState: func(s string, _ int) { gotState = s }}, wt, report, 1)

	if out.State != store.StateChangeReview || gotState != store.StateChangeReview {
		t.Fatalf("state = %q/%q, want change-review", out.State, gotState)
	}
	onDisk, err := agent.ReadReport(wt)
	if err != nil || onDisk.PRBody != "# template body" {
		t.Fatalf("report.json in the worktree = %+v (%v), want the adopted report", onDisk, err)
	}
	if _, err := os.Stat(paths.ReportFor("C-1")); err != nil {
		t.Fatalf("report not archived: %v", err)
	}
	sess, err := st.Get("C-1")
	if err != nil || sess == nil {
		t.Fatal(err)
	}
	if sess.ChangeFingerprint == "" || !strings.Contains(sess.ChangeFingerprint, ":") {
		t.Fatalf("fingerprint = %q, want head:hash", sess.ChangeFingerprint)
	}
	if sess.ChangeRound != 1 {
		t.Fatalf("round = %d, want 1", sess.ChangeRound)
	}
	if sess.ChangeError != "" {
		t.Fatalf("a fresh park must clear the approve error, got %q", sess.ChangeError)
	}
}

// A fresh round-1 park must clear any round tree left over from an earlier,
// unrelated change-review cycle on this same ticket (one that already
// shipped a PR, then came back to the gate for a new, separate change) - the
// TUI's diff view (internal/tui's changeDiffCmd) trusts a non-empty round
// tree as "changes since your feedback", so a stale one hides the real diff
// behind a tiny, unrelated one.
func TestParkChangeReviewClearsAStaleRoundTreeOnAFreshPark(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	wt := changeWorktree(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("C-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	// Simulate the leftover from an earlier change-review cycle that reached
	// round 2+ and then shipped: a stale round tree still on record.
	if err := st.SetChangeRoundTree("C-1", "deadbeef"); err != nil {
		t.Fatal(err)
	}

	task := Task{Ticket: "C-1", Repo: &config.Repo{Path: "/repo"}, Cfg: &config.Config{}, Store: st}
	parkChangeReview(task, Hooks{}, wt, &agent.Report{}, 1)

	sess, err := st.Get("C-1")
	if err != nil || sess == nil {
		t.Fatal(err)
	}
	if sess.ChangeRoundTree != "" {
		t.Fatalf("round tree = %q after a fresh round-1 park, want cleared", sess.ChangeRoundTree)
	}
}

// The gate condition's truth table: on + real run + no PR parks; a dry run,
// a disabled gate, or an existing PR does not.
func TestShouldParkChange(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("C-1", "/r", "x"); err != nil {
		t.Fatal(err)
	}

	base := Task{Ticket: "C-1", Store: st, ReviewChange: true}
	if !shouldParkChange(base) {
		t.Error("gate on, no PR, real run: must park")
	}
	off := base
	off.ReviewChange = false
	if shouldParkChange(off) {
		t.Error("gate off: must not park")
	}
	dry := base
	dry.DryRun = true
	if shouldParkChange(dry) {
		t.Error("dry run: must not park (nothing to gate)")
	}
	if err := st.SetFields("C-1", "b", "", "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if shouldParkChange(base) {
		t.Error("a ticket with a PR must never enter the gate")
	}
}

// The gate accepts only a change-review park (or this gate's own needs-you
// retries) behind an approve or rework run.
func TestChangeGateOK(t *testing.T) {
	cases := []struct {
		prior, flow string
		want        bool
	}{
		// Every idle state the change screen opens for passes: the screen
		// itself is the review, and --resume was always ungated from the
		// parked states anyway.
		{store.StateChangeReview, "", true},
		{store.StateCheckedOut, "", true},
		{store.StateNeedsYou, "ship-change", true},
		{store.StateNeedsYou, "rework-change", true},
		{store.StateNeedsYou, "", true},
		{store.StateNeedsYou, "ship-comments", true},
		{store.StateFailed, "", true},
		{store.StateReview, "", true},
		// Refused: no reviewable row behind the run.
		{store.StateQueued, "", false},
		{store.StateWorking, "", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := changeGateOK(c.prior, c.flow); got != c.want {
			t.Errorf("changeGateOK(%q, %q) = %v, want %v", c.prior, c.flow, got, c.want)
		}
	}
}

// The round label reads naturally in logs.
func TestChangeRoundLabel(t *testing.T) {
	if got := changeRoundLabel(1); got != "" {
		t.Errorf("round 1 label = %q, want empty", got)
	}
	if got := changeRoundLabel(3); got != fmt.Sprintf(" (round %d)", 3) {
		t.Errorf("round 3 label = %q", got)
	}
}
