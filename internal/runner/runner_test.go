package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Remove deprecated lint baseline entries": "remove-deprecated-lint-baseline-entries",
		"Fix NPE in LoginActivity!!!":             "fix-npe-in-loginactivity",
		"   ":                                     "change",
		"Bump kotlin 1.9 → 2.0":                   "bump-kotlin-1-9-2-0",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorktreeReady(t *testing.T) {
	dir := t.TempDir()
	if paths.WorktreeReady(dir) {
		t.Error("empty dir should not be a ready worktree")
	}
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !paths.WorktreeReady(dir) {
		t.Error("dir with a .git link should be a ready worktree")
	}
}

func TestResumeRefusesWithoutWorktree(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	out := Run(context.Background(), Task{
		Ticket:  "NOPE-1",
		Summary: "x",
		Resume:  true,
		Repo:    &config.Repo{Branch: "ai/{ticket}-{slug}"},
		Cfg:     &config.Config{},
	}, Hooks{})
	if out.State != store.StateFailed || out.Err == nil {
		t.Errorf("resume with no worktree should fail fast, got state=%q err=%v", out.State, out.Err)
	}
}

// TestFromPlanWithoutPlanFailsFast: the approve path (--from-plan) with no
// worktree/plan can't produce work - it must stop fast (failed on worktree
// creation, or needs-you when the plan is missing), never silently proceed.
// Mirrors TestResumeRefusesWithoutWorktree.
func TestFromPlanWithoutPlanFailsFast(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	out := Run(context.Background(), Task{
		Ticket:   "NOPE-2",
		Summary:  "x",
		FromPlan: true,
		Repo:     &config.Repo{Path: filepath.Join(t.TempDir(), "no-such-repo"), Branch: "ai/{ticket}-{slug}"},
		Cfg:      &config.Config{},
	}, Hooks{})
	// Exactly needs-you, not "failed or needs-you": the missing-plan guard runs
	// BEFORE worktree creation, so needs-you is what proves the guard fired.
	// Accepting failed too would keep this test green with the guard deleted -
	// it would then fail later, on the non-existent repo, for a different reason.
	if out.State != store.StateNeedsYou {
		t.Errorf("from-plan with no plan should stop at needs-you (the guard), got state=%q err=%v", out.State, out.Err)
	}
	if out.Err != nil {
		t.Errorf("the guard should stop cleanly, not error: %v", out.Err)
	}
}

func TestPrTitleFor(t *testing.T) {
	dir := t.TempDir()

	// No plan.json → no type prefix.
	got := prTitleFor(dir, "PROJ-1", "Fix login")
	if got != "[PROJ-1] Fix login" {
		t.Errorf("no plan: got %q", got)
	}

	// Write a feature plan.
	agentDir := filepath.Join(dir, ".agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "plan.json"),
		[]byte(`{"type":"feature","plan":"x","steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = prTitleFor(dir, "PROJ-2", "Add upload flow")
	if got != "[feat][PROJ-2] Add upload flow" {
		t.Errorf("feature plan: got %q", got)
	}

	// Bug plan.
	if err := os.WriteFile(filepath.Join(agentDir, "plan.json"),
		[]byte(`{"type":"bug","plan":"x","steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = prTitleFor(dir, "PROJ-3", "Fix crash")
	if got != "[bug][PROJ-3] Fix crash" {
		t.Errorf("bug plan: got %q", got)
	}

	// Chore plan.
	if err := os.WriteFile(filepath.Join(agentDir, "plan.json"),
		[]byte(`{"type":"chore","plan":"x","steps":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got = prTitleFor(dir, "PROJ-4", "Clean up deps")
	if got != "[chore][PROJ-4] Clean up deps" {
		t.Errorf("chore plan: got %q", got)
	}
}

func TestResolveBranch(t *testing.T) {
	cases := map[string]struct{ pattern, ticket, summary, want string }{
		"default":   {"{ticket}-{slug}", "PROJ-1", "Add pull to refresh", "proj-1-add-pull-to-refresh"},
		"feature":   {"feature/{ticket}", "PROJ-2", "Fix bug", "feature/proj-2"},
		"nested":    {"{ticket}/{slug}", "PROJ-3", "Clean up", "proj-3/clean-up"},
		"no tokens": {"static-branch", "PROJ-4", "x", "static-branch"},
	}
	for name, c := range cases {
		if got := ResolveBranch(c.pattern, c.ticket, c.summary); got != c.want {
			t.Errorf("%s: ResolveBranch(%q) = %q, want %q", name, c.pattern, got, c.want)
		}
	}
}

// archivePlan writes the durable plan copies (md + json) into Apple Pie's own
// home - they must never rely on the worktree's git-excluded .agent/ scratch.
func TestArchivePlan(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	plan := &agent.Plan{Plan: "do the thing", Steps: []string{"a", "b"}, Confidence: "high", Type: "bug"}
	archivePlan("PROJ-7", "Fix crash", plan, func(string, ...interface{}) {})

	md, err := os.ReadFile(paths.PlanMDFor("PROJ-7"))
	if err != nil {
		t.Fatalf("plan .md not archived: %v", err)
	}
	for _, want := range []string{"# PROJ-7 · Fix crash", "do the thing", "1. a"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("archived md missing %q:\n%s", want, md)
		}
	}
	raw, err := os.ReadFile(paths.PlanJSONFor("PROJ-7"))
	if err != nil {
		t.Fatalf("plan .json not archived: %v", err)
	}
	var back agent.Plan
	if jerr := json.Unmarshal(raw, &back); jerr != nil || back.Plan != "do the thing" {
		t.Errorf("archived json should round-trip, got %+v (%v)", back, jerr)
	}
}

// branchFor: an explicit branch override wins over the repo pattern; without
// one (and no store) the pattern is resolved as before.
func TestBranchForOverride(t *testing.T) {
	task := Task{
		Ticket: "PROJ-9", Summary: "Fix crash",
		Repo: &config.Repo{Branch: "{ticket}-{slug}"},
	}
	if got := branchFor(task); got != "proj-9-fix-crash" {
		t.Errorf("no override should resolve the pattern, got %q", got)
	}
	task.Branch = "pie/PROJ-9-custom"
	if got := branchFor(task); got != "pie/PROJ-9-custom" {
		t.Errorf("override should be used verbatim, got %q", got)
	}
}

// resolveAVD prefers the configured avd_name and never shells out for it; with
// no config it auto-detects (returns "" here since no emulator binary/AVDs exist
// in the test environment).
func TestResolveAVD(t *testing.T) {
	var logged bool
	logf := func(string, ...interface{}) { logged = true }

	// Configured name wins - returned as-is, no `emulator -list-avds` call.
	if got := resolveAVD("Pixel_6", emulator.SDKPaths{Emulator: "/no/such/emulator"}, logf); got != "Pixel_6" {
		t.Errorf("configured: got %q, want Pixel_6", got)
	}
	if logged {
		t.Error("configured avd should not log an auto-detect line")
	}

	// No config + no emulator binary → "" (caller falls back to unit tests).
	if got := resolveAVD("", emulator.SDKPaths{Emulator: "/no/such/emulator"}, logf); got != "" {
		t.Errorf("auto-detect with no emulator: got %q, want \"\"", got)
	}
}
