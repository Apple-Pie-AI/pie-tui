package emulator

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAVD(t *testing.T, home, name, top, cfg string) {
	t.Helper()
	dir := filepath.Join(home, name+".avd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if top != "" {
		if err := os.WriteFile(filepath.Join(home, name+".ini"), []byte(top), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadAVDInfo(t *testing.T) {
	home := t.TempDir()
	writeAVD(t, home, "Pixel_7_API_34",
		"avd.ini.encoding=UTF-8\npath="+filepath.Join(home, "Pixel_7_API_34.avd")+"\ntarget=android-34\n",
		"# comment\navd.ini.displayname=Pixel 7 API 34\nhw.device.name=pixel_7\nabi.type=arm64-v8a\n"+
			"PlayStore.enabled=true\nimage.sysdir.1=system-images/android-34/google_apis_playstore/arm64-v8a/\n")
	got := ReadAVDInfo(home, "Pixel_7_API_34")
	want := AVDInfo{Name: "Pixel_7_API_34", DisplayName: "Pixel 7 API 34", Device: "Pixel 7",
		API: 34, ABI: "arm64-v8a", PlayStore: true}
	if got != want {
		t.Errorf("ReadAVDInfo = %+v, want %+v", got, want)
	}

	// No files at all: the name stands in for everything.
	if got := ReadAVDInfo(home, "Ghost"); got != (AVDInfo{Name: "Ghost", DisplayName: "Ghost"}) {
		t.Errorf("missing AVD = %+v", got)
	}

	// API from the image dir when target is a codename.
	writeAVD(t, home, "Preview", "target=android-VanillaIceCream\n",
		"image.sysdir.1=system-images/android-35/google_apis/x86_64/\n")
	if got := ReadAVDInfo(home, "Preview"); got.API != 35 || got.PlayStore {
		t.Errorf("preview AVD = %+v", got)
	}
}

func TestAVDHomeOverride(t *testing.T) {
	t.Setenv("ANDROID_AVD_HOME", "/custom/avd")
	if got := AVDHome(); got != "/custom/avd" {
		t.Errorf("AVDHome = %q", got)
	}
}
