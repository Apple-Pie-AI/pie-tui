package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func modalStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Pause and Stop write their state BEFORE signalling, on purpose: the runner
// deliberately records nothing on the cancellation path so that this value is
// the one that survives. Writing it after the kill would leave a window where
// the row still claimed to be working - and, worse, the runner's own unwind
// could land in between.
func TestPauseAndCancelWriteStateBeforeSignalling(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(monitorModel, store.Session) tea.Cmd
		want string
	}{
		{"pause", monitorModel.pauseAgent, store.StateNeedsYou},
		{"cancel", monitorModel.cancelAgent, store.StateStopped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIE_HOME", t.TempDir())
			st := modalStore(t)
			if _, err := st.Claim("M-1", "", "x"); err != nil {
				t.Fatal(err)
			}
			_ = st.SetState("M-1", store.StateWorking, 0)

			// A helper that takes ~1s to die on SIGTERM. That gives a wide window in
			// which the state write is observable while the process is still alive -
			// which is what "before" means here. Asserting only the final state would
			// pass with the two lines in either order.
			ready := filepath.Join(t.TempDir(), "ready")
			c := exec.Command("sh", "-c",
				`trap 'sleep 1; exit 0' TERM; : > "$1"; while :; do sleep 0.05; done`, "sh", ready)
			c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := c.Start(); err != nil {
				t.Skip("cannot spawn a helper process:", err)
			}
			pid := c.Process.Pid
			go func() { _ = c.Wait() }()
			t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
			waitFor(t, ready)

			m := monitorModel{store: st}
			s := store.Session{Ticket: "M-1", State: store.StateWorking, PID: pid}
			done := make(chan struct{})
			go func() { defer close(done); tc.run(m, s)() }() // tea.Cmd runs off the render loop

			// Poll for the state landing while the process is demonstrably still up.
			sawStateBeforeExit := false
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
				row, err := st.Get("M-1")
				if err == nil && row.State == tc.want && proc.Alive(pid) {
					sawStateBeforeExit = true
					break
				}
				if !proc.Alive(pid) {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			<-done

			if !sawStateBeforeExit {
				t.Errorf("%s never had %q in the store while its process was still alive - "+
					"the state is being written after the kill, which races the canceller",
					tc.name, tc.want)
			}
			got, err := st.Get("M-1")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.want {
				t.Errorf("final state = %q, want %q", got.State, tc.want)
			}
		})
	}
}

// waitFor blocks until the helper signals its trap is installed; signalling
// earlier kills it with the default disposition and proves nothing.
func waitFor(t *testing.T, path string) {
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

// The signal only fires when there is something to signal. Stop is offered on
// needs-you / failed / stopped rows, where the driver exited long ago and macOS
// may have recycled its pid onto something unrelated - and this now signals a
// whole process group and escalates to SIGKILL, which is not a thing to do on a
// guess.
func TestStopRunLeavesTerminalRowsAlone(t *testing.T) {
	// A live process standing in for whatever inherited a recycled pid.
	victim := exec.Command("sh", "-c", "sleep 10")
	victim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := victim.Start(); err != nil {
		t.Skip("cannot spawn a helper process:", err)
	}
	pid := victim.Process.Pid
	go func() { _ = victim.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// A terminal state means the recorded pid is stale by definition.
	stopRun(store.Session{Ticket: "M-2", State: store.StateNeedsYou, PID: pid})

	if !proc.Alive(pid) {
		t.Error("stopRun signalled a pid belonging to a row whose driver had already exited - " +
			"on a recycled pid that kills an unrelated process group")
	}
}

// …and it does fire for a row that really is running.
func TestStopRunTerminatesALiveRun(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	c := exec.Command("sh", "-c", `: > "$1"; while :; do sleep 0.05; done`, "sh", ready)
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		t.Skip("cannot spawn a helper process:", err)
	}
	pid := c.Process.Pid
	go func() { _ = c.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper never started")
		}
		time.Sleep(10 * time.Millisecond)
	}

	stopRun(store.Session{Ticket: "M-3", State: store.StateBuilding, PID: pid})

	if proc.Alive(pid) {
		t.Error("stopRun left a live run running")
	}
}

// Choosing "No, keep running" must close the dialog and do nothing else.
func TestConfirmDialogDecline(t *testing.T) {
	m := baseModel()
	m.confirm = confirmState{ticket: "M-4", cursor: 1} // cursor 1 = "No"
	got, cmd := m.updateConfirming(tea.KeyMsg{Type: tea.KeyEnter})
	hub := got.(monitorModel)
	if hub.confirm.ticket != "" {
		t.Errorf("dialog still open on %q", hub.confirm.ticket)
	}
	if cmd != nil {
		t.Error("declining must not run the cancel command")
	}
}
