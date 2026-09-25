package tui

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
)

// The reap is the point. An unreaped child becomes a zombie when it exits, and a
// zombie still answers kill(pid, 0) - so the dashboard would keep a finished run
// under RUNNING, `pie run` would refuse to start another, and TerminateGroup
// would sit out its whole grace period before SIGKILLing a process that had
// already exited cleanly.
func TestStartDetachedReapsItsChild(t *testing.T) {
	c := exec.Command("sh", "-c", "exit 0")
	if err := startDetached(c); err != nil {
		t.Skip("cannot spawn a helper process:", err)
	}
	pid := c.Process.Pid

	// Once reaped the pid stops answering. Without the reap it answers forever
	// (as a zombie), so this loop would time out.
	for deadline := time.Now().Add(5 * time.Second); proc.Alive(pid); {
		if time.Now().After(deadline) {
			t.Fatal("child still answers kill(pid,0) after exiting - it was never reaped, " +
				"so every liveness check in the hub will believe it is still running")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// It must also detach: its own process group, so signalling the run does not
// travel back up to the hub, and no inherited stdio to corrupt the TUI's screen.
func TestStartDetachedIsolatesTheChild(t *testing.T) {
	c := exec.Command("sh", "-c", "sleep 5")
	if err := startDetached(c); err != nil {
		t.Skip("cannot spawn a helper process:", err)
	}
	pid := c.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	if c.Stdout != nil || c.Stderr != nil {
		t.Error("child inherited stdio; its output would land on top of the TUI")
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid != pid {
		t.Errorf("pgid = %d, want %d - the child is not a group leader, so killing "+
			"its group would signal the hub too", pgid, pid)
	}
}
