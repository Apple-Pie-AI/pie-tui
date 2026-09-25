package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func daemonStore(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir()) // logf writes to ~/.pie/daemon.log
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.RegisterEmulator("avd"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkEmulatorReady("avd"); err != nil {
		t.Fatal(err)
	}
	return st
}

// swapLeaseGrace drops the "don't second-guess a fresh lease" window. Without
// it a just-taken lease is never reclaimed and these tests pass vacuously.
func swapLeaseGrace(d time.Duration) func() {
	old := store.LeaseGrace
	store.LeaseGrace = d
	return func() { store.LeaseGrace = old }
}

func busyState(t *testing.T, st *store.Store) string {
	t.Helper()
	s, _, err := st.EmulatorState("avd")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The daemon is the only reclaimer when nobody is waiting. A run killed with
// SIGKILL never reaches its deferred release, and the idle reaper below only
// looks at `ready` rows - so before this existed, a busy row with a dead holder
// was invisible to the janitor and every later ticket queued behind it forever.
func TestReclaimStaleLeasesFreesADeadHolder(t *testing.T) {
	st := daemonStore(t)
	_, _ = st.Claim("DEAD-1", "/r", "x")
	_ = st.SetState("DEAD-1", store.StateBuilding, 0) // still claims to be running…
	_ = st.SetPID("DEAD-1", 0)                        // …but nothing is driving it
	if ok, _ := st.AcquireEmulator("avd", "DEAD-1"); !ok {
		t.Fatal("setup: holder should take the lease")
	}
	defer swapLeaseGrace(0)()

	reclaimStaleLeases(st, &config.Config{AVDName: "avd"})

	if got := busyState(t, st); got != store.EmulatorReady {
		t.Errorf("state = %q after reclaim, want ready - the AVD is still stranded", got)
	}
}

// …and it must not touch a lease whose holder is alive and working.
func TestReclaimStaleLeasesLeavesALiveHolder(t *testing.T) {
	st := daemonStore(t)
	_, _ = st.Claim("LIVE-1", "/r", "x")
	_ = st.SetState("LIVE-1", store.StateBuilding, 0)
	_ = st.SetPID("LIVE-1", 1) // pid 1 always exists
	_, _ = st.AcquireEmulator("avd", "LIVE-1")

	reclaimStaleLeases(st, &config.Config{AVDName: "avd"})

	if got := busyState(t, st); got != store.EmulatorBusy {
		t.Errorf("state = %q, want busy - the daemon stole a lease from a running ticket", got)
	}
}

// No AVD configured means there is nothing to reclaim; it must not panic or
// touch anything.
func TestReclaimStaleLeasesWithoutAnAVD(t *testing.T) {
	st := daemonStore(t)
	reclaimStaleLeases(st, &config.Config{})
}

// Reclaiming must NOT refresh last_used_at. It runs immediately before the idle
// reaper, and a fresh stamp would tell that reaper the AVD had just been used -
// buying an abandoned emulator a whole extra idle timeout instead of shutting it
// down on the same poll.
func TestReclaimDoesNotRefreshTheIdleTimer(t *testing.T) {
	st := daemonStore(t)
	before, err := st.EmulatorLastUsed("avd")
	if err != nil {
		t.Fatal(err)
	}
	// last_used_at has one-second granularity, so setup and reclaim must land in
	// different seconds or a re-stamp is invisible and this test passes whatever
	// the code does. (Verified: without the wait, swapping the reclaim back to
	// ReleaseEmulator - which does re-stamp - still passed.)
	time.Sleep(1100 * time.Millisecond)

	_, _ = st.Claim("DEAD-1", "/r", "x")
	_ = st.SetState("DEAD-1", store.StateStopped, 0)
	_, _ = st.AcquireEmulator("avd", "DEAD-1")
	defer swapLeaseGrace(0)()

	reclaimStaleLeases(st, &config.Config{AVDName: "avd"})
	if got := busyState(t, st); got != store.EmulatorReady {
		t.Fatalf("setup: reclaim did not run (state %q), so this test would pass vacuously", got)
	}

	after, err := st.EmulatorLastUsed("avd")
	if err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("last_used_at moved %d → %d; the idle reaper will now wait a full "+
			"timeout before killing an emulator nobody is using", before, after)
	}
}

