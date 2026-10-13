package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
	if err := st.UseEmulator("avd", "emulator-5554"); err != nil {
		t.Fatal(err)
	}
	return st
}

// holdDevice queues ticket (driven by pid) and gives it the device.
func holdDevice(t *testing.T, st *store.Store, ticket string, pid int) {
	t.Helper()
	_, _ = st.Claim(ticket, "/r", "x")
	_ = st.SetState(ticket, store.StateBuilding, 0)
	_ = st.SetPID(ticket, pid)
	_ = st.SetDeviceWait(ticket)
	if ok, _ := st.AcquireDevice(ticket, func(int) bool { return true }); !ok {
		t.Fatal("setup: ticket should take the device")
	}
}

func holder(t *testing.T, st *store.Store) string {
	t.Helper()
	d, err := st.ActiveDevice()
	if err != nil {
		t.Fatal(err)
	}
	return d.Holder
}

// The daemon is the reclaimer of last resort: a run killed with SIGKILL never
// reaches its deferred release, and without this the next ticket in line
// would wait out its whole budget behind a dead holder.
func TestReclaimDeviceFreesADeadHolder(t *testing.T) {
	st := daemonStore(t)
	holdDevice(t, st, "DEAD-1", 0) // claims to be running, nothing drives it
	old := store.LeaseGrace
	store.LeaseGrace = 0
	defer func() { store.LeaseGrace = old }()
	reclaimDevice(st)
	if h := holder(t, st); h != "" {
		t.Fatalf("holder = %q, want the device freed", h)
	}
}

func TestReclaimDeviceLeavesALiveHolder(t *testing.T) {
	st := daemonStore(t)
	holdDevice(t, st, "LIVE-1", os.Getpid())
	reclaimDevice(st)
	if h := holder(t, st); h != "LIVE-1" {
		t.Fatalf("holder = %q, a live run must keep the device", h)
	}
}

// The daemon never stops the device - the user is in control of emulators -
// and a human's hold is never reclaimed.
func TestDaemonLeavesTheDeviceAlone(t *testing.T) {
	st := daemonStore(t)
	if ok, _ := st.ReserveDevice(); !ok {
		t.Fatal("reserve")
	}
	reclaimDevice(st)
	d, _ := st.ActiveDevice()
	if d.State != store.DeviceRunning || d.Holder != store.HolderYou {
		t.Fatalf("device = %+v, the daemon must not touch it", d)
	}
}

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
