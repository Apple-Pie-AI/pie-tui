package runner

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func leaseStore(t *testing.T) *store.Store {
	t.Helper()
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

func quiet(string, ...interface{}) {}

// swapWaitBudget narrows the AVD wait to something a test can sit through, and
// returns the restore func. The production values are 20 minutes and 5 seconds.
func swapWaitBudget(timeout, poll time.Duration) func() {
	oldT, oldP := emulatorWaitTimeout, emulatorPollInterval
	emulatorWaitTimeout, emulatorPollInterval = timeout, poll
	return func() { emulatorWaitTimeout, emulatorPollInterval = oldT, oldP }
}

// swapLeaseGrace drops the "don't second-guess a fresh lease" window so a test
// can exercise reclaim without waiting out the real two minutes.
func swapLeaseGrace(d time.Duration) func() {
	old := store.LeaseGrace
	store.LeaseGrace = d
	return func() { store.LeaseGrace = old }
}

func TestAcquireEmulatorTakesAFreeAVD(t *testing.T) {
	st := leaseStore(t)
	if !acquireEmulator(context.Background(), st, "avd", "A-1", quiet) {
		t.Fatal("acquire on a ready AVD should win immediately")
	}
	if holder, _, _ := st.ReclaimStaleLease("avd", 0, func(int) bool { return false }); holder != "A-1" {
		t.Errorf("holder = %q, want A-1", holder)
	}
}

// The deadline is the fix for the original defect: both acquire sites used to
// loop `for { … sleep 5s }` forever, so one leaked lease stalled every later
// ticket indefinitely. A waiter now gives up and the run degrades to unit tests.
func TestAcquireEmulatorGivesUpAtTheDeadline(t *testing.T) {
	st := leaseStore(t)
	// A live holder: running state, and a pid that answers (our own).
	_, _ = st.Claim("HOLD-1", "/r", "x")
	_ = st.SetState("HOLD-1", store.StateBuilding, 0)
	_ = st.SetPID("HOLD-1", 1) // pid 1 always exists
	if ok, _ := st.AcquireEmulator("avd", "HOLD-1"); !ok {
		t.Fatal("setup: holder should take the lease")
	}

	// Squeeze the wait so the test is fast; the production values are 20min/5s.
	defer swapWaitBudget(50*time.Millisecond, 10*time.Millisecond)()

	start := time.Now()
	if acquireEmulator(context.Background(), st, "avd", "B-2", quiet) {
		t.Fatal("acquired an AVD a live holder is using")
	}
	if elapsed := time.Since(start); elapsed < 40*time.Millisecond {
		t.Errorf("gave up after %s - it did not actually wait out the deadline", elapsed)
	}
}

// A holder that died without running its deferred release is reclaimed by the
// next waiter, rather than stranding the AVD forever.
func TestAcquireEmulatorReclaimsADeadHolder(t *testing.T) {
	st := leaseStore(t)
	_, _ = st.Claim("DEAD-1", "/r", "x")
	_ = st.SetState("DEAD-1", store.StateBuilding, 0) // still claims to be running…
	_ = st.SetPID("DEAD-1", 0)                        // …but nothing is driving it
	if ok, _ := st.AcquireEmulator("avd", "DEAD-1"); !ok {
		t.Fatal("setup: holder should take the lease")
	}
	defer swapWaitBudget(5*time.Second, 10*time.Millisecond)()
	defer swapLeaseGrace(0)()

	if !acquireEmulator(context.Background(), st, "avd", "B-2", quiet) {
		t.Fatal("a waiter should reclaim a lease whose driver is gone and then take it")
	}
	holder, _, _ := st.ReclaimStaleLease("avd", 0, func(int) bool { return false })
	if holder != "B-2" {
		t.Errorf("holder = %q after reclaim, want B-2", holder)
	}
}

// Cancelling the run abandons the wait; it must not sit out the full deadline.
func TestAcquireEmulatorAbortsOnCancel(t *testing.T) {
	st := leaseStore(t)
	_, _ = st.Claim("HOLD-1", "/r", "x")
	_ = st.SetState("HOLD-1", store.StateBuilding, 0)
	_ = st.SetPID("HOLD-1", 1)
	_, _ = st.AcquireEmulator("avd", "HOLD-1")
	defer swapWaitBudget(time.Hour, 10*time.Millisecond)()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	done := make(chan bool, 1)
	go func() { done <- acquireEmulator(ctx, st, "avd", "B-2", quiet) }()
	select {
	case got := <-done:
		if got {
			t.Error("a cancelled wait must not report the AVD acquired")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("acquireEmulator ignored cancellation and kept waiting")
	}
}

// waitForBoot returns whichever comes first: the boot result or cancellation.
func TestWaitForBoot(t *testing.T) {
	ready := make(chan error, 1)
	ready <- nil
	if err := waitForBoot(context.Background(), ready); err != nil {
		t.Errorf("a completed boot should report nil, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForBoot(ctx, make(chan error)); err == nil {
		t.Error("a cancelled wait should report the cancellation, not block")
	}
}