// mergeCheckable decides which rows the daemon asks GitHub about: every idle
// row with a PR - not just review, which left needs-you/failed/stopped rows
// with merged PRs parked forever - and never an active row, whose state has
// exactly one writer (the live pipeline).
func TestMergeCheckable(t *testing.T) {
	idle := []string{
		store.StateReview, store.StateNeedsYou, store.StateAwaiting,
		store.StateFailed, store.StatePlanReview, store.StateFixReview,
		store.StateStopped, store.StateCheckedOut, store.StateQueued,
	}
	for _, state := range idle {
		s := store.Session{State: state, PRURL: "http://pr/1", Repo: "/r"}
		want := !store.IsActive(state) // queued is active - the rest are checkable
		if got := mergeCheckable(s); got != want {
			t.Errorf("mergeCheckable(%s with PR) = %v, want %v", state, got, want)
		}
	}
	for _, state := range []string{store.StatePlanning, store.StateWorking, store.StateBuilding, store.StateTesting, store.StateReviewing} {
		if mergeCheckable(store.Session{State: state, PRURL: "http://pr/1", Repo: "/r"}) {
			t.Errorf("mergeCheckable(%s) = true - active rows are the pipeline's alone", state)
		}
	}
	for _, state := range []string{store.StateMerged, store.StateClosed} {
		if mergeCheckable(store.Session{State: state, PRURL: "http://pr/1", Repo: "/r"}) {
			t.Errorf("mergeCheckable(%s) = true - already resolved, nothing to ask", state)
		}
	}
	if mergeCheckable(store.Session{State: store.StateNeedsYou, Repo: "/r"}) {
		t.Error("mergeCheckable with no PRURL = true")
	}
	if mergeCheckable(store.Session{State: store.StateNeedsYou, PRURL: "http://pr/1"}) {
		t.Error("mergeCheckable with no Repo = true")
	}
}

// A merged PR must not cost uncommitted hand work: a dirty worktree survives
// the reclaim (with a log line), and the state still flips.
func TestCleanupSparesDirtyWorktrees(t *testing.T) {
	// exercised through the git helpers directly: HasChanges is the gate.
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	if git.HasChanges(dir) {
		t.Fatal("clean worktree misread as dirty")
	}
	if err := os.WriteFile(filepath.Join(dir, "wip.txt"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !git.HasChanges(dir) {
		t.Fatal("dirty worktree misread as clean - the daemon would reclaim it")
	}
}

// Rows can reach merged/closed WITHOUT the daemon now (the TUI's PR-update
// check, commentsPreflight) - and mergeCheckable deliberately skips resolved
// rows, so without this sweep their worktrees and branches would leak
// forever. The sweep reclaims any resolved row still recording a worktree,
// then clears the fields so the next poll does nothing.
func TestSweepReclaimsResolvedRows(t *testing.T) {
	st := daemonStore(t)

	repo := t.TempDir()
	must := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	must("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	must("add", ".")
	must("commit", "-qm", "base")
	must("branch", "-M", "main")
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare origin: %v: %s", err, out)
	}
	must("remote", "add", "origin", origin)
	must("push", "-qu", "origin", "main")
	wt := filepath.Join(t.TempDir(), "CLO-1")
	const branch = "ai/clo-1-done"
	if err := git.CreateWorktree(repo, wt, branch, ""); err != nil {
		t.Fatal(err)
	}

	_, _ = st.Claim("CLO-1", repo, "x")
	_ = st.SetState("CLO-1", store.StateClosed, 0)
	_ = st.SetFields("CLO-1", branch, wt, "http://pr/1")

	cleanupResolved(st)

	if _, err := os.Stat(filepath.Join(wt, ".git")); err == nil {
		t.Error("closed row's worktree still on disk after the sweep")
	}
	if git.RefExists(repo, branch) {
		t.Error("closed row's local branch survived the sweep")
	}
	s, err := st.Get("CLO-1")
	if err != nil {
		t.Fatal(err)
	}
	if s.Worktree != "" || s.Branch != "" {
		t.Fatalf("worktree/branch = %q/%q - fields must clear so the sweep is idempotent", s.Worktree, s.Branch)
	}
	if s.State != store.StateClosed {
		t.Fatalf("state = %q - the sweep must not touch state", s.State)
	}

	// Second poll: nothing left to do, and nothing blows up.
	cleanupResolved(st)
}

// A dirty worktree vetoes the sweep the same way it vetoes the detection
// path: fields stay recorded so a later poll can retry after a human cleans up.
func TestSweepSparesDirtyResolvedRows(t *testing.T) {
	st := daemonStore(t)

	repo := t.TempDir()
	must := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	must("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	must("add", ".")
	must("commit", "-qm", "base")
	must("branch", "-M", "main")
	origin := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare origin: %v: %s", err, out)
	}
	must("remote", "add", "origin", origin)
	must("push", "-qu", "origin", "main")
	wt := filepath.Join(t.TempDir(), "CLO-2")
	if err := git.CreateWorktree(repo, wt, "ai/clo-2-wip", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = st.Claim("CLO-2", repo, "x")
	_ = st.SetState("CLO-2", store.StateMerged, 0)
	_ = st.SetFields("CLO-2", "ai/clo-2-wip", wt, "http://pr/2")

	cleanupResolved(st)

	if _, err := os.Stat(filepath.Join(wt, "wip.txt")); err != nil {
		t.Fatal("uncommitted work deleted by the sweep")
	}
	s, _ := st.Get("CLO-2")
	if s.Worktree == "" {
		t.Fatal("fields cleared for a spared worktree - the retry path is lost")
	}
}
