package emulator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real-world `adb devices -l`: a daemon notice, an emulator, a USB phone,
// an unauthorized phone, a two-word state, and a model with underscores.
const devicesL = `* daemon not running; starting now at tcp:5037
* daemon started successfully
List of devices attached
emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:1
2A281FDH3001           device usb:1-1 product:panther model:Pixel_7 device:panther transport_id:3
R58N12ABCD             unauthorized usb:1-2 transport_id:4
0123456789             no permissions (user in plugdev group); see [http://developer.android.com/tools/device.html] usb:3-1
emulator-5556          offline transport_id:5

`

func TestParseDevicesL(t *testing.T) {
	got := parseDevicesL(devicesL)
	want := []Device{
		{Serial: "emulator-5554", State: "device", Kind: KindEmulator, Model: "sdk gphone64 arm64"},
		{Serial: "2A281FDH3001", State: "device", Kind: KindUSB, Model: "Pixel 7"},
		{Serial: "R58N12ABCD", State: "unauthorized", Kind: KindUSB},
		{Serial: "0123456789", State: "no permissions (user in plugdev group); see [http", Kind: KindUSB},
		{Serial: "emulator-5556", State: "offline", Kind: KindEmulator},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d devices, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if i == 3 {
			// "no permissions" is free text; only its head matters.
			if !strings.HasPrefix(got[i].State, "no permissions") || got[i].Ready() {
				t.Errorf("device %d state = %q", i, got[i].State)
			}
			continue
		}
		if got[i] != want[i] {
			t.Errorf("device %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if !got[0].Ready() || got[2].Ready() || got[4].Ready() {
		t.Error("Ready must be true only for state device")
	}
}

// stubSDK writes fake adb/emulator scripts. adb answers per serial from
// $STUB_AVD_<serial-with-underscores> and logs every argv to $STUB_LOG.
func stubSDK(t *testing.T, devices string) SDKPaths {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "argv.log")
	t.Setenv("STUB_LOG", log)
	adb := `#!/bin/sh
echo "adb $*" >> "$STUB_LOG"
if [ "$1" = "devices" ]; then cat <<'EOF'
` + devices + `EOF
exit 0; fi
if [ "$1" = "-s" ]; then
  serial="$2"; shift 2
  key=$(echo "$serial" | tr '-' '_')
  case "$*" in
  "emu avd name") eval "v=\$STUB_AVD_$key"; [ -n "$v" ] && printf '%s\nOK\n' "$v" ;;
  "shell getprop sys.boot_completed") eval "v=\$STUB_BOOT_$key"; echo "${v:-1}" ;;
  "emu kill") echo "OK: killing emulator, bye bye" ;;
  esac
fi
`
	emu := `#!/bin/sh
echo "emulator $*" >> "$STUB_LOG"
[ -n "$STUB_EMU_EXIT" ] && { echo "FATAL | $STUB_EMU_EXIT"; exit 1; }
exit 0
`
	for name, body := range map[string]string{"adb": adb, "emulator": emu} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return SDKPaths{ADB: filepath.Join(dir, "adb"), Emulator: filepath.Join(dir, "emulator")}
}

func stubLog(t *testing.T) string {
	b, _ := os.ReadFile(os.Getenv("STUB_LOG"))
	return string(b)
}

const twoEmulators = "List of devices attached\nemulator-5554 device model:sdk_gphone64\nemulator-5556 device model:sdk_gphone64\n"

// SerialForAVD maps an AVD to ITS emulator only - the other running emulator
// belongs to the user and must never be adopted.
func TestSerialForAVDPicksOnlyItsOwnEmulator(t *testing.T) {
	p := stubSDK(t, twoEmulators)
	t.Setenv("STUB_AVD_emulator_5554", "Users_Phone")
	t.Setenv("STUB_AVD_emulator_5556", "Pixel_7_API_34")
	serial, ok := SerialForAVD(p, "Pixel_7_API_34")
	if !ok || serial != "emulator-5556" {
		t.Fatalf("SerialForAVD = %q, %v; want emulator-5556", serial, ok)
	}
	if _, ok := SerialForAVD(p, "Pixel_Tablet"); ok {
		t.Error("an AVD that isn't running must not match another emulator")
	}
	if !strings.Contains(stubLog(t), "adb -s emulator-5556 emu avd name") {
		t.Errorf("AVD names must be read per serial:\n%s", stubLog(t))
	}
}

func TestWaitAVDReadyWaitsForBootOfItsSerial(t *testing.T) {
	old := waitPoll
	waitPoll = 10 * time.Millisecond
	defer func() { waitPoll = old }()
	p := stubSDK(t, twoEmulators)
	t.Setenv("STUB_AVD_emulator_5556", "Pixel_7_API_34")
	t.Setenv("STUB_BOOT_emulator_5556", "0")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := WaitAVDReady(ctx, p, "Pixel_7_API_34"); err == nil {
		t.Fatal("not booted yet: WaitAVDReady must keep waiting until ctx ends")
	}
	t.Setenv("STUB_BOOT_emulator_5556", "1")
	serial, err := WaitAVDReady(context.Background(), p, "Pixel_7_API_34")
	if err != nil || serial != "emulator-5556" {
		t.Fatalf("WaitAVDReady = %q, %v", serial, err)
	}
}

func TestStopSerialUsesTheSerial(t *testing.T) {
	p := stubSDK(t, twoEmulators)
	if err := StopSerial(p, "emulator-5556"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stubLog(t), "adb -s emulator-5556 emu kill") {
		t.Errorf("stop must target the serial:\n%s", stubLog(t))
	}
}
