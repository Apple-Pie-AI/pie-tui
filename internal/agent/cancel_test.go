package agent

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeClaude installs an executable named `claude` at the front of PATH. It
// emits one stream-json line, spawns a grandchild that records its own pid and
// then loops, and loops itself - standing in for a real session that has forked
// a build or a test runner.
//
// The grandchild is the whole point: exec.CommandContext's default cancel kills
// only the direct child, so a grandchild surviving cancellation is exactly the
// bug being tested for.
func fakeClaude(t *testing.T) (grandchildPidFile string) {
	t.Helper()
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")
	script := "#!/bin/sh\n" +
		`echo '{"type":"system","subtype":"init","session_id":"sess-123"}'` + "\n" +
		`sh -c 'echo $$ > "$1"; while :; do sleep 0.2; done' sh ` + strconv.Quote(pidFile) + " &\n" +
		"while :; do sleep 0.2; done\n"
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return pidFile
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// readPidFile waits for the fake session's grandchild to announce itself.
func readPidFile(t *testing.T, path string) int {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if b, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the fake claude session never spawned its grandchild")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Cancelling the run must stop the agent AND everything it spawned. Without
// this, "Stop & clean up" removed a ticket's worktree while a claude session -
// and whatever build it had forked - kept writing into that directory.
func TestRunCancellationKillsTheProcessGroup(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no POSIX shell available")
	}
	pidFile := fakeClaude(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = Run(ctx, "irrelevant", Options{AllowedTools: "Read"})
	}()

	grandchild := readPidFile(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	cancel()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after its context was cancelled - it is still draining stdout or waiting on the process")
	}

	// The grandchild inherited claude's process group, so signalling the group
	// reaches it. If only the direct child were killed it would still be looping.
	for deadline := time.Now().Add(5 * time.Second); alive(grandchild) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(grandchild) {
		t.Errorf("grandchild pid %d survived cancellation - only the direct claude process was killed, "+
			"so the agent's build/test subtree keeps running against a worktree being deleted", grandchild)
	}
}

// An already-cancelled context must not start a session at all, and must not
// hang: the runner checks stage boundaries, but the last check and the spawn are
// not atomic, so this is the backstop.
func TestRunWithAlreadyCancelledContext(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no POSIX shell available")
	}
	pidFile := fakeClaude(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, "irrelevant", Options{AllowedTools: "Read"})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Run on a cancelled context returned no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run blocked on an already-cancelled context")
	}
	if _, err := os.Stat(pidFile); err == nil {
		t.Error("a session started despite the context already being cancelled")
	}
}
