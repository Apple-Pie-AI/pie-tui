package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TerminateGroup must not return until the process is actually gone. The whole
// reason it exists is that "Stop & clean up" removes the ticket's worktree right
// after signalling: `pie run` now unwinds gracefully (cancels the agent, waits
// for claude, releases the emulator lease) rather than dying instantly, so a
// caller that returned immediately would delete the directory out from under a
// session still writing to it.
func TestTerminateGroupWaitsForExit(t *testing.T) {
	// The helper takes a KNOWN, measurable time to die: it catches SIGTERM and
	// exits ~600ms later. A TerminateGroup that returned as soon as it signalled
	// would come back in microseconds with the process still running, which is a
	// deterministic failure - not a race with whoever reaps the child first.
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c",
		`trap 'sleep 0.6; exit 0' TERM; : > "$1"; while :; do sleep 0.05; done`, "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot spawn a helper process:", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }() // reap, so Alive stops reporting a zombie
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitForFile(t, ready)

	start := time.Now()
	TerminateGroup(pid, 10*time.Second)
	if Alive(pid) {
		t.Error("TerminateGroup returned while the process was still alive")
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Errorf("returned after %s, but the helper takes ~600ms to exit - it did not wait", elapsed)
	}
}

// waitForFile blocks until the helper signals that its trap is installed.
// Without it, a SIGTERM sent before `trap` runs kills the helper with the
// default disposition and the test quietly proves nothing.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("helper never signalled readiness")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A process that ignores SIGTERM still has to die: the grace period expires and
// SIGKILL follows. Without that, a wedged agent would hold the cleanup forever.
func TestTerminateGroupEscalatesToKill(t *testing.T) {
	// `trap '' TERM` makes SIGTERM ignored. Two details the helper depends on:
	// the `while` loop, because with a bare `trap '' TERM; sleep 30` the shell
	// execs sleep as its last command and exec resets the disposition; and the
	// readiness file, because signalling before the trap is installed kills the
	// helper with the default disposition and quietly tests nothing.
	// The path is an argv parameter, not interpolated into the script.
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", `trap '' TERM; : > "$1"; while :; do sleep 0.2; done`, "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Skip("cannot spawn a helper process:", err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper never installed its SIGTERM trap")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	TerminateGroup(pid, 300*time.Millisecond)
	if Alive(pid) {
		t.Error("a SIGTERM-ignoring process survived TerminateGroup")
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("returned after %s - it skipped the grace period instead of waiting it out", elapsed)
	}
}

// Guard the degenerate input: syscall.Kill(0, …) signals the caller's ENTIRE
// process group, so a zero pid must be a no-op rather than self-immolation.
func TestTerminateGroupIgnoresZeroPID(t *testing.T) {
	done := make(chan struct{})
	go func() {
		TerminateGroup(0, time.Second)
		TerminateGroup(-1, time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("TerminateGroup(0) blocked - it must return immediately")
	}
}
