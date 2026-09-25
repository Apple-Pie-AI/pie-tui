// Package proc owns the two process questions the rest of the tree must answer
// the same way: is that pid still alive, and how do you stop it.
//
// It also owns the ~/.pie/daemon.pid file and the liveness checks around it:
// `pie start` uses it as a single-instance guard, `pie stop` to decide whether
// to signal, and the TUI to show the daemon indicator and offer start/stop.
package proc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// Alive reports whether process pid currently exists (deterministic, via
// signal 0). EPERM means it exists but we can't signal it - still alive.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// TerminateGroup stops a process and everything it spawned, and does not return
// until it is actually gone (or grace has elapsed, after which it is SIGKILLed).
//
// The wait is the load-bearing part. `pie run` catches SIGTERM and unwinds
// deliberately - it cancels the agent, waits for claude's process group to exit,
// and hands back the shared emulator lease. A caller that removes the worktree
// the instant it signals is racing that unwind and deleting the directory out
// from under a session still writing to it.
//
// Both the group and the bare pid are signalled: TUI-launched runs are group
// leaders, but a run started by hand in a shell is not.
func TerminateGroup(pid int, grace time.Duration) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	_ = syscall.Kill(pid, syscall.SIGTERM)
	if waitGone(pid, grace) {
		return
	}
	// Grace expired: something is ignoring SIGTERM or wedged. Escalate, and wait
	// again - SIGKILL is not synchronous either, and the caller's next act is to
	// delete the directory this process is still in.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = syscall.Kill(pid, syscall.SIGKILL)
	waitGone(pid, 2*time.Second)
}

// waitGone polls until pid disappears, reporting whether it did within d.
func waitGone(pid int, d time.Duration) bool {
	for deadline := time.Now().Add(d); ; {
		if !Alive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ReadPid returns the daemon pid recorded in the pidfile.
func ReadPid() (int, bool) {
	b, err := os.ReadFile(paths.PidFile())
	if err != nil {
		return 0, false
	}
	// TrimSpace: a pidfile with a trailing newline (hand-written, or from a shell
	// redirect) must not read as "no daemon running" - that defeats the
	// single-instance guard and starts a second daemon, each enforcing its own
	// concurrency limit.
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// WritePid records this process as the running daemon.
func WritePid(pid int) error {
	return os.WriteFile(paths.PidFile(), []byte(strconv.Itoa(pid)), 0o644)
}

// RemovePid deletes the pidfile, ignoring a missing one.
func RemovePid() { _ = os.Remove(paths.PidFile()) }

// DaemonAlive reports the daemon pid and whether it is actually running.
func DaemonAlive() (int, bool) {
	if pid, ok := ReadPid(); ok && Alive(pid) {
		return pid, true
	}
	return 0, false
}

// IsPieProcess reports whether pid looks like an Apple Pie daemon rather than
// an unrelated process that inherited a recycled pid. After a SIGKILL the
// pidfile survives, and macOS recycles pids aggressively, so `stop` must not
// SIGTERM a pid on liveness alone. The test is an exact match on the installed
// binary name - every supported install path (goreleaser, install.sh, the
// Makefile, setup) produces a binary named literally `pie`, and a substring
// test would let neighbours like `copier` or `magpie` through. Fails open
// (true) when the check itself can't run, so stop still works where `ps` is
// unavailable.
func IsPieProcess(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return true
	}
	name := strings.TrimSpace(string(out))
	if name == "" {
		return true
	}
	return filepath.Base(name) == "pie"
}
