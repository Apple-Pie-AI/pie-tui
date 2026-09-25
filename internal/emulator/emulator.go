// Package emulator boots, waits for and kills the Android emulator (AVD) used by
// the verify stage, and locates the SDK binaries it needs.
//
// It runs no build: the agent discovers and runs the project's own build and
// tests and self-certifies via report.json. The package was called `build` for
// exactly as long as that was untrue.
package emulator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// SDKPaths holds resolved absolute paths to Android SDK binaries.
// Use ResolvePaths to build one from the sdk root directory.
type SDKPaths struct {
	ADB      string // e.g. /Users/.../sdk/platform-tools/adb
	Emulator string // e.g. /Users/.../sdk/emulator/emulator
}

// ResolvePaths resolves adb and emulator from sdkRoot. When sdkRoot is empty
// (android_sdk_path not configured) it auto-detects the SDK, then falls back to
// PATH if the SDK-relative paths don't exist.
func ResolvePaths(sdkRoot string) SDKPaths {
	if sdkRoot == "" {
		sdkRoot = detectSDKRoot()
	}
	resolve := func(rel, name string) string {
		if sdkRoot != "" {
			p := filepath.Join(sdkRoot, rel)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
		return name // last resort: let exec fail with a clear message
	}
	return SDKPaths{
		ADB:      resolve(filepath.Join("platform-tools", "adb"), "adb"),
		Emulator: resolve(filepath.Join("emulator", "emulator"), "emulator"),
	}
}

// RootFor returns the SDK root the manager itself would use: the configured
// android_sdk_path when set, else auto-detection, else "". This is what lets
// the agent subprocess inherit the SAME SDK pie boots the emulator from -
// resolving it twice from different rules is how the verify agent once ran
// `adb devices` into exit 127 beside a booted emulator (PLEX-59644).
func RootFor(configured string) string {
	if configured != "" {
		return configured
	}
	return detectSDKRoot()
}

// detectSDKRoot finds the Android SDK when android_sdk_path isn't configured:
// $ANDROID_HOME / $ANDROID_SDK_ROOT first, then the standard per-OS location.
// Returns "" when nothing is found (callers then fall back to PATH).
func detectSDKRoot() string {
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			if _, err := os.Stat(v); err == nil {
				return v
			}
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	for _, rel := range []string{
		"Library/Android/sdk",       // macOS
		"Android/Sdk",               // Linux
		"AppData/Local/Android/Sdk", // Windows
	} {
		p := filepath.Join(home, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// ListAVDs returns the names of the installed AVDs via `emulator -list-avds`
// (nil when the emulator binary is missing or there are none).
func ListAVDs(p SDKPaths) []string {
	out, err := exec.Command(p.Emulator, "-list-avds").Output()
	if err != nil {
		return nil
	}
	return parseAVDList(string(out))
}

// parseAVDList extracts AVD names from `emulator -list-avds` output: one name
// per non-blank line.
func parseAVDList(out string) []string {
	var avds []string
	for _, line := range strings.Split(out, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			avds = append(avds, s)
		}
	}
	return avds
}

// AlreadyRunning reports whether any Android emulator is currently connected.
func AlreadyRunning(p SDKPaths) bool {
	out, err := exec.Command(p.ADB, "devices").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.HasPrefix(fields[0], "emulator-") && fields[1] == "device" {
			return true
		}
	}
	return false
}

// Start launches an AVD in the background with no window, no audio, and no
// snapshot load. Returns the OS pid of the emulator process.
func Start(p SDKPaths, avdName string) (int, error) {
	cmd := exec.Command(p.Emulator,
		"-avd", avdName,
		"-no-window",
		"-no-audio",
		"-no-snapshot-load",
	)
	cmd.Stdout = nil // suppress verbose emulator INFO logs
	cmd.Stderr = nil
	// The emulator gets its OWN process group. It is a shared resource that
	// deliberately outlives the run that booted it - other tickets queue on the
	// same AVD - so it must not inherit the booting run's group, where a later
	// "Stop & clean up" (which signals the whole group) would take it down with
	// the run and strand every ticket waiting for it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("emulator start %q: %w", avdName, err)
	}
	return cmd.Process.Pid, nil
}

// WaitReady polls `adb shell getprop sys.boot_completed` every 3 seconds until
// the emulator is fully booted or ctx is cancelled/times out.
func WaitReady(ctx context.Context, p SDKPaths) error {
	for {
		out, err := exec.CommandContext(ctx, p.ADB, "shell", "getprop", "sys.boot_completed").Output()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("emulator not ready: %w", ctx.Err())
		case <-time.After(3 * time.Second):
		}
	}
}

// Kill sends SIGTERM then SIGKILL to the emulator process. Used on daemon
// shutdown and idle-timeout - NOT after individual test runs.
func Kill(pid int) {
	// pid 0 means "no emulator process recorded" - which the daemon's idle
	// reaper can read straight out of the store. Signalling it would not be a
	// no-op: syscall.Kill(0, …) targets the caller's ENTIRE process group, so
	// this would SIGTERM the daemon itself instead of an emulator.
	if pid <= 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(syscall.SIGTERM)
	time.Sleep(2 * time.Second)
	_ = proc.Signal(syscall.SIGKILL)
}
