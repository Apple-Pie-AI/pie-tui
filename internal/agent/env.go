// The agent subprocess environment. The agent inherits pie's env verbatim,
// and pie is often launched where JAVA_HOME is unset (daemon, GUI-launched
// terminal, a Mac whose only JDK lives inside Android Studio). Gradle then has
// no Java, and the agent improvises - in production it "fixed" that by writing
// gradle/gradle-daemon-jvm.properties into the repo, which git add -A then
// committed into the PR. Detecting and injecting a real JAVA_HOME removes the
// reason to improvise.
package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// javaHomeCandidates are well-known JDK locations, most specific first.
// Android Studio's embedded JBR leads: on many Android dev machines it is the
// ONLY JDK installed, and it is exactly the one Studio itself builds with.
var javaHomeCandidates = []string{
	"/Applications/Android Studio.app/Contents/jbr/Contents/Home",
}

// macJavaHome asks macOS's JDK registry. Indirected for tests (and it simply
// errors into "" on other platforms).
var macJavaHome = func() string {
	out, err := exec.Command("/usr/libexec/java_home").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// agentEnv is the environment for the claude subprocess: the parent env, plus
// a detected JAVA_HOME when the parent has none. An inherited JAVA_HOME -
// even one pointing somewhere odd - is always respected: the user's
// environment is theirs to control.
func agentEnv(logf func(string, ...interface{})) []string {
	env := os.Environ()
	if strings.TrimSpace(os.Getenv("JAVA_HOME")) != "" {
		return env
	}
	home := detectJavaHome()
	if home == "" {
		// Loud, once per run: if Gradle later fails on Java, the reason is
		// already on the record.
		logf("  (warn) no JAVA_HOME in the environment and no JDK detected - Gradle may fail to find Java")
		return env
	}
	logf("  • JAVA_HOME not set - using detected JDK: %s", home)
	return append(env, "JAVA_HOME="+home)
}

// withAndroidSDK is agentEnv's Android sibling: Android Studio users rarely
// have platform-tools on PATH (Studio and Gradle find the SDK internally), so
// a bare `adb devices` in the agent's shell exits 127 - which one verify agent
// read as "no emulator in this environment" while pie held a booted emulator's
// lease (PLEX-59644). Given the SDK root pie itself resolved, this puts its
// tool dirs on the subprocess PATH and sets ANDROID_HOME; a user's existing
// ANDROID_HOME and PATH entries are respected, and no SDK means no change.
func withAndroidSDK(env []string, sdkRoot string, logf func(string, ...interface{})) []string {
	if sdkRoot == "" {
		return env
	}
	var dirs []string
	for _, rel := range []string{"platform-tools", "emulator"} {
		if fi, err := os.Stat(filepath.Join(sdkRoot, rel)); err == nil && fi.IsDir() {
			dirs = append(dirs, filepath.Join(sdkRoot, rel))
		}
	}
	if len(dirs) == 0 {
		return env
	}

	pathIdx, hasHome := -1, false
	var path string
	for i, kv := range env {
		switch {
		case strings.HasPrefix(kv, "PATH="):
			pathIdx, path = i, strings.TrimPrefix(kv, "PATH=")
		case strings.HasPrefix(kv, "ANDROID_HOME="):
			hasHome = true
		}
	}
	elems := strings.Split(path, string(os.PathListSeparator))
	on := make(map[string]bool, len(elems))
	for _, e := range elems {
		on[e] = true
	}
	var added []string
	for _, d := range dirs {
		if !on[d] {
			elems, added = append(elems, d), append(added, d)
		}
	}
	if len(added) > 0 {
		// Rewritten in place, never appended as a second PATH= entry - which
		// of two duplicates wins differs across libcs.
		entry := "PATH=" + strings.Join(elems, string(os.PathListSeparator))
		if pathIdx >= 0 {
			env[pathIdx] = entry
		} else {
			env = append(env, entry)
		}
		logf("  • Android SDK tools added to the agent's PATH: %s", strings.Join(added, ", "))
	}
	if !hasHome {
		env = append(env, "ANDROID_HOME="+sdkRoot)
	}
	return env
}

// detectJavaHome returns the first usable JDK: a known candidate path, else
// whatever the OS registry offers. "Usable" means bin/java exists.
func detectJavaHome() string {
	usable := func(home string) bool {
		fi, err := os.Stat(filepath.Join(home, "bin", "java"))
		return err == nil && !fi.IsDir()
	}
	for _, c := range javaHomeCandidates {
		if usable(c) {
			return c
		}
	}
	if h := macJavaHome(); h != "" && usable(h) {
		return h
	}
	return ""
}
