package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T8.1 - CLIVersion reads the real binary's answer (via a PATH shim) and is
// cached; a missing binary degrades to a labelled unknown, never an error.
// Both paths share the sync.Once, so this test covers whichever branch runs
// first and asserts only shape, not freshness.
func TestCLIVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"),
		[]byte("#!/bin/sh\necho '9.9.9 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	got := CLIVersion()
	if got == "" {
		t.Fatal("empty version")
	}
	if !strings.Contains(got, "9.9.9") && !strings.Contains(got, "unknown") &&
		!strings.Contains(got, "Claude Code") {
		t.Errorf("version = %q, want the shim's answer, a real answer, or a labelled unknown", got)
	}
}

func TestCountRules(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"Edit Write Bash(./gradlew:*)", 3},
		{"Bash(git diff:*) Bash(cd Compass && ./gradlew test)", 2}, // spaces inside rules
	}
	for _, c := range cases {
		if got := CountRules(c.in); got != c.want {
			t.Errorf("CountRules(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
