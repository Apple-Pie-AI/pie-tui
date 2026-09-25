//go:build evals

// Baseline evals for PR B: deterministic, offline demonstrations that the
// CURRENT pipeline misreports failures. Each drives the real runner against a
// stub `claude` (first on PATH) that emits canned stream-json - no API calls,
// no cost, same result every run.
//
// These assert the BUGGY behavior on purpose: they are the "before" pictures.
// PR B flips each assertion into its honest counterpart and they become the
// regression suite. Run with:
//
//	GOTOOLCHAIN=auto go test -tags evals ./internal/runner -run TestEvalBaseline -v
package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// stubClaude writes a fake `claude` into its own dir and prepends it to PATH.
// EVAL_MODE selects the canned behavior.
func stubClaude(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$EVAL_MODE" in
denial)
  # A verify session whose build command was refused by the permission system:
  # the result event carries permission_denials (verified real shape, CLI
  # 2.1.220) and the agent reports verified=false.
  mkdir -p .agent
  cat > .agent/report.json <<'EOF'
{"status":"ready_for_build","summary":"could not verify","verified":false,"verifyLog":"Could not run the build: the gradle command was refused by permission requirements."}
EOF
  echo '{"type":"system","subtype":"init","session_id":"evaldeni","model":"stub"}'
  echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"bundle exec fastlane test"}}]}}'
  echo '{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"This command requires approval"}]}}'
  echo '{"type":"result","subtype":"success","result":"verification blocked","session_id":"evaldeni","permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"bundle exec fastlane test"}}]}'
  ;;
apierror)
  # A plan session killed by an API-level failure before doing any work.
  echo '{"type":"system","subtype":"init","session_id":"evalapie","model":"stub"}'
  echo '{"type":"assistant","message":{"content":[{"type":"text","text":"API Error: Request rejected (429) - ExceededBudget: over budget. Spend=105.24, Budget=105.0"}]}}'
  echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: Request rejected (429) - ExceededBudget: over budget. Spend=105.24, Budget=105.0","session_id":"evalapie"}'
  exit 1
  ;;
apierror-verify)
  # A VERIFY session killed by the API before it could run anything: no
  # report.json is written at all.
  echo '{"type":"system","subtype":"init","session_id":"evalvapi","model":"stub"}'
  echo '{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: Request rejected (429) - ExceededBudget","session_id":"evalvapi"}'
  exit 1
  ;;
denial-noreport)
  # A verify session that was refused and then died before writing report.json
  # - the worst case: no self-report, only the stream denial.
  echo '{"type":"system","subtype":"init","session_id":"evaldnr","model":"stub"}'
  echo '{"type":"result","subtype":"success","result":"blocked","session_id":"evaldnr","permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"bundle exec fastlane test"}}]}'
  ;;
