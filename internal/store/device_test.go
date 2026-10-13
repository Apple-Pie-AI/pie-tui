package store

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func deviceStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func dev(t *testing.T, st *Store) Device {
	t.Helper()
	d, err := st.ActiveDevice()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// agent registers a running ticket owned by pid.
func agent(t *testing.T, st *Store, ticket string, pid int) {
	t.Helper()
	if _, err := st.Claim(ticket, "/repo", ticket); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState(ticket, StateBuilding, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPID(ticket, pid); err != nil {
		t.Fatal(err)
	}
}

func alive(int) bool { return true }

// A fresh DB has no device; avd_name seeds it exactly once, and never over a
// device the user picked.
func TestSeedDeviceOnlyBeforeAChoice(t *testing.T) {
	st := deviceStore(t)
	if d := dev(t, st); !d.None() || d.State != DeviceOff {
		t.Fatalf("fresh device = %+v", d)
	}
	if err := st.SeedDevice("Pixel_7_API_34"); err != nil {
		t.Fatal(err)
	}
	if d := dev(t, st); d.Kind != DeviceEmulator || d.AVD != "Pixel_7_API_34" {
		t.Fatalf("seeded = %+v", d)
	}
	if err := st.UseUSB("2A281FDH3001"); err != nil {
		t.Fatal(err)
	}
	_ = st.SeedDevice("Pixel_7_API_34")
	if d := dev(t, st); d.Kind != DeviceUSB || d.State != DeviceRunning {
		t.Fatalf("seeding overwrote the user's choice: %+v", d)
	}
}

// Start is claimed once; the boot is timed for the next progress estimate.
func TestStartLifecycle(t *testing.T) {
	st := deviceStore(t)
	if err := st.UseEmulator("Pixel_7", ""); err != nil {
		t.Fatal(err)
	}
	won, _ := st.BeginStart("Pixel_7", 4242)
	lost, _ := st.BeginStart("Pixel_7", 9999)
	if !won || lost {
		t.Fatalf("BeginStart: first=%v second=%v - exactly one launcher must win", won, lost)
	}
	if d := dev(t, st); d.State != DeviceStarting || d.PID != 4242 {
		t.Fatalf("starting = %+v", d)
	}
	_, _ = st.db.Exec(`UPDATE active_device SET started_at = started_at - 30`)
	if err := st.MarkRunning("Pixel_7", "emulator-5554"); err != nil {
		t.Fatal(err)
	}
	if d := dev(t, st); d.State != DeviceRunning || d.Serial != "emulator-5554" {
		t.Fatalf("running = %+v", d)
	}
	if secs := st.BootSecs("Pixel_7"); secs < 30 {
		t.Errorf("boot secs = %d, want >= 30", secs)
	}
	if err := st.MarkOff(); err != nil {
		t.Fatal(err)
	}
	if d := dev(t, st); d.State != DeviceOff || d.PID != 0 || d.Serial != "" {
		t.Fatalf("off = %+v", d)
	}
}

// The golden rule: one holder at a time, and agents in arrival order.
func TestDeviceQueueIsFIFOAndExclusive(t *testing.T) {
	st := deviceStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	for i, tk := range []string{"T-1", "T-2", "T-3"} {
		agent(t, st, tk, 100+i)
		_, _ = st.db.Exec(`UPDATE sessions SET device_wait_since = ? WHERE ticket = ?`, 1000+i, tk)
	}
	// T-3 and T-2 can't jump the line.
	for _, tk := range []string{"T-3", "T-2"} {
		if ok, _ := st.AcquireDevice(tk, alive); ok {
			t.Fatalf("%s acquired ahead of T-1", tk)
		}
	}
	if ok, _ := st.AcquireDevice("T-1", alive); !ok {
		t.Fatal("head of the queue could not acquire a free device")
	}
	if ok, _ := st.AcquireDevice("T-2", alive); ok {
		t.Fatal("two holders at once")
	}
	if w, ok := st.NextWaiter(alive); !ok || w.Ticket != "T-2" {
		t.Fatalf("next in line = %+v, want T-2", w)
	}
	if ok, _ := st.ReleaseDevice("T-2"); ok {
		t.Fatal("a non-holder released the device")
	}
	if ok, _ := st.ReleaseDevice("T-1"); !ok {
		t.Fatal("holder could not release")
	}
	if ok, _ := st.AcquireDevice("T-2", alive); !ok {
		t.Fatal("T-2 should be next")
	}
}

// A waiter whose run died is skipped instead of blocking the queue forever.
func TestDeadWaiterIsSkipped(t *testing.T) {
	st := deviceStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	agent(t, st, "DEAD", 1)
	agent(t, st, "LIVE", 2)
	_, _ = st.db.Exec(`UPDATE sessions SET device_wait_since = 10 WHERE ticket = 'DEAD'`)
	_, _ = st.db.Exec(`UPDATE sessions SET device_wait_since = 20 WHERE ticket = 'LIVE'`)
	isAlive := func(pid int) bool { return pid == 2 }
	if ok, _ := st.AcquireDevice("LIVE", isAlive); !ok {
		t.Fatal("a dead waiter blocked the queue")
	}
	if ws, _ := st.DeviceWaiters(); len(ws) != 0 {
		t.Errorf("queue should be empty, got %+v", ws)
	}
}

// Many processes racing for a free device: exactly one wins.
func TestAcquireRace(t *testing.T) {
	st := deviceStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	agent(t, st, "T-1", 1)
	_ = st.SetDeviceWait("T-1")
	var wg sync.WaitGroup
	wins := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); ok, _ := st.AcquireDevice("T-1", alive); wins <- ok }()
	}
	wg.Wait()
	close(wins)
	n := 0
	for ok := range wins {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d winners, want exactly 1", n)
	}
}

