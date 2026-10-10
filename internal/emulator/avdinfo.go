// What an AVD is, read from the files the AVD Manager writes - so the device
// row can say "Pixel 7 · API 34 · Google Play" the way Android Studio does,
// without booting anything.
package emulator

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// AVDInfo describes one installed AVD. Missing files degrade to the name.
type AVDInfo struct {
	Name        string // the AVD id, as `emulator -list-avds` prints it
	DisplayName string // avd.ini.displayname, else Name
	Device      string // hw.device.name, e.g. "pixel_7" → "Pixel 7"
	API         int    // 0 when unknown
	ABI         string // abi.type, e.g. "arm64-v8a"
	PlayStore   bool   // PlayStore.enabled / google_apis_playstore image
}

// AVDHome is where the AVD Manager keeps AVDs, honoring the same overrides the
// emulator does.
func AVDHome() string {
	if v := os.Getenv("ANDROID_AVD_HOME"); v != "" {
		return v
	}
	for _, env := range []string{"ANDROID_USER_HOME", "ANDROID_EMULATOR_HOME"} {
		if v := os.Getenv(env); v != "" {
			return filepath.Join(v, "avd")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".android", "avd")
}

// ReadAVDInfo reads <home>/<name>.ini (path=, target=) and that path's
// config.ini.
func ReadAVDInfo(home, name string) AVDInfo {
	info := AVDInfo{Name: name, DisplayName: name}
	top := readINI(filepath.Join(home, name+".ini"))
	dir := top["path"]
	if dir == "" {
		dir = filepath.Join(home, name+".avd")
	}
	cfg := readINI(filepath.Join(dir, "config.ini"))
	if v := cfg["avd.ini.displayname"]; v != "" {
		info.DisplayName = v
	}
	info.Device = prettyDevice(cfg["hw.device.name"])
	info.ABI = cfg["abi.type"]
	info.API = apiFrom(top["target"], cfg["image.sysdir.1"])
	info.PlayStore = cfg["PlayStore.enabled"] == "true" ||
		strings.Contains(cfg["tag.id"], "playstore") ||
		strings.Contains(cfg["image.sysdir.1"], "playstore")
	return info
}

func readINI(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer f.Close()
	return parseINI(f)
}

// parseINI reads key=value lines; comments and blanks are skipped.
func parseINI(r io.Reader) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

var apiRe = regexp.MustCompile(`android-(\d+)`)

// apiFrom reads the API level from target ("android-34") or the system image
// dir ("system-images/android-34/google_apis/arm64-v8a/"). Codename targets
// ("android-VanillaIceCream") give 0.
func apiFrom(target, sysdir string) int {
	for _, s := range []string{target, sysdir} {
		if m := apiRe.FindStringSubmatch(s); m != nil {
			n, _ := strconv.Atoi(m[1])
			return n
		}
	}
	return 0
}

// prettyDevice turns a hw.device.name id into a label: "pixel_7" → "Pixel 7".
func prettyDevice(id string) string {
	if id == "" {
		return ""
	}
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '_' || r == ' ' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}
