// Launching detached child processes. Every `pie run` the hub starts - for a new
// ticket, an answer, plan feedback, resume, ship - goes through here, as does the
// daemon.
package tui

import (
	"os/exec"
	"syscall"
)

// startDetached launches c in its own process group, with no inherited stdio,
// and reaps it in the background.
//
// The reap is correctness, not hygiene. An unreaped child becomes a zombie when
// it exits, and a zombie still answers `kill(pid, 0)` - so every liveness check
// in the hub would go on believing a finished run was still going: the dashboard
// would keep it under RUNNING instead of STOPPED, `pie run` would refuse to start
// a second one, and proc.TerminateGroup would wait out its entire grace period
// and then SIGKILL a process that had already exited cleanly. The SIGKILL is the
// expensive part, because it is what stops a signalled run from unwinding - and
// unwinding is how it hands back the shared emulator lease.
//
// The goroutine does not tie the child's lifetime to the hub's: if the hub exits
// first the child is simply reparented to init, which reaps it instead. That is
// what lets the daemon and long runs outlive the TUI.
func startDetached(c *exec.Cmd) error {
	c.Stdout, c.Stderr = nil, nil
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	return nil
}