esac
`
	p := filepath.Join(dir, "claude")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// evalRepo builds a minimal git repo with a bare origin (CreateWorktree fetches).
func evalRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	repo := filepath.Join(base, "repo")
	gitc := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitc(base, "init", "-q", "--bare", origin)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", ".")
	gitc(repo, "commit", "-q", "-m", "x")
	gitc(repo, "branch", "-m", "main")
	gitc(repo, "remote", "add", "origin", origin)
	gitc(repo, "push", "-qu", "origin", "main")
	return repo
}

// EVAL 1 (flipped after PR B): a verify run blocked by the permission system
// parks at needs-you with the HONEST message - the denial marked in the log,
// the denied command verbatim in the human-facing comment, and the exact
// extra_allowed_tools remediation. Before PR B this eval asserted the inverse
// (blame-the-build, denial invisible).
func TestEvalBaselineDenialMisreported(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	t.Setenv("EVAL_MODE", "denial")
	stubClaude(t)
	repo := evalRepo(t)

	// A ready worktree, as --resume expects (verify-only path: no plan needed).
	wt := paths.WorktreeFor(repo, "EV-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var lines, comments []string
	out := Run(context.Background(), Task{
		Ticket: "EV-1", Summary: "eval",
		Repo:   &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
		Cfg:    &config.Config{},
		Resume: true,
	}, Hooks{
		Logf:    func(f string, a ...interface{}) { lines = append(lines, fmt.Sprintf(f, a...)) },
		Comment: func(text string) { comments = append(comments, text) },
	})

	if out.State != "needs-you" {
		t.Fatalf("state = %q, want needs-you", out.State)
	}
	log, msg := strings.Join(lines, "\n"), strings.Join(comments, "\n")
	t.Logf("session log:\n%s\nhuman-facing comment:\n%s", log, msg)

	// The refusal is marked in the ticket log as it happens.
	if !strings.Contains(log, "✗ permission denied: Bash(bundle exec fastlane test)") {
		t.Errorf("denial not marked in the session log")
	}
	// The human-facing message classifies honestly and remediates precisely:
	// the verbatim command, the exact config line, and the dashboard shortcut.
	for _, want := range []string{
		"Configuration issue on [EV-1]", // honest header, not "needs your help"
		"Fix: in the dashboard",         // the one-keystroke fix leads
		"bundle exec fastlane test",
		`extra_allowed_tools = "Bash(bundle:*)"`,
		"Allow denied commands & re-run",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("honest message missing %q", want)
		}
	}
	// And it no longer blames the build.
	if strings.Contains(msg, "build or tests failed") || strings.Contains(msg, "still red") {
		t.Errorf("message still blames the build for a permissions problem")
	}
	t.Log("FLIPPED EVAL HOLDS: denial marked in log; honest classification + verbatim remediation in the message")
}

// EVAL 2 (baseline): a plan stage killed by an API error (429 budget) parks the
// ticket at needs-you blaming ticket ambiguity, with the real error absent from
// the human-facing message (it exists only deep in the session log).
func TestEvalBaselineAPIErrorMisreported(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	t.Setenv("EVAL_MODE", "apierror")
	stubClaude(t)
	repo := evalRepo(t)
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	var comments []string
	out := Run(context.Background(), Task{
		Ticket: "EV-2", Summary: "eval",
		Repo: &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
		Cfg:  &config.Config{},
	}, Hooks{
		Comment: func(text string) { comments = append(comments, text) },
	})

	if out.State != "needs-you" {
		t.Fatalf("state = %q, want needs-you", out.State)
	}
	msg := strings.Join(comments, "\n")
	t.Logf("human-facing comment:\n%s", msg)

	// Flipped after PR B: the real cause verbatim, no blame on the ticket.
	for _, want := range []string{"API Error", "429", "ExceededBudget", "account/API problem"} {
		if !strings.Contains(msg, want) {
			t.Errorf("honest message missing %q", want)
		}
	}
	if strings.Contains(msg, "too ambiguous") {
		t.Errorf("message still blames the ticket for an API failure")
	}
	t.Log("FLIPPED EVAL HOLDS: API error shown verbatim; ticket no longer blamed")
}

// EVAL 5: an API failure during VERIFY (no denials, no report) must classify
// as an account/API problem - not "the build or tests are still red", which is
// what the resume path said before this guard.
func TestEvalVerifyAPIErrorClassified(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	t.Setenv("EVAL_MODE", "apierror-verify")
	stubClaude(t)
	repo := evalRepo(t)
	wt := paths.WorktreeFor(repo, "EV-5")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var comments []string
	out := Run(context.Background(), Task{
		Ticket: "EV-5", Summary: "eval",
		Repo:   &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
		Cfg:    &config.Config{},
		Resume: true,
	}, Hooks{Comment: func(s string) { comments = append(comments, s) }})

	if out.State != "needs-you" {
		t.Fatalf("state = %q, want needs-you", out.State)
	}
	msg := strings.Join(comments, "\n")
	t.Logf("comment:\n%s", msg)
	for _, want := range []string{"API Error", "429", "account/API problem"} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(msg, "still red") {
		t.Errorf("API failure blamed on the build:\n%s", msg)
	}
}

// EVAL 6: a denial with NO report.json at all (session died right after the
// refusal) still classifies as a permission block with full remediation - the
// stream signal alone carries the flow.
func TestEvalDenialWithoutReportClassified(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	t.Setenv("EVAL_MODE", "denial-noreport")
	stubClaude(t)
	repo := evalRepo(t)
	wt := paths.WorktreeFor(repo, "EV-6")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var comments []string
	out := Run(context.Background(), Task{
		Ticket: "EV-6", Summary: "eval",
		Repo:   &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
		Cfg:    &config.Config{},
		Resume: true,
	}, Hooks{Comment: func(s string) { comments = append(comments, s) }})

	if out.State != "needs-you" {
		t.Fatalf("state = %q, want needs-you", out.State)
	}
	msg := strings.Join(comments, "\n")
	for _, want := range []string{"Configuration issue on [EV-6]", "bundle exec fastlane test", `extra_allowed_tools = "Bash(bundle:*)"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}
