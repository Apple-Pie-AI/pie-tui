package store

import (
	"path/filepath"
	"testing"
	"time"
)

func emuStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.RegisterEmulator("avd"); err != nil {
		t.Fatal(err)
	}
	return st
}

func stateOf(t *testing.T, st *Store) string {
	t.Helper()
	s, _, err := st.EmulatorState("avd")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The whole point of the semaphore: while one ticket holds the AVD, nothing a
// peer runner does may hand it to somebody else. MarkEmulatorReady is called on
// every path through bootOrWait, and when it was ReleaseEmulator (unguarded) a
// peer starting mid-verify flipped busy→ready and blanked the holder - so two
// runs drove one emulator.
func TestMarkReadyCannotStealALiveLease(t *testing.T) {
	st := emuStore(t)
	if err := st.MarkEmulatorReady("avd"); err != nil { // idle → ready
		t.Fatal(err)
	}
	if ok, _ := st.AcquireEmulator("avd", "A-1"); !ok {
		t.Fatal("first acquire should win")
	}

	// A peer announcing "the emulator is up" must not disturb the lease.
	if err := st.MarkEmulatorReady("avd"); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, st); got != EmulatorBusy {
		t.Fatalf("state = %q after a peer marked ready, want busy", got)
	}
	if ok, _ := st.AcquireEmulator("avd", "B-2"); ok {
		t.Fatal("a peer acquired an AVD that A-1 still holds")
	}
}

// Release is holder-scoped: you can only hand back the lease you hold.
func TestReleaseOnlyByHolder(t *testing.T) {
	st := emuStore(t)
	_ = st.MarkEmulatorReady("avd")
	if ok, _ := st.AcquireEmulator("avd", "A-1"); !ok {
		t.Fatal("acquire should win")
	}
	if ok, _ := st.ReleaseEmulator("avd", "B-2"); ok {
		t.Fatal("B-2 released a lease held by A-1")
	}
	if got := stateOf(t, st); got != EmulatorBusy {
		t.Fatalf("state = %q after a foreign release, want busy", got)
	}
	if ok, _ := st.ReleaseEmulator("avd", "A-1"); !ok {
		t.Fatal("the holder's own release should win")
	}
	if got := stateOf(t, st); got != EmulatorReady {
		t.Fatalf("state = %q after the holder released, want ready", got)
	}
}

// ReclaimStaleLease is the answer to a runner that was SIGKILLed mid-verify: its
// deferred release never ran, and without reclaim every later ticket needing
// that AVD waits forever.
func TestReclaimStaleLease(t *testing.T) {
	alive := func(int) bool { return true }
	dead := func(int) bool { return false }

	t.Run("holder still running is left alone", func(t *testing.T) {
		st := emuStore(t)
		_, _ = st.Claim("A-1", "/r", "x")
		_ = st.SetState("A-1", StateBuilding, 0)
		_ = st.SetPID("A-1", 4242)
		_ = st.MarkEmulatorReady("avd")
		_, _ = st.AcquireEmulator("avd", "A-1")

		if _, ok, err := st.ReclaimStaleLease("avd", 0, alive); err != nil || ok {
			t.Fatalf("reclaimed a live holder's lease (ok=%v err=%v)", ok, err)
		}
	})

	t.Run("running state but a dead pid is reclaimed", func(t *testing.T) {
		st := emuStore(t)
		_, _ = st.Claim("A-1", "/r", "x")
		_ = st.SetState("A-1", StateBuilding, 0)
		_ = st.SetPID("A-1", 4242)
		_ = st.MarkEmulatorReady("avd")
		_, _ = st.AcquireEmulator("avd", "A-1")

		holder, ok, err := st.ReclaimStaleLease("avd", 0, dead)
		if err != nil || !ok || holder != "A-1" {
			t.Fatalf("ReclaimStaleLease = (%q, %v, %v), want (A-1, true, nil)", holder, ok, err)
		}
		if got := stateOf(t, st); got != EmulatorReady {
			t.Fatalf("state = %q after reclaim, want ready", got)
		}
		if ok, _ := st.AcquireEmulator("avd", "B-2"); !ok {
			t.Fatal("the waiting ticket should be able to take the reclaimed AVD")
		}
	})

	t.Run("terminal state with a live pid is reclaimed", func(t *testing.T) {
		// The pid was recycled by an unrelated process - liveness alone lies.
		st := emuStore(t)
		_, _ = st.Claim("A-1", "/r", "x")
		_ = st.SetState("A-1", StateStopped, 0)
		_ = st.SetPID("A-1", 4242)
		_ = st.MarkEmulatorReady("avd")
		_, _ = st.AcquireEmulator("avd", "A-1")

		if _, ok, _ := st.ReclaimStaleLease("avd", 0, alive); !ok {
			t.Fatal("a stopped holder's lease should be reclaimable")
		}
	})

	t.Run("a lease younger than the grace period is never second-guessed", func(t *testing.T) {
		st := emuStore(t)
		_ = st.MarkEmulatorReady("avd")
		_, _ = st.AcquireEmulator("avd", "A-1") // no session row at all
		if _, ok, _ := st.ReclaimStaleLease("avd", time.Hour, dead); ok {
			t.Fatal("reclaimed a lease taken moments ago")
		}
	})

	t.Run("an unheld AVD reports nothing to reclaim", func(t *testing.T) {
		st := emuStore(t)
		_ = st.MarkEmulatorReady("avd")
		if _, ok, err := st.ReclaimStaleLease("avd", 0, dead); ok || err != nil {
			t.Fatalf("ReclaimStaleLease on a ready row = (%v, %v), want (false, nil)", ok, err)
		}
	})
}

// A fresh AVD must not look like it has been idle since 1970, or the daemon's
// idle reaper kills it on the first poll after it becomes available.
func TestMarkReadyStampsLastUsed(t *testing.T) {
	st := emuStore(t)
	if err := st.MarkEmulatorReady("avd"); err != nil {
		t.Fatal(err)
	}
	last, err := st.EmulatorLastUsed("avd")
	if err != nil {
		t.Fatal(err)
	}
	if last == 0 {
		t.Fatal("last_used_at is 0 after MarkEmulatorReady - the idle reaper would kill it immediately")
	}
}
