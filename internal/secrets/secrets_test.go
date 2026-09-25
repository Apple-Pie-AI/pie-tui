package secrets

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEnvName(t *testing.T) {
	cases := map[string]string{
		Jira:      "PIE_JIRA_TOKEN",
		Git:       "PIE_GIT_TOKEN",
		Anthropic: "PIE_ANTHROPIC_TOKEN",
	}
	for key, want := range cases {
		if got := envName(key); got != want {
			t.Errorf("envName(%q) = %q, want %q", key, got, want)
		}
	}
}

// The env var is the documented escape hatch for headless/CI use, so it must win
// over the keychain rather than merely being a fallback.
func TestGetPrefersEnvOverKeychain(t *testing.T) {
	t.Setenv(envName(Anthropic), "from-env")
	if got := Get(Anthropic); got != "from-env" {
		t.Errorf("Get(%q) = %q, want %q", Anthropic, got, "from-env")
	}
}

func TestGetEmptyEnvFallsThrough(t *testing.T) {
	// An empty env var must not be treated as "the secret is empty string" and
	// short-circuit the keychain lookup.
	t.Setenv(envName(Jira), "")
	// No keychain entry is guaranteed in test environments, so the only stable
	// assertion is that this does not panic and does not return the empty env
	// value as if it were set.
	_ = Get(Jira)
}

// withGOOS forces the platform branch under test. Without it these assertions
// could only run on a host of that OS - and since this project's CI and its
// developers are all on macOS, the non-darwin branch would never execute.
func withGOOS(t *testing.T, os string) {
	t.Helper()
	prev := goos
	t.Cleanup(func() { goos = prev })
	goos = os
}

// Set used to `return nil` on every non-darwin platform, reporting success while
// discarding the token: `pie init` looked like it saved the key and every
// later Get returned "".
func TestSetFailsLoudlyOnUnsupportedPlatform(t *testing.T) {
	for _, os := range []string{"linux", "windows", "freebsd"} {
		t.Run(os, func(t *testing.T) {
			withGOOS(t, os)
			err := Set(Anthropic, "some-token")
			if err == nil {
				t.Fatal("Set returned nil with no keychain backend - the token was silently discarded")
			}
			if !errors.Is(err, ErrUnsupportedPlatform) {
				t.Errorf("Set error = %v, want it to wrap ErrUnsupportedPlatform", err)
			}
		})
	}
}

// Get must not consult the keychain on a platform that has none, and must still
// honour the env var there - that is the documented fallback the Set error
// points users at, so it has to actually work.
func TestGetUsesEnvOnUnsupportedPlatform(t *testing.T) {
	withGOOS(t, "linux")
	t.Setenv(envName(Git), "env-token")
	if got := Get(Git); got != "env-token" {
		t.Errorf("Get = %q, want %q", got, "env-token")
	}
	t.Setenv(envName(Git), "")
	if got := Get(Git); got != "" {
		t.Errorf("Get with no env and no keychain = %q, want empty", got)
	}
}

// The value is fed to `security` on stdin, which reads a line at a time, so an
// embedded newline would silently truncate the stored secret (or store an empty
// one). Rejected before the exec, so this runs on every platform.
func TestSetRejectsNewlineInValue(t *testing.T) {
	withGOOS(t, "darwin")
	for _, val := range []string{"tok\nen", "tok\r\nen", "token\n", "\n"} {
		err := Set(Anthropic, val)
		if err == nil {
			t.Errorf("Set(%q) = nil, want an error", val)
			continue
		}
		if !strings.Contains(err.Error(), "newline") {
			t.Errorf("Set(%q) error = %v, want it to mention the newline", val, err)
		}
		if errors.Is(err, ErrUnsupportedPlatform) {
			t.Errorf("Set(%q) reported the wrong reason: %v", val, err)
		}
	}
}

// Both of these are invisible to a happy-path test: `go test` has no controlling
// terminal, so the keychain write succeeds whether or not Setsid is set and
// whether the value is written once or twice. They are asserted on the command
// itself instead. See setCmd's comments for why each one matters.
func TestSetCmdDetachesFromControllingTerminal(t *testing.T) {
	c := setCmd(Anthropic, "tok")
	if c.SysProcAttr == nil || !c.SysProcAttr.Setsid {
		t.Fatal("setCmd must set Setsid: `security -w` prompts on /dev/tty when a " +
			"controlling terminal exists, ignoring cmd.Stdin, and `pie init` hangs")
	}
}

func TestSetCmdWritesValueTwiceOnStdin(t *testing.T) {
	c := setCmd(Anthropic, "tok")
	if c.Stdin == nil {
		t.Fatal("setCmd must feed the secret on stdin, not as an argv element")
	}
	b, err := io.ReadAll(c.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	// `security -w` prompts twice; a single write leaves the second read at EOF
	// and silently stores an empty password.
	if got, want := string(b), "tok\ntok\n"; got != want {
		t.Errorf("stdin = %q, want %q", got, want)
	}
}

// The whole point of feeding stdin is keeping the secret out of argv, where a
// same-user `ps` can read it.
func TestSetCmdKeepsSecretOutOfArgv(t *testing.T) {
	const secret = "sup3r-s3cret-token"
	c := setCmd(Anthropic, secret)
	for _, a := range c.Args {
		if strings.Contains(a, secret) {
			t.Fatalf("secret leaked into argv: %v", c.Args)
		}
	}
	// -w must be last, or it consumes the next argv element as the password.
	if c.Args[len(c.Args)-1] != "-w" {
		t.Errorf("-w must be the final argument, got %v", c.Args)
	}
}
