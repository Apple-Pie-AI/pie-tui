package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// The verify agent runs from the WORKTREE ROOT, always - the same cwd as
// every other stage. A relocation into the Gradle project subdir (with the
// root passed via --add-dir) shipped on 2026-08-26 and killed the
// ship-comments verify at launch on every monorepo ticket (PLEX-57773):
// claude reads the cwd as its project, so a subdir changes which settings,
// hooks and MCP config it loads - a class of failure, not one bug. The
// monorepo problem it was solving (the agent flails between `cd X &&
// ./gradlew`, `X/gradlew` and absolute paths, none on the allowlist) is
// solved in the allowlist instead: the verify spawn derives
// Bash(<projectDir>/gradlew:*) from the repo's configured project path.
func TestVerifyAgentRunsAtWorktreeRootWithDerivedGradlewRule(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	proj := filepath.Join(root, "Compass")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := t.TempDir()
	for _, d := range []string{"Compass", ".agent"} {
		if err := os.MkdirAll(filepath.Join(wt, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	bin := t.TempDir()
	script := "#!/bin/sh\npwd > '" + filepath.Join(wt, ".agent", "cwd.txt") + "'\n" +
		"echo \"$@\" > '" + filepath.Join(wt, ".agent", "argv.txt") + "'\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	task := Task{Ticket: "K-2", Repo: &config.Repo{Path: proj}, Cfg: &config.Config{}}
	verifyWithAgent(t.Context(), task, wt, "", false, true, "",
		func(string, ...interface{}) {}, func(string, int) {})

	cwd, err := os.ReadFile(filepath.Join(wt, ".agent", "cwd.txt"))
	if err != nil {
		t.Fatalf("stub never ran: %v", err)
	}
	wantDir, _ := filepath.EvalSymlinks(wt)
	gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(string(cwd)))
	if gotDir != wantDir {
		t.Errorf("agent cwd = %q, want the worktree root %q", gotDir, wantDir)
	}
	argv, _ := os.ReadFile(filepath.Join(wt, ".agent", "argv.txt"))
	if strings.Contains(string(argv), "--add-dir") {
		t.Errorf("argv relocates the session (--add-dir):\n%s", argv)
	}
	if !strings.Contains(string(argv), "Bash(Compass/gradlew:*)") {
		t.Errorf("argv lacks the derived Bash(Compass/gradlew:*) rule - the monorepo wrapper shape is unallowed:\n%s", argv)
	}
}

// model_verify reaches the claude argv when set; unset falls back to the
// implementation model. Pinned at the spawn site because a wrong model here
// is invisible - the stage still runs, just with less judgment than the
// config promised.
func TestVerifyUsesConfiguredVerifyModel(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > '" + filepath.Join(wt, ".agent", "argv.txt") + "'\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := func(cfg *config.Config) string {
		task := Task{Ticket: "K-4", Repo: &config.Repo{Path: wt}, Cfg: cfg}
		verifyWithAgent(t.Context(), task, wt, "", false, true, "",
			func(string, ...interface{}) {}, func(string, int) {})
		argv, err := os.ReadFile(filepath.Join(wt, ".agent", "argv.txt"))
		if err != nil {
			t.Fatalf("stub never ran: %v", err)
		}
		return string(argv)
	}

	if argv := run(&config.Config{ModelImpl: "impl-model", ModelVerify: "verify-model"}); !strings.Contains(argv, "--model verify-model") {
		t.Errorf("model_verify set: argv = %q, want --model verify-model", argv)
	}
	if argv := run(&config.Config{ModelImpl: "impl-model"}); !strings.Contains(argv, "--model impl-model") {
		t.Errorf("model_verify unset: argv = %q, want the impl model fallback", argv)
	}
}

// verifyWithAgent resumes exactly the session it is handed - and a blank
// sessionID means a genuinely fresh session, no --resume at all. The
// comments-ship path deliberately passes "" (a stale resumed transcript
// re-taught a field agent its pre-fix flail habits and partial-evidence
// certification); this pins that "" actually means fresh at the spawn.
func TestVerifyResumeFlagFollowsSessionID(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > '" + filepath.Join(wt, ".agent", "argv.txt") + "'\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	run := func(sessionID string) string {
		task := Task{Ticket: "K-5", Repo: &config.Repo{Path: wt}, Cfg: &config.Config{}}
		verifyWithAgent(t.Context(), task, wt, sessionID, false, true, "",
			func(string, ...interface{}) {}, func(string, int) {})
		argv, err := os.ReadFile(filepath.Join(wt, ".agent", "argv.txt"))
		if err != nil {
			t.Fatalf("stub never ran: %v", err)
		}
		return string(argv)
	}

	if argv := run("sess-old"); !strings.Contains(argv, "--resume sess-old") {
		t.Errorf("with a session id: argv = %q, want --resume sess-old", argv)
	}
	if argv := run(""); strings.Contains(argv, "--resume") {
		t.Errorf("blank session id: argv = %q, want NO --resume (a fresh session)", argv)
	}
}

// The comments-ship verify must start a FRESH session even when the ticket
// has a recorded one. The recorded id is the comment-fix run - possibly days
// old, predating current prompts - and a field verify that resumed it
// repeated the transcript's habits over fresh instructions: absolute gradlew
// paths through approval prompts, then verified:true off partial evidence.
// This drives shipComments all the way to the verify spawn and asserts the
// stub claude saw no --resume.
func TestShipCommentsVerifyStartsFresh(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	fakeGH(t)

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("K-6", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("K-6", "b", "", "https://github.com/o/r/pull/9"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSessionID("K-6", "stale-fix-session"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("K-6", "https://github.com/o/r/pull/9", []review.Thread{{
		ID: "T1", Kind: review.KindThread, Author: "alice", Path: "a.kt", Line: 3, Body: "fix",
	}}, true); err != nil {
		t.Fatal(err)
	}
	if err := st.QueueComments("K-6", []string{"T1"}); err != nil {
		t.Fatal(err)
	}
	readyWorktree(t, "/repo", "K-6")
	wt := filepath.Join(os.Getenv("PIE_HOME"), "worktrees", "K-6")

	bin := t.TempDir()
	argvPath := filepath.Join(bin, "argv.txt")
	script := "#!/bin/sh\necho \"$@\" > '" + argvPath + "'\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	task := Task{Ticket: "K-6", Repo: &config.Repo{Path: wt}, Cfg: &config.Config{},
		Store: st, ShipComments: true, PriorState: store.StateFixReview}
	shipComments(t.Context(), task, Hooks{})

	argv, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatalf("the verify stub never ran - shipComments parked before verify: %v", err)
	}
	if strings.Contains(string(argv), "--resume") {
		t.Errorf("ship-comments verify resumed a session: argv = %q - stale transcripts re-teach retired habits", argv)
	}
}

// A root-level project (no subdirectory): worktree root as cwd, no derived
// wrapper rule - ./gradlew is already on the default allowlist.
func TestVerifyAgentRootProjectGetsNoDerivedRule(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\npwd > '" + filepath.Join(wt, ".agent", "cwd.txt") + "'\n" +
		"echo \"$@\" > '" + filepath.Join(wt, ".agent", "argv.txt") + "'\necho '{}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	task := Task{Ticket: "K-3", Repo: &config.Repo{Path: wt}, Cfg: &config.Config{}}
	verifyWithAgent(t.Context(), task, wt, "", false, true, "",
		func(string, ...interface{}) {}, func(string, int) {})

	cwd, err := os.ReadFile(filepath.Join(wt, ".agent", "cwd.txt"))
	if err != nil {
		t.Fatalf("stub never ran: %v", err)
	}
	wantDir, _ := filepath.EvalSymlinks(wt)
	gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(string(cwd)))
	if gotDir != wantDir {
		t.Errorf("agent cwd = %q, want the worktree root %q", gotDir, wantDir)
	}
	derived := regexp.MustCompile(`Bash\([^.)][^)]*/gradlew:\*\)`)
	if argv, _ := os.ReadFile(filepath.Join(wt, ".agent", "argv.txt")); strings.Contains(string(argv), "--add-dir") || derived.Match(argv) {
		t.Errorf("root-level project must get neither --add-dir nor a derived wrapper rule:\n%s", argv)
	}
}
