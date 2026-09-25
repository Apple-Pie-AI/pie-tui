package proc

import (
	"os"
	"strconv"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

func writePidFile(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(paths.Root(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PidFile(), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readPid must tolerate a trailing newline. Without TrimSpace, Atoi fails, the
// single-instance guard reads as "no daemon running", and a SECOND daemon starts
// - each enforcing its own concurrency limit, and whichever exits first deletes
// the survivor's pidfile so it can never be stopped.
func TestReadPidTolerantOfWhitespace(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	cases := []struct {
		name string
		body string
		want int
		ok   bool
	}{
		{"bare", "4321", 4321, true},
		{"trailing newline", "4321\n", 4321, true},
		{"trailing CRLF", "4321\r\n", 4321, true},
		{"surrounding space", "  4321  \n", 4321, true},
		{"empty", "", 0, false},
		{"whitespace only", "\n", 0, false},
		{"not a number", "abc\n", 0, false},
		{"zero", "0\n", 0, false},
		{"negative", "-1\n", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			writePidFile(t, c.body)
			got, ok := ReadPid()
			if ok != c.ok || got != c.want {
				t.Errorf("ReadPid() = (%d, %v), want (%d, %v)", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestReadPidRoundTripsWhatStartWrites(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	writePidFile(t, strconv.Itoa(os.Getpid()))
	got, ok := ReadPid()
	if !ok || got != os.Getpid() {
		t.Fatalf("ReadPid() = (%d, %v), want (%d, true)", got, ok, os.Getpid())
	}
}

func TestReadPidMissingFile(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if got, ok := ReadPid(); ok {
		t.Errorf("ReadPid() = (%d, true) with no pidfile, want (0, false)", got)
	}
}

// pidAlive is now the single liveness implementation (start.go had its own copy
// that treated EPERM as dead, so `pie stop` and the dashboard could reach
// opposite conclusions about the same daemon). TestPidAlive in monitor_test.go
// covers the happy path; this pins the negative pids readPid can no longer emit.
func TestPidAliveRejectsNonPositivePids(t *testing.T) {
	for _, pid := range []int{0, -1, -12345} {
		if Alive(pid) {
			t.Errorf("Alive(%d) = true, want false", pid)
		}
	}
}

// `stop` must not SIGTERM a pid on liveness alone: after a SIGKILL the pidfile
// survives and macOS recycles pids aggressively.
func TestIsPieProcessRejectsUnrelatedPid(t *testing.T) {
	// pid 1 is launchd/init - definitively alive and definitively not Apple Pie.
	if !Alive(1) {
		t.Skip("pid 1 not probeable in this environment")
	}
	if IsPieProcess(1) {
		t.Error("IsPieProcess(1) = true; `stop` would signal an unrelated process")
	}
}

func TestPidAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Error("our own pid should be alive")
	}
	if Alive(0) {
		t.Error("pid 0 is not a real process")
	}
	if Alive(2_000_000_000) {
		t.Error("an absurd pid should not be alive")
	}
}
