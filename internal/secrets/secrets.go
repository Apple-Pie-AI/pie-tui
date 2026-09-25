// Package secrets reads long-lived tokens (Decision 14: OS keychain, env-var fallback).
//
// On macOS it shells out to the `security` CLI so we don't pull in a cgo/keychain
// dependency. Env vars ($PIE_<KEY>_TOKEN) always win, for headless/CI use.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

const service = "pie"

// goos is runtime.GOOS, indirected so the platform branches in Get/Set are
// reachable from tests on any host. Without this, the non-darwin path could only
// ever be exercised by running the suite on Linux - and both this project's CI
// and its developers are on macOS, so the branch would never execute anywhere.
var goos = runtime.GOOS

// Logical secret keys.
const (
	Jira      = "jira"
	Git       = "git"
	Anthropic = "anthropic"
)

func envName(key string) string {
	return "PIE_" + strings.ToUpper(key) + "_TOKEN"
}

// Get returns the secret for key: env var first, then the OS keychain.
func Get(key string) string {
	if v := os.Getenv(envName(key)); v != "" {
		return v
	}
	if goos == "darwin" {
		out, err := exec.Command("security", "find-generic-password", "-s", service, "-a", key, "-w").Output()
		if err == nil {
			return strings.TrimRight(string(out), "\n")
		}
	}
	return ""
}

// ErrUnsupportedPlatform is returned by Set on platforms with no keychain
// backend. It is a real error rather than a silent no-op: reporting success
// while discarding the token makes `pie init` and the hub's API-key prompt
// look like they saved something, and the failure then surfaces much later as an
// unauthenticated Claude/Jira call.
var ErrUnsupportedPlatform = errors.New(
	"no keychain backend on this platform - set the token via the " +
		"PIE_<KEY>_TOKEN environment variable instead")

// setCmd builds the keychain-write command. Split out from Set so the two
// non-obvious details below are assertable in a unit test - neither can be
// caught by a happy-path test, because `go test` runs without a controlling
// terminal and therefore takes the working path either way.
func setCmd(key, val string) *exec.Cmd {
	// A trailing -w with no value makes `security` read the secret from stdin
	// instead of taking it as an argv element, where any user running `ps` could
	// read it. It prompts twice ("password data" / "retype password"), so the
	// value has to be written twice - feeding it once leaves the second read at
	// EOF and silently stores an EMPTY password.
	cmd := exec.Command("security", "add-generic-password", "-s", service, "-a", key, "-U", "-w")
	cmd.Stdin = strings.NewReader(val + "\n" + val + "\n")
	// Setsid is load-bearing, not hygiene. `security` prompts via
	// readpassphrase(3), which opens /dev/tty whenever a controlling terminal
	// exists and only falls back to stdin when it cannot. Both callers (`pie init`
	// and the hub setup form) run on a terminal, so without a new session
	// the prompt lands on the user's tty, cmd.Stdin is never read, and the write
	// blocks forever - or stores whatever the user types at the mystery prompt.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}

// Set stores a secret in the OS keychain.
func Set(key, val string) error {
	if goos != "darwin" {
		return fmt.Errorf("store %s secret: %w", key, ErrUnsupportedPlatform)
	}
	if strings.ContainsAny(val, "\r\n") {
		return fmt.Errorf("store %s secret: value must not contain a newline", key)
	}
	if out, err := setCmd(key, val).CombinedOutput(); err != nil {
		return fmt.Errorf("store %s secret in keychain: %w\n%s", key, err, out)
	}
	return nil
}
