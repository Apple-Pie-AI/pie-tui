package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeJDK creates a dir that passes the bin/java usability check.
func fakeJDK(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bin", "java"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestAgentEnvInjectsDetectedJavaHome(t *testing.T) {
	jdk := fakeJDK(t)
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = []string{jdk}
	t.Setenv("JAVA_HOME", "")

	var lines []string
	env := agentEnv(func(f string, a ...interface{}) { lines = append(lines, fmt.Sprintf(f, a...)) })
	if !contains(env, "JAVA_HOME="+jdk) {
		t.Errorf("JAVA_HOME not injected; env tail: %v", env[len(env)-3:])
	}
	if all := strings.Join(lines, "\n"); !strings.Contains(all, "using detected JDK") {
		t.Errorf("injection not logged:\n%s", all)
	}
}

func TestAgentEnvRespectsExistingJavaHome(t *testing.T) {
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = []string{fakeJDK(t)} // a valid candidate exists...
	t.Setenv("JAVA_HOME", "/user/chose/this")

	env := agentEnv(func(string, ...interface{}) {})
	// ...but the user's own value wins, and nothing extra is appended.
	if contains(env, "JAVA_HOME="+javaHomeCandidates[0]) {
		t.Error("detected JDK must not override an explicit JAVA_HOME")
	}
}

func TestAgentEnvFallsBackToMacRegistry(t *testing.T) {
	jdk := fakeJDK(t)
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = nil // no well-known install
	macJavaHome = func() string { return jdk }
	t.Setenv("JAVA_HOME", "")

	if env := agentEnv(func(string, ...interface{}) {}); !contains(env, "JAVA_HOME="+jdk) {
		t.Error("registry-provided JDK not injected")
	}
}

func TestAgentEnvWarnsWhenNothingFound(t *testing.T) {
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = nil
	macJavaHome = func() string { return "" }
	t.Setenv("JAVA_HOME", "")

	var lines []string
	agentEnv(func(f string, a ...interface{}) { lines = append(lines, fmt.Sprintf(f, a...)) })
	if all := strings.Join(lines, "\n"); !strings.Contains(all, "no JAVA_HOME") {
		t.Errorf("missing-JDK warn absent:\n%s", all)
	}
}

func contains(env []string, kv string) bool {
	for _, e := range env {
		if e == kv {
			return true
		}
	}
	return false
}

// A candidate dir without bin/java is not a JDK - detection must fall through
// to the OS registry rather than injecting a broken path.
func TestAgentEnvSkipsUnusableCandidate(t *testing.T) {
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = []string{t.TempDir()} // exists, but no bin/java
	real := fakeJDK(t)
	macJavaHome = func() string { return real }
	t.Setenv("JAVA_HOME", "")

	if env := agentEnv(func(string, ...interface{}) {}); !contains(env, "JAVA_HOME="+real) {
		t.Error("unusable candidate should fall through to the registry JDK")
	}
}

// Whitespace-only JAVA_HOME means unset, not "respect this".
func TestAgentEnvWhitespaceJavaHomeTreatedAsUnset(t *testing.T) {
	jdk := fakeJDK(t)
	origCandidates, origMac := javaHomeCandidates, macJavaHome
	t.Cleanup(func() { javaHomeCandidates, macJavaHome = origCandidates, origMac })
	javaHomeCandidates = []string{jdk}
	macJavaHome = func() string { return "" }
	t.Setenv("JAVA_HOME", "   ")

	if env := agentEnv(func(string, ...interface{}) {}); !contains(env, "JAVA_HOME="+jdk) {
		t.Error("whitespace JAVA_HOME should be treated as unset and detection should inject")
	}
}

// fakeSDK creates a dir shaped like an Android SDK: platform-tools/adb and
// emulator/emulator both present and executable.
func fakeSDK(t *testing.T) string {
	t.Helper()
	sdk := t.TempDir()
	for _, rel := range []string{"platform-tools/adb", "emulator/emulator"} {
		p := filepath.Join(sdk, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return sdk
}

// pathOf returns the PATH value from an env slice ("" if absent).
func pathOf(env []string) string {
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			return strings.TrimPrefix(kv, "PATH=")
		}
	}
	return ""
}

// The verify agent could not run `adb devices` (exit 127) while pie itself
// held a booted emulator's lease - the SDK's tool dirs were never put on the
// subprocess PATH (PLEX-59644). withAndroidSDK is the JAVA_HOME-style healing
// for that: PATH gains platform-tools and emulator, ANDROID_HOME is set.
func TestWithAndroidSDKAddsToolDirsToPath(t *testing.T) {
	sdk := fakeSDK(t)
	env := withAndroidSDK([]string{"PATH=/usr/bin", "HOME=/h"}, sdk, func(string, ...interface{}) {})

	path := pathOf(env)
	if !strings.Contains(path, filepath.Join(sdk, "platform-tools")) {
		t.Errorf("PATH missing platform-tools: %q", path)
	}
	if !strings.Contains(path, filepath.Join(sdk, "emulator")) {
		t.Errorf("PATH missing emulator dir: %q", path)
	}
	if !strings.Contains(path, "/usr/bin") {
		t.Errorf("original PATH entries lost: %q", path)
	}
	if !contains(env, "ANDROID_HOME="+sdk) {
		t.Error("ANDROID_HOME not set from the resolved SDK root")
	}
	// The PATH entry is rewritten in place - a second PATH= var is undefined
	// behavior territory across libcs.
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d PATH entries, want exactly 1", n)
	}
}

func TestWithAndroidSDKRespectsExisting(t *testing.T) {
	sdk := fakeSDK(t)
	pt := filepath.Join(sdk, "platform-tools")

	// Already on PATH -> not appended twice.
	env := withAndroidSDK([]string{"PATH=" + pt + ":/usr/bin"}, sdk, func(string, ...interface{}) {})
	if path := pathOf(env); strings.Count(path, pt) != 1 {
		t.Errorf("platform-tools duplicated on PATH: %q", path)
	}

	// An existing ANDROID_HOME is the user's choice - never overwritten.
	env = withAndroidSDK([]string{"PATH=/usr/bin", "ANDROID_HOME=/custom"}, sdk, func(string, ...interface{}) {})
	if !contains(env, "ANDROID_HOME=/custom") {
		t.Error("existing ANDROID_HOME overwritten")
	}
	for _, kv := range env {
		if kv == "ANDROID_HOME="+sdk {
			t.Error("second ANDROID_HOME appended alongside the user's")
		}
	}
}

func TestWithAndroidSDKNoSDKNoChange(t *testing.T) {
	in := []string{"PATH=/usr/bin", "HOME=/h"}
	for _, root := range []string{"", filepath.Join(t.TempDir(), "missing")} {
		env := withAndroidSDK(append([]string(nil), in...), root, func(string, ...interface{}) {})
		if len(env) != len(in) || pathOf(env) != "/usr/bin" {
			t.Errorf("root %q: env changed with no usable SDK: %v", root, env)
		}
	}
}
