// Tests for the denial-park plumbing: where a blocked comment-fix lands, how
// a parked ship-comments retry passes the gate, and what the flavor-diagnosed
// park messages say.
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// T5.2 - the comment-fix disposition truth table. Pure, so no worktree needed.
func TestCommentFixDisposition(t *testing.T) {
	err := fmt.Errorf("exit status 1")
	cases := []struct {
		name     string
		denials  int
		evidence bool
		agentErr error
		want     string
	}{
		{"blocked with nothing to show parks as a denial even on clean exit", 2, false, nil, fixParkDenied},
		{"blocked and errored with nothing to show still parks as a denial", 2, false, err, fixParkDenied},
		{"blocked but produced evidence parks reviewable (annotated)", 2, true, nil, fixReady},
		{"died with nothing to show and no denials parks as a dead agent", 0, false, err, fixParkDead},
		{"clean run with evidence is reviewable", 0, true, nil, fixReady},
		{"errored but produced evidence is reviewable (caller logged the exit)", 0, true, err, fixReady},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commentFixDisposition(c.denials, c.evidence, c.agentErr); got != c.want {
				t.Errorf("disposition(%d, %v, %v) = %q, want %q", c.denials, c.evidence, c.agentErr, got, c.want)
			}
		})
	}
}

// T5.5 - the ship-comments gate accepts a parked retry of its own flow, and
// nothing else that isn't a human approval.
func TestShipGate(t *testing.T) {
	cases := []struct {
		prior, flow string
		want        bool
	}{
		{store.StateFixReview, "", true},
		{store.StateFixReview, "ship-comments", true},
		{store.StateNeedsYou, "ship-comments", true}, // parked ship retry: already approved
		{store.StateNeedsYou, "", false},             // ordinary needs-you: never approved
		{store.StateNeedsYou, "address-comments", false},
		{store.StateReview, "ship-comments", false},
		{"queued", "", false},
	}
	for _, c := range cases {
		if got := shipGateOK(c.prior, c.flow); got != c.want {
			t.Errorf("shipGateOK(%q, %q) = %v, want %v", c.prior, c.flow, got, c.want)
		}
	}
}

// flowFor names the flow the park records for the retry.
func TestFlowFor(t *testing.T) {
	if got := flowFor(Task{ShipComments: true}); got != "ship-comments" {
		t.Errorf("ship: %q", got)
	}
	if got := flowFor(Task{AddressComments: true}); got != "address-comments" {
		t.Errorf("address: %q", got)
	}
	if got := flowFor(Task{Resume: true}); got != "" {
		t.Errorf("main pipeline: %q", got)
	}
}

