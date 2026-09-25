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
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// monorepo builds a git checkout with the project in App/ plus a worktree-like
// dir holding only the tracked files, and returns (repoRoot, worktree).
func monorepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	gitc := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitc("init", "-q")
	if err := os.MkdirAll(filepath.Join(repo, "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("local.properties\n"), 0o644))
	must(os.WriteFile(filepath.Join(repo, "App", "settings.gradle"), []byte("x\n"), 0o644))
	gitc("add", ".")
	gitc("commit", "-q", "-m", "skeleton")

	wt := t.TempDir()
	must(os.MkdirAll(filepath.Join(wt, "App"), 0o755))
	must(os.WriteFile(filepath.Join(wt, "App", "settings.gradle"), []byte("x\n"), 0o644))
	return repo, wt
}

func collectLog() (*[]string, logFn) {
	var lines []string
	return &lines, func(format string, a ...interface{}) {
		lines = append(lines, fmt.Sprintf(format, a...))
	}
}

// A seeded local.properties from the main checkout must win over the
// synthesized one, and land in the PROJECT dir.
func TestEnsureProjectFilesSeedWins(t *testing.T) {
	repo, wt := monorepo(t)
	if err := os.WriteFile(filepath.Join(repo, "App", "local.properties"),
		[]byte("sdk.dir=/real\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, logf := collectLog()
	task := Task{Ticket: "T-1",
		Repo: &config.Repo{Path: filepath.Join(repo, "App")},
		Cfg:  &config.Config{AndroidSDKPath: "/configured"}}
	projectDir := ensureProjectFiles(task, wt, logf)

	if want := filepath.Join(wt, "App"); projectDir != want {
		t.Errorf("projectDir %q, want %q", projectDir, want)
	}
	got, err := os.ReadFile(filepath.Join(wt, "App", "local.properties"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "sdk.dir=/real\n" {
		t.Errorf("seeded copy should win, got %q", got)
	}
}

// With nothing to seed, local.properties is synthesized from the configured
// SDK path - in the project dir, not the worktree root (the original bug).
func TestEnsureProjectFilesSynthesizesInProjectDir(t *testing.T) {
	repo, wt := monorepo(t)
	_, logf := collectLog()
	task := Task{Ticket: "T-1",
		Repo: &config.Repo{Path: filepath.Join(repo, "App")},
		Cfg:  &config.Config{AndroidSDKPath: "/sdk"}}
	ensureProjectFiles(task, wt, logf)

	got, err := os.ReadFile(filepath.Join(wt, "App", "local.properties"))
	if err != nil {
		t.Fatalf("project-dir local.properties missing: %v", err)
	}
	if string(got) != "sdk.dir=/sdk\n" {
		t.Errorf("got %q", got)
	}
	if _, err := os.Stat(filepath.Join(wt, "local.properties")); err == nil {
		t.Error("local.properties written at the worktree root - Gradle ignores it there")
	}
}

// An empty SDK path used to be a silent no-op; it must now be loud.
func TestEnsureProjectFilesWarnsOnEmptySDK(t *testing.T) {
	repo, wt := monorepo(t)
	lines, logf := collectLog()
	task := Task{Ticket: "T-1",
		Repo: &config.Repo{Path: filepath.Join(repo, "App")},
		Cfg:  &config.Config{}}
	ensureProjectFiles(task, wt, logf)

	all := strings.Join(*lines, "\n")
	if !strings.Contains(all, "android_sdk_path") {
		t.Errorf("expected a loud warn naming android_sdk_path, got:\n%s", all)
	}
	if _, err := os.Stat(filepath.Join(wt, "App", "local.properties")); err == nil {
		t.Error("no SDK path should mean no synthesized file")
	}
}

// The resume and ship entrypoints must HEAL the worktree (seed + SDK path)
// before doing anything else - worktrees created before seeding existed are
// missing local.properties, and re-verifying against that environment loops in
// needs-you forever. A cancelled context stops each path right after the heal,
// so no agent is ever spawned. This pins the CALL SITES; a mutation that
// removes ensureProjectFiles from either path must fail here.
func TestResumeAndShipHealWorktree(t *testing.T) {
	for _, mode := range []string{"resume", "ship"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PIE_HOME", t.TempDir())
			repo, _ := monorepo(t)
			if err := os.WriteFile(filepath.Join(repo, "App", "local.properties"),
				[]byte("sdk.dir=/real\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			// A worktree the way a pre-seeding release left it: tracked files
			// only, at the exact path the runner derives for this ticket.
			wt := paths.WorktreeFor(repo, "T-1")
			if err := os.MkdirAll(filepath.Join(wt, "App"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // stop at the first stage boundary after the heal
			task := Task{Ticket: "T-1",
				Repo:   &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
				Cfg:    &config.Config{},
				Resume: mode == "resume", Ship: mode == "ship"}
			Run(ctx, task, Hooks{})

			if _, err := os.Stat(filepath.Join(wt, "App", "local.properties")); err != nil {
				t.Errorf("%s path did not heal the worktree: %v", mode, err)
			}
		})
	}
}

func TestPermissionExplanation(t *testing.T) {
	t.Setenv("PIE_HOME", "/tmp/pie-test-home")
	denials := []agent.Denial{
		{Tool: "Bash", Command: "cd App && ./gradlew testDebugUnitTest"},
		{Tool: "Bash", Command: "chmod +x gradlew"},
	}
	msg := permissionExplanation(denials, []string{"Bash(cd:*)", "Bash(chmod +x:*)"}, "T-9", t.TempDir())

	for _, want := range []string{
		"cd App && ./gradlew testDebugUnitTest", // verbatim command, shown once up top
		"chmod +x gradlew",
		`Fix: in the dashboard, press Enter on the ticket and choose "Allow denied commands & re-run"`, // the one-keystroke fix IS the fix
		"Manual alternative:",
		"/tmp/pie-test-home/config.toml",                          // real config path
		`│ extra_allowed_tools = "Bash(cd:*) Bash(chmod +x:*)" │`, // exact TOML line, boxed
		"pie run T-9 --resume",
		"APPEND",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("explanation missing %q:\n%s", want, msg)
		}
	}
	// The old multi-step walkthrough stays gone - it buried the shortcut.
	for _, gone := range []string{"Fix it now:", "nano ", "Shortcut:", "What happened:"} {
		if strings.Contains(msg, gone) {
			t.Errorf("explanation regressed to the verbose shape (%q):\n%s", gone, msg)
		}
	}

	// Empty suggestions: no config line proposed, honest pointer instead.
	honest := permissionExplanation(denials, nil, "T-9", t.TempDir())
	if strings.Contains(honest, "extra_allowed_tools =") {
		t.Errorf("empty-rules branch must not propose a config change:\n%s", honest)
	}
	if !strings.Contains(honest, "would not help") || !strings.Contains(honest, "pie logs T-9") {
		t.Errorf("empty-rules branch should point at the log:\n%s", honest)
	}

	// A guarded denial (git state mutation): the block is a deliberate
	// guardrail, and the message must say so rather than suggest allowing it.
	guarded := []agent.Denial{{Tool: "Bash", Command: "git checkout e5087fc"}}
	gmsg := permissionExplanation(guarded, agent.SuggestAllowRules(guarded, ""), "T-9", t.TempDir())
	if strings.Contains(gmsg, "extra_allowed_tools =") {
		t.Errorf("guarded branch must not propose a config change:\n%s", gmsg)
	}
	for _, want := range []string{"git checkout e5087fc", "deliberately not offered", "pie logs T-9"} {
		if !strings.Contains(gmsg, want) {
			t.Errorf("guarded branch missing %q:\n%s", want, gmsg)
		}
	}

	// Mixed: suggestible rules still offered, the guarded command called out.
	mixed := append([]agent.Denial{{Tool: "Bash", Command: "gradle build"}}, guarded...)
	mmsg := permissionExplanation(mixed, agent.SuggestAllowRules(mixed, ""), "T-9", t.TempDir())
	if !strings.Contains(mmsg, `extra_allowed_tools = "Bash(gradle:*)"`) {
		t.Errorf("mixed branch lost the suggestible rule:\n%s", mmsg)
	}
	if !strings.Contains(mmsg, "Not fixed by this: git checkout e5087fc") {
		t.Errorf("mixed branch must call out the guarded command:\n%s", mmsg)
	}
}

// T6.1 - the ship-comments verify heals the worktree like every other
// entrypoint. The run exits at the zero-queued-comments check, which sits
// AFTER the heal (the cancelled-context trick the resume/ship test uses exits
// before it here, at the top-of-function stop guard).
func TestShipCommentsHealsWorktree(t *testing.T) {
	fakeGH(t)
	t.Setenv("PIE_HOME", t.TempDir())
	repo, _ := monorepo(t)
	if err := os.WriteFile(filepath.Join(repo, "App", "local.properties"),
		[]byte("sdk.dir=/real\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A worktree the way a pre-seeding release left it.
	wt := paths.WorktreeFor(repo, "T-2")
	if err := os.MkdirAll(filepath.Join(wt, "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Claim("T-2", repo, "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("T-2", "b", wt, "https://github.com/o/r/pull/7"); err != nil {
		t.Fatal(err)
	}

	task := Task{Ticket: "T-2",
		Repo: &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
		Cfg:  &config.Config{}, Store: st,
		ShipComments: true, PriorState: store.StateFixReview}
	Run(context.Background(), task, Hooks{})

	if _, err := os.Stat(filepath.Join(wt, "App", "local.properties")); err != nil {
		t.Errorf("ship-comments did not heal the worktree: %v", err)
	}
}
