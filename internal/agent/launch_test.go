package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A claude that dies at launch explains itself ONLY on stderr - which Run
// used to forward to pie's own stderr, invisible under the TUI and the
// daemon. PLEX-57773: exit 1, zero stream events, and the stage was reported
// as a red build. Run must keep the stderr tail and, when no session ever
// started, make it the run's error text.
func TestRunNamesStderrWhenClaudeNeverStarts(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'Error: MCP tool mcp__pie__approve (passed via --permission-prompt-tool) not found' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var logs []string
	res, err := Run(context.Background(), "p", Options{WorktreeDir: t.TempDir(),
		Logf: func(f string, a ...interface{}) { logs = append(logs, f) }})
	if err == nil {
		t.Fatal("exit 1 must surface as the process error")
	}
	if !strings.Contains(res.ErrorText, "before starting a session") {
		t.Errorf("ErrorText = %q, want it to say the CLI never started a session", res.ErrorText)
	}
	if !strings.Contains(res.ErrorText, "mcp__pie__approve") {
		t.Errorf("ErrorText = %q, want the stderr line in it", res.ErrorText)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "did not start") {
		t.Errorf("the launch failure is not in the ticket log:\n%s", joined)
	}
}

// A session that DID start and then ended in an API error keeps that error
// text; stderr must not overwrite a more specific cause.
func TestRunKeepsAPIErrorOverStderr(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho '{\"type\":\"system\",\"subtype\":\"init\",\"session_id\":\"s1\"}'\n" +
		"echo '{\"type\":\"result\",\"is_error\":true,\"result\":\"API Error: 429 budget\",\"session_id\":\"s1\"}'\n" +
		"echo 'noise' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	res, _ := Run(context.Background(), "p", Options{WorktreeDir: t.TempDir(), Logf: func(string, ...interface{}) {}})
	if res.ErrorText != "API Error: 429 budget" {
		t.Errorf("ErrorText = %q, want the API error untouched", res.ErrorText)
	}
}
