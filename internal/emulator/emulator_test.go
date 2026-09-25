package emulator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSDKRoot(t *testing.T) {
	// $ANDROID_HOME wins when it points at an existing dir.
	dir := t.TempDir()
	t.Setenv("ANDROID_HOME", dir)
	t.Setenv("ANDROID_SDK_ROOT", "")
	if got := detectSDKRoot(); got != dir {
		t.Errorf("ANDROID_HOME: got %q, want %q", got, dir)
	}

	// $ANDROID_SDK_ROOT is the documented second choice.
	other := t.TempDir()
	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", other)
	if got := detectSDKRoot(); got != other {
		t.Errorf("ANDROID_SDK_ROOT: got %q, want %q", got, other)
	}

	// ANDROID_HOME wins over ANDROID_SDK_ROOT when both are valid.
	t.Setenv("ANDROID_HOME", dir)
	if got := detectSDKRoot(); got != dir {
		t.Errorf("precedence: got %q, want ANDROID_HOME %q", got, dir)
	}

	// A non-existent env value is ignored (falls through to the standard paths).
	bogus := filepath.Join(dir, "does-not-exist")
	t.Setenv("ANDROID_HOME", bogus)
	t.Setenv("ANDROID_SDK_ROOT", "")
	got := detectSDKRoot()
	if got == bogus {
		t.Errorf("detectSDKRoot returned a non-existent env path: %q", got)
	}
	// Asserting only "not the bogus path" would also pass if detectSDKRoot were
	// gutted to return "". Whatever it returns must be "" or a real directory.
	if got != "" {
		if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
			t.Errorf("detectSDKRoot() = %q, which is not an existing directory", got)
		}
	}
}

func TestResolvePathsConfiguredRoot(t *testing.T) {
	// A configured root with the expected layout resolves to the SDK binaries.
	root := t.TempDir()
	for _, rel := range []string{"platform-tools/adb", "emulator/emulator"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := ResolvePaths(root)
	if p.ADB != filepath.Join(root, "platform-tools/adb") {
		t.Errorf("ADB = %q", p.ADB)
	}
	if p.Emulator != filepath.Join(root, "emulator/emulator") {
		t.Errorf("Emulator = %q", p.Emulator)
	}
}

func TestParseAVDList(t *testing.T) {
	cases := map[string][]string{
		"Pixel_6_API_34\nPixel_Tablet_API_34\n": {"Pixel_6_API_34", "Pixel_Tablet_API_34"},
		"  Pixel_6  \n\n":                       {"Pixel_6"}, // trims + skips blanks
		"":                                      nil,
		"\n\n":                                  nil,
		"OnlyOne":                               {"OnlyOne"}, // no trailing newline
	}
	for in, want := range cases {
		got := parseAVDList(in)
		if len(got) != len(want) {
			t.Errorf("parseAVDList(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("parseAVDList(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

// RootFor hands the agent the same SDK the emulator manager resolved: the
// configured android_sdk_path wins, else auto-detection, else "".
func TestRootFor(t *testing.T) {
	sdk := t.TempDir()

	if got := RootFor(sdk); got != sdk {
		t.Errorf("configured root ignored: %q", got)
	}

	t.Setenv("ANDROID_HOME", sdk)
	if got := RootFor(""); got != sdk {
		t.Errorf("auto-detect via ANDROID_HOME = %q, want %q", got, sdk)
	}

	t.Setenv("ANDROID_HOME", "")
	t.Setenv("ANDROID_SDK_ROOT", "")
	t.Setenv("HOME", t.TempDir()) // no ~/Library/Android/sdk either
	if got := RootFor(""); got != "" {
		t.Errorf("no SDK anywhere should yield \"\", got %q", got)
	}
}
