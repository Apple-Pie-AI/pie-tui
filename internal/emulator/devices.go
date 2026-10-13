// Seeing every device adb knows about - emulators and USB phones - by serial.
// Everything pie does to a device goes through its serial (`adb -s`): with a
// user's phone plugged in next to the emulator, a bare `adb shell` fails with
// "more than one device/emulator", and an install would land on whichever
// device adb picked.
package emulator

import (
	"os/exec"
	"strconv"
	"strings"
)

// Kind tells an emulator from a physical (USB) device.
type Kind int

const (
	KindEmulator Kind = iota
	KindUSB
)

// Device is one line of `adb devices -l`, plus what pie learned about it.
type Device struct {
	Serial string // "emulator-5554", "2A281FDH3001"
	State  string // device | offline | unauthorized | authorizing | no permissions
	Kind   Kind
	Model  string // from -l "model:Pixel_7", underscores → spaces
	AVD    string // emulators only, when known (AVDName)
}

// Ready reports whether adb can talk to the device.
func (d Device) Ready() bool { return d.State == "device" }

// ListDevices runs `adb devices -l`. It returns nil and the error when adb
// itself can't run (missing SDK) - callers show that instead of "no devices".
func ListDevices(p SDKPaths) ([]Device, error) {
	out, err := exec.Command(p.ADB, "devices", "-l").Output()
	if err != nil {
		return nil, err
	}
	return parseDevicesL(string(out)), nil
}

// parseDevicesL parses `adb devices -l`: a header, optional "* daemon …"
// notices, then "<serial> <state> [key:value ...]" per device. A state can be
// two words ("no permissions"), so it is everything before the first key:value.
func parseDevicesL(out string) []Device {
	var devs []Device
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		d := Device{Serial: f[0], Kind: KindUSB}
		if strings.HasPrefix(d.Serial, "emulator-") {
			d.Kind = KindEmulator
		}
		var state []string
		inKV := false
		for _, tok := range f[1:] {
			if k, v, ok := strings.Cut(tok, ":"); ok && k != "" {
				inKV = true
				if k == "model" {
					d.Model = strings.ReplaceAll(v, "_", " ")
				}
				continue
			}
			if !inKV {
				state = append(state, tok)
			}
		}
		d.State = strings.Join(state, " ")
		devs = append(devs, d)
	}
	return devs
}

// adbOut runs `adb -s <serial> <args>` and returns trimmed stdout ("" on error).
func adbOut(p SDKPaths, serial string, args ...string) string {
	out, err := exec.Command(p.ADB, append([]string{"-s", serial}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// AVDName is the AVD an emulator serial is running: `adb emu avd name` (whose
// output ends in a console "OK" line), else the boot property API 30+ sets.
func AVDName(p SDKPaths, serial string) string {
	if out := adbOut(p, serial, "emu", "avd", "name"); out != "" {
		if name := firstLine(out); name != "" && name != "OK" && !strings.HasPrefix(name, "KO") {
			return name
		}
	}
	return firstLine(adbOut(p, serial, "shell", "getprop", "ro.boot.qemu.avd_name"))
}

// SerialForAVD finds the running emulator serving avd. Only that serial is
// ever adopted: another running emulator belongs to the user.
func SerialForAVD(p SDKPaths, avd string) (string, bool) {
	devs, err := ListDevices(p)
	if err != nil {
		return "", false
	}
	for _, d := range devs {
		if d.Kind == KindEmulator && AVDName(p, d.Serial) == avd {
			return d.Serial, true
		}
	}
	return "", false
}

// BootCompleted reports whether the device finished booting.
func BootCompleted(p SDKPaths, serial string) bool {
	return adbOut(p, serial, "shell", "getprop", "sys.boot_completed") == "1"
}

// APILevel is ro.build.version.sdk (0 when unknown).
func APILevel(p SDKPaths, serial string) int {
	n, _ := strconv.Atoi(adbOut(p, serial, "shell", "getprop", "ro.build.version.sdk"))
	return n
}

// AndroidVersion is ro.build.version.release, e.g. "14".
func AndroidVersion(p SDKPaths, serial string) string {
	return adbOut(p, serial, "shell", "getprop", "ro.build.version.release")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
