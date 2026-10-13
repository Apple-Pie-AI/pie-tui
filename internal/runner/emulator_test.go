// Tests for the one-device rule as the runner sees it: the active device only,
// windowed emulators, the user's other devices never touched, agents in
// queue order, and every failure degrading to unit tests.
package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func quiet(string, ...interface{}) {}

func deviceTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// swapWaitBudget narrows the device wait to something a test can sit through.
func swapWaitBudget(timeout, poll time.Duration) func() {
	oldT, oldP := emulatorWaitTimeout, emulatorPollInterval
	emulatorWaitTimeout, emulatorPollInterval = timeout, poll
	return func() { emulatorWaitTimeout, emulatorPollInterval = oldT, oldP }
}

// fakeSDK builds an SDK dir whose adb lists the emulators named in
// $FAKE_EMUS ("serial=AVD" pairs) plus $FAKE_USB lines, and whose emulator
// binary records its argv and then "boots": it appends its AVD as
// emulator-5560 so a later `adb devices` sees it.
func fakeSDK(t *testing.T) (sdk, log string) {
	t.Helper()
	sdk = t.TempDir()
	log = filepath.Join(sdk, "argv.log")
	state := filepath.Join(sdk, "booted")
	adb := `#!/bin/sh
echo "adb $*" >> "` + log + `"
emus="$FAKE_EMUS"; [ -f "` + state + `" ] && emus="$emus $(cat "` + state + `")"
if [ "$1" = "devices" ]; then
  echo "List of devices attached"
  for e in $emus; do echo "${e%%=*} device model:sdk_gphone64"; done
  [ -n "$FAKE_USB" ] && echo "$FAKE_USB"
  exit 0
fi
if [ "$1" = "-s" ]; then
  serial="$2"; shift 2
  case "$*" in
  "emu avd name") for e in $emus; do [ "${e%%=*}" = "$serial" ] && printf '%s\nOK\n' "${e#*=}"; done ;;
  "shell getprop sys.boot_completed") echo 1 ;;
  esac
fi
`
	emu := `#!/bin/sh
if [ "$1" = "-list-avds" ]; then [ -n "$FAKE_AVDS" ] && echo "$FAKE_AVDS"; exit 0; fi
echo "emulator $*" >> "` + log + `"
avd="$2"
echo "emulator-5560=$avd" > "` + state + `"
sleep 1
`
	for path, body := range map[string]string{
		filepath.Join(sdk, "platform-tools", "adb"): adb,
		filepath.Join(sdk, "emulator", "emulator"):  emu,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return sdk, log
}

func deviceTask(t *testing.T, st *store.Store, sdk string) Task {
	t.Setenv("PIE_HOME", t.TempDir())
	_ = os.MkdirAll(filepath.Join(os.Getenv("PIE_HOME"), "logs"), 0o755)
	return Task{Ticket: "T-1", Store: st, Cfg: &config.Config{AndroidSDKPath: sdk}}
}

func readLog(path string) string { b, _ := os.ReadFile(path); return string(b) }

// The regression this whole change exists for: the user's own emulator
// (another AVD, emulator-5554) is running next to the active one. The agent
// gets ONLY the active AVD's serial, and nothing launches.
func TestEnsureDeviceAdoptsOnlyTheActiveAVD(t *testing.T) {
	sdk, log := fakeSDK(t)
	t.Setenv("FAKE_EMUS", "emulator-5554=Users_Phone emulator-5556=Pixel_7")
	st := deviceTestStore(t)
	_ = st.UseEmulator("Pixel_7", "")
	serial, err := ensureDeviceRunning(context.Background(), deviceTask(t, st, sdk), quiet)
	if err != nil || serial != "emulator-5556" {
		t.Fatalf("serial = %q, %v; want emulator-5556", serial, err)
	}
	if strings.Contains(readLog(log), "emulator -avd") {
		t.Errorf("a running active emulator must be adopted, not relaunched:\n%s", readLog(log))
	}
	if d, _ := st.ActiveDevice(); d.State != store.DeviceRunning || d.Serial != "emulator-5556" {
		t.Errorf("device = %+v", d)
	}
}

// Off → launched WINDOWED (no -no-window), even with the user's other
// emulator running - it is not adopted in its place.
func TestEnsureDeviceLaunchesTheActiveAVDWindowed(t *testing.T) {
	sdk, log := fakeSDK(t)
	t.Setenv("FAKE_EMUS", "emulator-5554=Users_Phone")
	st := deviceTestStore(t)
	_ = st.UseEmulator("Pixel_7", "")
	serial, err := ensureDeviceRunning(context.Background(), deviceTask(t, st, sdk), quiet)
	if err != nil || serial != "emulator-5560" {
		t.Fatalf("serial = %q, %v; want the newly booted emulator-5560", serial, err)
	}
	l := readLog(log)
	if !strings.Contains(l, "emulator -avd Pixel_7") || strings.Contains(l, "-no-window") {
		t.Errorf("want a windowed launch of Pixel_7:\n%s", l)
	}
}

// The store says running but the emulator is gone (closed by hand): the
// stale row is reset and the device relaunched.
func TestEnsureDeviceRecoversAStaleRunningRow(t *testing.T) {
	sdk, log := fakeSDK(t)
	st := deviceTestStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5556") // recorded running…
	serial, err := ensureDeviceRunning(context.Background(), deviceTask(t, st, sdk), quiet)
	if err != nil || serial != "emulator-5560" { // …but nothing is attached
		t.Fatalf("serial = %q, %v", serial, err)
	}
	if !strings.Contains(readLog(log), "emulator -avd Pixel_7") {
		t.Error("a gone emulator must be relaunched")
	}
}

func TestEnsureDeviceUSB(t *testing.T) {
	sdk, _ := fakeSDK(t)
	st := deviceTestStore(t)
	_ = st.UseUSB("2A281FDH3001")
	task := deviceTask(t, st, sdk)

	t.Setenv("FAKE_USB", "2A281FDH3001 device usb:1-1 model:Pixel_7")
	if serial, err := ensureDeviceRunning(context.Background(), task, quiet); err != nil || serial != "2A281FDH3001" {
		t.Fatalf("ready phone: %q, %v", serial, err)
	}
	t.Setenv("FAKE_USB", "2A281FDH3001 unauthorized usb:1-1")
	if _, err := ensureDeviceRunning(context.Background(), task, quiet); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("unauthorized phone: %v", err)
	}
	t.Setenv("FAKE_USB", "")
	if _, err := ensureDeviceRunning(context.Background(), task, quiet); err == nil || !strings.Contains(err.Error(), "not connected") {
		t.Fatalf("unplugged phone: %v", err)
	}
}