// T5.1 - the flavor-diagnosed park messages: an ask rule names the rule and
// its file and points at the in-dashboard approval; a deny rule refuses to
// work around itself; an unknown cause quotes the CLI's own words.
func TestPermissionExplanationFlavors(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(wt, ".claude", "settings.json")
	if err := os.WriteFile(settings,
		[]byte(`{"permissions":{"ask":["Bash(./gradlew:*)"],"deny":["Bash(adb:*)"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	ask := []agent.Denial{{Tool: "Bash", Command: "cd Compass && ./gradlew test",
		ErrorText: "Claude requested permissions to use Bash, but you haven't granted it yet."}}
	msg := permissionExplanation(ask, nil, "T-1", wt)
	for _, want := range []string{
		"require interactive approval",
		"Bash(./gradlew:*)", // the rule, named
		settings,            // the file, named
		"approval prompt in the pie dashboard",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("ask-rule message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "would not help") {
		t.Errorf("ask-rule message fell back to the shrug:\n%s", msg)
	}

	deny := []agent.Denial{{Tool: "Bash", Command: "adb devices",
		ErrorText: "Permission to use Bash with command adb devices has been denied."}}
	dmsg := permissionExplanation(deny, nil, "T-1", wt)
	for _, want := range []string{"explicit deny rule", "Bash(adb:*)", "will not work around"} {
		if !strings.Contains(dmsg, want) {
			t.Errorf("deny-rule message missing %q:\n%s", want, dmsg)
		}
	}

	unknown := []agent.Denial{{Tool: "Bash", Command: "gradle help",
		ErrorText: "some never-seen refusal wording"}}
	umsg := permissionExplanation(unknown, nil, "T-1", wt)
	if !strings.Contains(umsg, "some never-seen refusal wording") {
		t.Errorf("unknown-cause message must quote the CLI verbatim:\n%s", umsg)
	}
	if !strings.Contains(umsg, "would not help") {
		t.Errorf("unknown-cause message lost its honest fallback:\n%s", umsg)
	}
}

// T5.3 - a comment-fix blocked before producing anything parks as a denial
// (not fix-review), stores the denials for the dashboard, and records the
// address-comments flow for the retry. Mirrors the scaffolding of
// TestDeadFixAgentDoesNotParkAVictoryState with a stub claude that is REFUSED
// rather than dead.
func TestBlockedFixAgentParksAsDenial(t *testing.T) {
	fakeGH(t)
	binDir := t.TempDir()
	stub := `#!/bin/sh
echo '{"type":"system","subtype":"init","session_id":"blockfix","model":"stub"}'
echo '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Claude requested permissions to use Bash, but you haven'"'"'t granted it yet."}]}}'
echo '{"type":"result","subtype":"success","result":"blocked","session_id":"blockfix","permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"cd Compass && ./gradlew test"}}]}'
`
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PIE_HOME", t.TempDir())

	wt := paths.WorktreeFor("", "K-2")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "base")

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("K-2", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("K-2", "b", wt, "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("K-2", "https://github.com/o/r/pull/9", []review.Thread{{
		ID: "NEW1", Kind: review.KindThread, Author: "a", Path: "a.kt", Line: 1, Body: "fix", LastCommentID: "c1",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-2", []string{"NEW1"}); err != nil {
		t.Fatal(err)
	}

	task := Task{Ticket: "K-2", Repo: &config.Repo{Path: "/repo"},
		Cfg: &config.Config{}, Store: st, AddressComments: true}
	var logs []string
	out := addressComments(t.Context(), task, Hooks{
		Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) },
	})
	joined := strings.Join(logs, "\n")

	if out.State != store.StateNeedsYou {
		t.Fatalf("state = %q, want needs-you - a blocked fix parked as ready:\n%s", out.State, joined)
	}
	if !strings.Contains(joined, "✗ permission denied: Bash(cd Compass && ./gradlew test)") {
		t.Errorf("the denial is not in the log:\n%s", joined)
	}
	sess, err := st.Get("K-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.UnmarshalDenials(sess.Denials)) != 1 {
		t.Errorf("denials not stored: %q", sess.Denials)
	}
	if sess.ParkedFlow != "address-comments" {
		t.Errorf("parked_flow = %q, want address-comments", sess.ParkedFlow)
	}
}

// T7.1 - the PR comment's verification claim is derived from the report, not
// asserted: a compile-only verify log yields a line that does not claim tests.
func TestTestsLineNeverOverclaims(t *testing.T) {
	compileOnly := &agent.Report{VerifyLog: "compileLegacyStagingDebugSources: BUILD SUCCESSFUL in 1m 6s\n(tests not run: task still building when the session ended)"}
	got := testsLine(compileOnly)
	if strings.Contains(got, "compile + tests") {
		t.Errorf("hardcoded overclaim survived: %q", got)
	}
	if !strings.Contains(got, "compileLegacyStagingDebugSources") {
		t.Errorf("claim does not reflect the verify log: %q", got)
	}
	if got := testsLine(nil); got != "✅ agent-verified" {
		t.Errorf("nil report fallback = %q", got)
	}
	if got := testsLine(&agent.Report{}); got != "✅ agent-verified" {
		t.Errorf("empty log fallback = %q", got)
	}
}

// T7.2 - the verify prompt carries the watched-to-completion certification
// contract, so an agent cannot honestly certify a suite it never saw finish.
func TestVerifyPromptCertificationContract(t *testing.T) {
	p := agent.BuildVerifyPrompt("T-1", "s", "/wt", false, true, "Bash(ls:*)", "", "")
	for _, want := range []string{
		"CERTIFY ONLY WHAT YOU WATCHED FINISH",
		"ran to completion IN THIS SESSION",
		"still running in the background",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("verify prompt missing %q", want)
		}
	}
}

// T8.2 - every run starts with the diagnostics lines a permission post-mortem
// needs: the claude CLI version and the allowlist actually in effect.
func TestRunLogsDiagnostics(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // stop at the first stage boundary; the diagnostics precede it
	var logs []string
	Run(ctx, Task{Ticket: "D-9", Repo: &config.Repo{Path: "/nope"}, Cfg: &config.Config{}},
		Hooks{Logf: func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }})
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "claude CLI:") {
		t.Errorf("no CLI version line:\n%s", joined)
	}
	if !strings.Contains(joined, "rule(s):") {
		t.Errorf("no allowlist line:\n%s", joined)
	}
}

// The callback's Deny verdict gets its own honest park (Test D QA finding: a
// human's deliberate Deny was headlined "Configuration issue - one keystroke
// fixes it"). Human-denied leads with the decision and its two honest paths.
// (There is no timed-out sibling: a pending prompt waits for the human
// indefinitely - pie never answers a permission question on the user's behalf.)
func TestPermissionExplanationHumanDenied(t *testing.T) {
	denied := []agent.Denial{{Tool: "Bash", Command: "./gradlew :shared:jvmTest",
		ErrorText: agent.CallbackDeniedText}}
	rules := []string{"Bash(./gradlew:*)"}
	msg := permissionExplanation(denied, rules, "T-1", t.TempDir())
	for _, want := range []string{
		"You denied the agent permission to run:",
		"If that was intentional",
		"If it was a mistake",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("human-denied message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "The agent was refused permission") {
		t.Errorf("human-denied message uses the generic refusal intro:\n%s", msg)
	}
}

// The park header follows the flavor: a human Deny outranks the "one
// keystroke fixes it" header even when suggestible rules exist.
func TestDenialNeedsYouHeaderForHumanDeny(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	var comments []string
	h := Hooks{Comment: func(s string) { comments = append(comments, s) }}
	task := Task{Ticket: "T-1", Cfg: &config.Config{}}

	denialNeedsYou(task, h, t.TempDir(), []agent.Denial{{Tool: "Bash",
		Command: "./gradlew test", ErrorText: agent.CallbackDeniedText}})
	if len(comments) != 1 || !strings.Contains(comments[0], "You denied [T-1]'s commands from the dashboard") {
		t.Fatalf("human-denied header wrong:\n%v", comments)
	}
	if strings.Contains(comments[0], "Configuration issue") {
		t.Errorf("a human Deny must never be headlined as a configuration issue:\n%s", comments[0])
	}
}
