package agent

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updatePrompts = flag.Bool("update-prompts", false, "rewrite testdata/prompts/*.golden")

// goldenPrompts renders every stage prompt with fixed inputs. The goldens were
// written from the builders BEFORE prompts became customizable, so with no
// overrides the customizable builders must reproduce them byte for byte.
func goldenPrompts() map[string]string {
	plan := &Plan{Plan: "1. Add the screen.\n2. Wire the ViewModel."}
	cs := []ReviewComment{
		{ID: "c1", Author: "ana", Path: "app/src/Main.kt", Line: 42, Body: "Rename this.", DiffHunk: "@@ -1 +1 @@\n-a\n+b"},
		{ID: "c2", Author: "bob", Body: "Overall looks good.", UserNote: "use the retry helper", Outdated: true},
	}
	return map[string]string{
		"plan":             BuildPlanPrompt("PROJ-1", "Add login", "Users need to log in.", PlanTools),
		"implement":        BuildImplPrompt("PROJ-1", "Add login", "Users need to log in.", plan, "Read Bash(./gradlew:*)", ""),
		"implement-auto":   BuildImplPrompt("PROJ-1", "Add login", "", plan, "", "App"),
		"self-review":      BuildReviewPrompt("PROJ-1", "Add login", plan),
		"review-fix":       BuildReviewFixPrompt([]string{"missing null check", "unused import"}),
		"rework":           BuildChangeReworkPrompt("rename @app/Main.kt:3 please", 2),
		"discuss":          BuildChangeDiscussPrompt("why this approach?"),
		"verify":           BuildVerifyPrompt("PROJ-1", "Add login", "/wt", "emulator-5554", true, "Read Bash(./gradlew:*)", "", "/sdk/adb"),
		"verify-unit-only": BuildVerifyPrompt("PROJ-1", "Add login", "/wt", "", false, "", "Compass", ""),
		"comment-fix":      BuildCommentFixPrompt("PROJ-1", "Add login", "develop", "Read", cs),
		"comment-revise":   BuildCommentRevisePrompt("use the existing helper"),
		"run-repair": BuildRunRepairPrompt(RunRepairInput{ProjectDir: "/repo", Serial: "emulator-5554", Module: ":app",
			Variant: "debug", RecipeJSON: `{"version":1}`, Failure: RunFailure{Step: "build",
				Argv: []string{"/repo/gradlew", ":app:assembleDebug"}, ExitCode: 1, Tail: "e: boom"}}),
	}
}

func TestPromptsMatchGoldens(t *testing.T) {
	for name, got := range goldenPrompts() {
		path := filepath.Join("testdata", "prompts", name+".golden")
		if *updatePrompts {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v (run with -update-prompts)", err)
		}
		if got != string(want) {
			t.Errorf("%s prompt changed from its golden - with no customization prompts must be byte-identical", name)
		}
	}
}
