// Launching, stopping and showing the one emulator pie works with. Emulators
// are always WINDOWED - the user is in control and watches what agents do on
// the device - so there is no headless variant any more.
package emulator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// StartOpts are Android Studio's launch variants.
type StartOpts struct {
	ColdBoot bool   // -no-snapshot-load: boot fresh instead of from the quick-boot snapshot
	WipeData bool   // -wipe-data: factory reset first
	LogPath  string // where the emulator's own output goes ("" = discarded)
}

// startArgs builds the emulator argv. No -no-window / -no-audio: windowed,
// with the snapshot saved on exit so the next start is a quick boot.
func startArgs(avd string, o StartOpts) []string {
	args := []string{"-avd", avd}
	if o.ColdBoot {
		args = append(args, "-no-snapshot-load")
	}
	if o.WipeData {
		args = append(args, "-wipe-data")
	}
	return args
}

// StartWindowed launches avd in its own window. exited delivers the process's
// exit (nil or error) - an emulator that dies seconds after launch ("running
// multiple emulators with the same AVD", a missing system image) says why in
// its log, and the caller surfaces that instead of waiting out a boot.
func StartWindowed(p SDKPaths, avd string, o StartOpts) (pid int, exited <-chan error, err error) {
	cmd := exec.Command(p.Emulator, startArgs(avd, o)...)
	if o.LogPath != "" {
		if f, ferr := os.OpenFile(o.LogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644); ferr == nil {
			cmd.Stdout, cmd.Stderr = f, f
			defer f.Close() // the child keeps its own copy of the fd
		}
	}
	// Its own process group: the emulator outlives whatever launched it (a
	// ticket's run, the TUI), and a group-wide stop of that launcher must not
	// take the device down with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return 0, nil, fmt.Errorf("emulator start %q: %w", avd, err)
	}
	ch := make(chan error, 1)
	go func() { ch <- cmd.Wait() }() // reap: an unreaped zombie still answers kill(pid, 0)
	return cmd.Process.Pid, ch, nil
}

// StopSerial asks the emulator to shut down cleanly (`adb emu kill`), which
// also saves its quick-boot snapshot. USB devices are never stopped.
func StopSerial(p SDKPaths, serial string) error {
	out, err := exec.Command(p.ADB, "-s", serial, "emu", "kill").CombinedOutput()
	if err != nil {
		return fmt.Errorf("adb -s %s emu kill: %v: %s", serial, err, firstLine(string(out)))
	}
	return nil
}

// WaitAVDReady waits until the emulator serving avd is attached and fully
// booted, and returns its serial. It never looks at any other device.
func WaitAVDReady(ctx context.Context, p SDKPaths, avd string) (string, error) {
	for {
		if serial, ok := SerialForAVD(p, avd); ok && BootCompleted(p, serial) {
			return serial, nil
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("%s not ready: %w", avd, ctx.Err())
		case <-time.After(waitPoll):
		}
	}
}

// waitPoll is WaitAVDReady's poll interval; a var so tests can shorten it.
var waitPoll = 3 * time.Second

// ErrShowUnsupported is returned where pie can't raise a window.
var ErrShowUnsupported = errors.New("bringing the emulator window to the front is only supported on macOS")

// ShowWindow brings the emulator's window to the front. The window belongs to
// the qemu-system process the emulator launcher spawns, not to the pid pie
// started - and pie runs one emulator at a time, so raising that process is
// unambiguous.
func ShowWindow() error {
	if runtime.GOOS != "darwin" {
		return ErrShowUnsupported
	}
	script := `tell application "System Events" to set frontmost of (every process whose name starts with "qemu-system") to true`
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		return fmt.Errorf("could not raise the emulator window: %v: %s", err, firstLine(string(out)))
	}
	return nil
}
