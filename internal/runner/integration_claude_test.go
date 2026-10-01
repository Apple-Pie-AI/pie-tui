//go:build integration

package runner

// Real-CLI integration tests. They spawn the actual `claude` binary and cost
// API tokens, so they are behind the `integration` build tag:
//
//	make integration                       # PATH's claude
//	PIE_CLAUDE_BIN=~/claude-2.1.145 make integration   # a pinned CLI version
//
// They exist because the unit suite stubs claude, and the two regressions of
// 2026-08-26 (PLEX-57773: a verify that died at launch and was reported as a
// red build; the allowlist shape a monorepo agent needs) were only visible
// against the real CLI. Every test here reads a PIE_CLAUDE_BIN override so a
// release can be checked against the oldest CLI the team still runs.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// realClaude puts the CLI under test first on PATH and returns its version.
// It also builds the real pie binary and names it as the permission-prompt
// MCP server: inside `go test`, os.Executable() is the test binary, which
// has no mcp-approve command, and every tool call then fails.
func realClaude(t *testing.T) string {
	t.Helper()
	pieBin := filepath.Join(t.TempDir(), "pie")
	build := exec.Command("go", "build", "-o", pieBin, ".")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building pie: %v\n%s", err, out)
	}
	t.Setenv("PIE_SELF_BIN", pieBin)
	// The approve server loads ~/.pie/config.toml at startup; give it one.
	home := t.TempDir()
	t.Setenv("PIE_HOME", home)
	cfg := "permissions = \"allowlist\"\nmodel_impl = \"claude-haiku-4-5-20251001\"\n" +
		"[[repo]]\n  path = \"" + home + "\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := os.Getenv("PIE_CLAUDE_BIN")
	if bin == "" {
		p, err := exec.LookPath("claude")
		if err != nil {
			t.Skip("no claude on PATH and no PIE_CLAUDE_BIN")
		}
		bin = p
	}
	dir := t.TempDir()
	if err := os.Symlink(bin, filepath.Join(dir, "claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		t.Fatalf("claude --version: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// monorepoWorktree builds the PLEX-57773 shape: a git repo whose configured
// project path is a subdirectory holding a gradlew that "passes".
func monorepoWorktree(t *testing.T, sub string) (proj, wt string) {
	t.Helper()
	root := t.TempDir()
	git := func(dir string, args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init", "-q")
	proj = filepath.Join(root, sub)
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	gradlew := "#!/bin/sh\necho \"> Task :app:test\"\necho 'BUILD SUCCESSFUL in 1s'\n"
	if err := os.WriteFile(filepath.Join(proj, "gradlew"), []byte(gradlew), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "settings.gradle.kts"), []byte("rootProject.name = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(root, "add", ".")
	git(root, "commit", "-q", "-m", "base")
	wt = filepath.Join(t.TempDir(), "PLEX-1")
	git(root, "worktree", "add", "-q", wt)
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".agent", "report.json"), []byte(`{"status":"ready_for_build"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return proj, wt
}

// The ship-comments verify on a monorepo, against the real CLI: the session
// must START (PLEX-57773 died before init), run the wrapper through the
// derived allowlist rule with no permission denial, and certify. This is the
// test that would have failed before the 2026-08-26 release.
func TestIntegrationMonorepoVerifyStartsAndCertifies(t *testing.T) {
	v := realClaude(t)
	proj, wt := monorepoWorktree(t, "Compass")

	var logs []string
	logf := func(f string, a ...interface{}) { logs = append(logs, fmt.Sprintf(f, a...)) }
	cfg := &config.Config{ModelImpl: "claude-haiku-4-5-20251001", MaxBudgetUSD: 1, Permissions: "allowlist"}
	task := Task{Ticket: "PLEX-1", Summary: "integration", Repo: &config.Repo{Path: proj}, Cfg: cfg}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rep, verified, sid, denials, errText := verifyWithAgent(ctx, task, wt, "", false, false, "", logf, func(string, int) {})
	joined := strings.Join(logs, "\n")
	t.Logf("claude %s\n%s", v, joined)

	if sid == "" {
		t.Fatalf("no session started - the CLI died at launch (errText=%q)", errText)
	}
	if len(denials) != 0 {
		t.Errorf("permission denials on a monorepo verify: %v", denials)
	}
	if rep == nil {
		t.Fatalf("no fresh report.json (errText=%q)", errText)
	}
	if !verified {
		t.Errorf("not certified; verifyLog:\n%s", rep.VerifyLog)
	}
}

// The prefix-matching semantics the monorepo allowlist depends on, pinned
// against the real CLI: Bash(<dir>/gradlew:*) allows `<dir>/gradlew -p <dir>
// <task>` (with a trailing pipe too), while a `cd <dir> && ./gradlew` rule
// does NOT allow the compound it quotes - so the prompt must never sanction
// that shape.
func TestIntegrationDerivedGradlewRuleSemantics(t *testing.T) {
	realClaude(t)
	proj, wt := monorepoWorktree(t, "Compass")
	_ = proj

	run := func(allowed, cmd string) []agent.Denial {
		res, _ := agent.Run(context.Background(),
			"Run exactly this shell command via the Bash tool and nothing else, then reply with its output: "+cmd,
			agent.Options{WorktreeDir: wt, AllowedTools: allowed, PermissionMode: "allowlist",
				Model: "claude-haiku-4-5-20251001", MaxBudgetUSD: 1, Logf: func(string, ...interface{}) {}})
		return res.Denials
	}
	if d := run("Bash(Compass/gradlew:*)", "Compass/gradlew -p Compass test 2>&1 | tail -20"); len(d) != 0 {
		t.Errorf("Bash(Compass/gradlew:*) did not allow the wrapper form: %v", d)
	}
	if d := run("Bash(cd Compass && ./gradlew:*)", "cd Compass && ./gradlew test"); len(d) == 0 {
		t.Errorf("a verbatim cd-compound rule allowed the compound - the prompt guidance rests on it being refused; re-check the sanctioned shape")
	}
}

// A CLI that cannot start must be reported as a launch failure with its own
// stderr - never as a red build. Driven with a deliberately unusable flag
// combination so the real CLI exits before init.
func TestIntegrationLaunchFailureIsNamed(t *testing.T) {
	realClaude(t)
	wt := t.TempDir()
	res, err := agent.Run(context.Background(), "p", agent.Options{
		WorktreeDir: wt, AllowedTools: "Read", PermissionMode: "allowlist",
		PermissionPromptConfig: filepath.Join(wt, "does-not-exist.json"),
		Model:                  "claude-haiku-4-5-20251001", MaxBudgetUSD: 1, Logf: func(string, ...interface{}) {}})
	if err == nil {
		t.Skip("this CLI tolerates a missing --mcp-config; nothing to assert")
	}
	if res.SessionID == "" && !strings.Contains(res.ErrorText, "before starting a session") {
		t.Errorf("launch failure not named: ErrorText=%q stderr=%q", res.ErrorText, res.Stderr)
	}
}

// The models screen's check, against the real CLI: an alias the account has
// resolves to a model of that family, and a name Claude Code refuses comes
// back as an error, not a success. Uses haiku, so a run costs well under a cent.
func TestIntegrationCheckModel(t *testing.T) {
	realClaude(t)
	ctx := context.Background()
	ok := agent.CheckModel(ctx, "haiku", "")
	if ok.Err != "" || !strings.Contains(ok.Resolved, "haiku") || ok.Mismatch("haiku") {
		t.Fatalf("haiku check = %+v, want a haiku model with no error", ok)
	}
	bad := agent.CheckModel(ctx, "claude-nonexistent-9", "")
	if bad.Err == "" || bad.Resolved != "" {
		t.Fatalf("a made-up model must be refused, got %+v", bad)
	}
	t.Logf("haiku → %s; refusal: %s", ok.Resolved, bad.Err)
}