// Take it back: the agent's hold moves to you, you are never reclaimed, and
// the device can't be switched while held.
func TestTakeBackAndReserve(t *testing.T) {
	st := deviceStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	agent(t, st, "T-1", 1)
	_ = st.SetDeviceWait("T-1")
	if ok, _ := st.AcquireDevice("T-1", alive); !ok {
		t.Fatal("acquire")
	}
	if err := st.UseEmulator("Other", ""); !errors.Is(err, ErrDeviceBusy) {
		t.Fatalf("switching a held device = %v, want ErrDeviceBusy", err)
	}
	if ok, _ := st.TakeDevice("T-1", HolderYou, PurposeTakenBack+"T-1"); !ok {
		t.Fatal("take back")
	}
	if h, ok, _ := st.ReclaimStaleHolder(0, func(int) bool { return false }); ok {
		t.Fatalf("reclaimed %q - the human's hold must never be reclaimed", h)
	}
	if ok, _ := st.ReserveDevice(""); !ok {
		t.Fatal("re-reserving your own device must succeed")
	}
	if ok, _ := st.ReleaseDevice(HolderYou); !ok {
		t.Fatal("release to agents")
	}
}

// A ticket holder whose run died is reclaimed, after the grace period.
func TestReclaimStaleTicketHolder(t *testing.T) {
	st := deviceStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	agent(t, st, "T-1", 7)
	_ = st.SetDeviceWait("T-1")
	_, _ = st.AcquireDevice("T-1", alive)
	dead := func(int) bool { return false }
	if _, ok, _ := st.ReclaimStaleHolder(3600, dead); ok {
		t.Fatal("reclaimed inside the grace period")
	}
	if h, ok, _ := st.ReclaimStaleHolder(0, alive); ok {
		t.Fatalf("reclaimed a live holder %q", h)
	}
	if h, ok, _ := st.ReclaimStaleHolder(0, dead); !ok || h != "T-1" {
		t.Fatalf("dead holder not reclaimed: %q %v", h, ok)
	}
}

// Why you hold the device survives with the hold and is cleared with it.
func TestHoldPurpose(t *testing.T) {
	st := deviceStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	if ok, _ := st.ReserveDevice(PurposeRunApp + "PLEX-12"); !ok {
		t.Fatal("reserve")
	}
	if d := dev(t, st); d.RunAppTarget() != "PLEX-12" || d.TakenBackFrom() != "" {
		t.Fatalf("purpose = %q", d.Purpose)
	}
	_ = st.SetHoldPurpose(PurposeTakenBack + "PLEX-9")
	if d := dev(t, st); d.TakenBackFrom() != "PLEX-9" {
		t.Fatalf("purpose = %q", d.Purpose)
	}
	_, _ = st.ReleaseDevice(HolderYou)
	if d := dev(t, st); d.Purpose != "" || d.RunAppTarget() != "" {
		t.Fatalf("released device kept purpose %q", d.Purpose)
	}
}
