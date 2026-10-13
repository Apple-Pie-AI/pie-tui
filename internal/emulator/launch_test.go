package emulator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Emulators are always windowed now: no -no-window / -no-audio anywhere, for
// agents or the user. Cold boot and wipe map to Studio's launch options.
func TestStartArgsAreWindowed(t *testing.T) {
	for _, tc := range []struct {
		o    StartOpts
		want string
	}{
		{StartOpts{}, "-avd Pixel_7"},
		{StartOpts{ColdBoot: true}, "-avd Pixel_7 -no-snapshot-load"},
		{StartOpts{WipeData: true}, "-avd Pixel_7 -wipe-data"},
	} {
		got := strings.Join(startArgs("Pixel_7", tc.o), " ")
		if got != tc.want {
			t.Errorf("startArgs(%+v) = %q, want %q", tc.o, got, tc.want)
		}
		if strings.Contains(got, "-no-window") || strings.Contains(got, "-no-audio") {
			t.Errorf("headless flag in %q", got)
		}
	}
}

func TestStartWindowedLaunchesAndReportsExit(t *testing.T) {
	p := stubSDK(t, "")
	logPath := filepath.Join(t.TempDir(), "emu.log")
	_, exited, err := StartWindowed(p, "Pixel_7", StartOpts{ColdBoot: true, LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("exited never delivered")
	}
	if !strings.Contains(stubLog(t), "emulator -avd Pixel_7 -no-snapshot-load") {
		t.Errorf("argv:\n%s", stubLog(t))
	}

	// An emulator that dies at launch says why - in its log, surfaced by exited.
	t.Setenv("STUB_EMU_EXIT", "Running multiple emulators with the same AVD")
	_, exited, err = StartWindowed(p, "Pixel_7", StartOpts{LogPath: logPath})
	if err != nil {
		t.Fatal(err)
	}
	if werr := <-exited; werr == nil {
		t.Error("a failed launch must deliver its exit error")
	}
	b, _ := os.ReadFile(logPath)
	if !strings.Contains(string(b), "same AVD") {
		t.Errorf("emulator output should land in the log: %q", b)
	}
}

// Start (the agents' entry point) is the windowed launch too.
func TestStartIsWindowed(t *testing.T) {
	p := stubSDK(t, "")
	if _, err := Start(p, "Pixel_7"); err != nil {
		t.Fatal(err)
	}
	// Start doesn't wait for the process; give the stub time to record argv.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stubLog(t), "emulator -avd") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if log := stubLog(t); !strings.Contains(log, "emulator -avd Pixel_7") || strings.Contains(log, "-no-window") {
		t.Errorf("Start must launch windowed:\n%s", log)
	}
}