// With no device chosen, config avd_name seeds it; with nothing at all, the
// run degrades to unit tests.
func TestNoDeviceDegradesToUnitTests(t *testing.T) {
	sdk, _ := fakeSDK(t)
	st := deviceTestStore(t)
	task := deviceTask(t, st, sdk)
	serial, release := acquireForRun(context.Background(), task, &agent.Plan{NeedsEmulator: true}, quiet)
	defer release()
	if serial != "" {
		t.Fatalf("serial = %q, want unit tests only", serial)
	}
	if ws, _ := st.DeviceWaiters(); len(ws) != 0 {
		t.Errorf("a run that gave up must leave the queue: %+v", ws)
	}
	if _, err := activeOrSeed(st, task, emulator.ResolvePaths(sdk), quiet); err != errNoDevice {
		t.Errorf("err = %v, want errNoDevice", err)
	}
}

// One agent at a time: while another live ticket holds the device, this one
// waits its budget out and falls back to unit tests - leaving the queue.
func TestClaimDeviceWaitsForTheHolder(t *testing.T) {
	defer swapWaitBudget(50*time.Millisecond, 5*time.Millisecond)()
	sdk, _ := fakeSDK(t)
	t.Setenv("FAKE_EMUS", "emulator-5556=Pixel_7")
	st := deviceTestStore(t)
	_ = st.UseEmulator("Pixel_7", "emulator-5556")
	_, _ = st.Claim("HOLD-1", "/r", "x")
	_ = st.SetState("HOLD-1", store.StateBuilding, 0)
	_ = st.SetPID("HOLD-1", os.Getpid())
	_ = st.SetDeviceWait("HOLD-1")
	if ok, _ := st.AcquireDevice("HOLD-1", func(int) bool { return true }); !ok {
		t.Fatal("setup")
	}
	task := deviceTask(t, st, sdk)
	_, _ = st.Claim("T-1", "/r", "x")
	_ = st.SetState("T-1", store.StateBuilding, 0)
	_ = st.SetPID("T-1", os.Getpid())

	serial, release := claimDevice(context.Background(), task, startDeviceEarly(context.Background(), task, quiet), quiet)
	release()
	if serial != "" {
		t.Fatalf("got the device %q while HOLD-1 held it", serial)
	}
	if d, _ := st.ActiveDevice(); d.Holder != "HOLD-1" {
		t.Errorf("holder = %q", d.Holder)
	}

	// Once HOLD-1 releases, T-1 gets it and its release frees it again.
	_, _ = st.ReleaseDevice("HOLD-1")
	serial, release = claimDevice(context.Background(), task, startDeviceEarly(context.Background(), task, quiet), quiet)
	if serial != "emulator-5556" {
		t.Fatalf("serial = %q, want emulator-5556", serial)
	}
	if d, _ := st.ActiveDevice(); d.Holder != "T-1" {
		t.Errorf("holder = %q, want T-1", d.Holder)
	}
	release()
	if d, _ := st.ActiveDevice(); d.Holder != "" {
		t.Errorf("release left holder %q", d.Holder)
	}
}

// The serial reaches the verify agent's environment.
func TestSerialEnv(t *testing.T) {
	if got := serialEnv("emulator-5556"); len(got) != 1 || got[0] != "ANDROID_SERIAL=emulator-5556" {
		t.Errorf("serialEnv = %v", got)
	}
	if serialEnv("") != nil {
		t.Error("no device, no ANDROID_SERIAL")
	}
}

// No device chosen: config avd_name (or the first AVD) seeds it once.
func TestSeedFromAVDName(t *testing.T) {
	sdk, _ := fakeSDK(t)
	st := deviceTestStore(t)
	task := deviceTask(t, st, sdk)
	t.Setenv("FAKE_AVDS", "First_AVD\nSecond_AVD")
	d, err := activeOrSeed(st, task, emulator.ResolvePaths(sdk), quiet)
	if err != nil || d.AVD != "First_AVD" {
		t.Fatalf("auto-seed = %+v, %v", d, err)
	}
	st2 := deviceTestStore(t)
	task.Store, task.Cfg.AVDName = st2, "Configured"
	if d, _ := activeOrSeed(st2, task, emulator.ResolvePaths(sdk), quiet); d.AVD != "Configured" {
		t.Fatalf("avd_name seed = %+v", d)
	}
}
