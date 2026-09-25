package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// deadClaude puts a claude on PATH that dies at launch the way the field CLI
// did on PLEX-57773: a line on stderr, exit 1, and not one stream event.
func deadClaude(t *testing.T, stderr string) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho '" + stderr + "' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

const fieldStderr = "Error: MCP tool mcp__pie__approve (passed via --permission-prompt-tool) not found"

// PLEX-57773 (twice, two tickets): the ship-comments verify's claude exited 1
// before its init event. Pie read "no fresh report.json" as "the build or
// tests failed", told the human their fixes were red, and never pushed or
// replied. Nothing was red - nothing ran. A verify that never started must
// say so, carry the CLI's own stderr, and park the flow so a retry re-enters
// ship-comments.
func TestShipCommentsDeadVerifyIsNotReportedAsRed(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	fakeGH(t)
	deadClaude(t, fieldStderr)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("K-7", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("K-7", "b", "", "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("K-7", "https://github.com/o/r/pull/9", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "alice", Path: "a.kt", Line: 3, Body: "fix",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-7", []string{"T1"}); err != nil {
		t.Fatal(err)
	}
	readyWorktree(t, "/repo", "K-7")
	wt := filepath.Join(os.Getenv("PIE_HOME"), "worktrees", "K-7")

	var comment string
	var logs []string
	task := Task{Ticket: "K-7", Repo: &config.Repo{Path: wt}, Cfg: &config.Config{},
		Store: st, ShipComments: true, PriorState: store.StateFixReview}
	out := shipComments(t.Context(), task, Hooks{
		Comment: func(s string) { comment = s },
		Logf:    func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")

	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you:\n%s", out.State, joined)
	}
	for _, lie := range []string{"review fixes are red", "build or tests failed", "build or tests are red"} {
		if strings.Contains(comment, lie) {
			t.Errorf("the comment claims %q for a verify that never ran:\n%s", lie, comment)
		}
	}
	if !strings.Contains(comment, "mcp__pie__approve") {
		t.Errorf("the CLI's own stderr is the only explanation and it is missing from the comment:\n%s", comment)
	}
	if !strings.Contains(joined, "mcp__pie__approve") {
		t.Errorf("the CLI's stderr never reached the ticket log:\n%s", joined)
	}
	sess, err := st.Get("K-7")
	if err != nil {
		t.Fatal(err)
	}
	if sess.ParkedFlow != "ship-comments" {
		t.Errorf("parked_flow = %q, want ship-comments so a retry re-enters this flow", sess.ParkedFlow)
	}
}

// verifyWithAgent's error text is what every gate prints. When claude never
// started a session, that text must be the launch failure with the stderr
// tail - not empty, which every gate reads as "the build is red".
func TestVerifyLaunchFailureNamesStderr(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	deadClaude(t, fieldStderr)

	task := Task{Ticket: "K-8", Repo: &config.Repo{Path: wt}, Cfg: &config.Config{}}
	rep, verified, _, denials, errText := verifyWithAgent(t.Context(), task, wt, "", false, true, "",
		func(string, ...interface{}) {}, func(string, int) {})
	if rep != nil || verified || len(denials) != 0 {
		t.Fatalf("rep=%v verified=%v denials=%v, want nothing certified", rep, verified, denials)
	}
	if !strings.Contains(errText, "before starting a session") || !strings.Contains(errText, "mcp__pie__approve") {
		t.Errorf("errText = %q, want a launch failure naming the CLI's stderr", errText)
	}
}
