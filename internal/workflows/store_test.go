package workflows

import (
	"os"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

func TestSaveLoadPerRepo(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	a, b := t.TempDir()+"/app", t.TempDir()+"/app" // same base name, different checkouts
	err := Save(a, map[string]string{
		agent.SlotReviewStrictness: "Only pass clean code.\r\n\n",
		agent.SlotPlanApproach:     agent.SlotDefault(agent.SlotPlanApproach), // unchanged: not stored
		"plan.extra":               "   ",                                     // blank: not stored
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[agent.SlotReviewStrictness] != "Only pass clean code." {
		t.Fatalf("loaded %q", got)
	}
	if other, _ := Load(b); len(other) != 0 {
		t.Errorf("a same-named checkout shares the customization: %q", other)
	}
	if paths.WorkflowsFor(a) == paths.WorkflowsFor(b) {
		t.Error("same file for two checkouts")
	}
	if !For(a).Customized() || For(b).Customized() {
		t.Error("For does not reflect the saved file")
	}
}

func TestSaveRejectsUnknownAndHuge(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := Save("/r", map[string]string{"verify.contract": "x"}); err == nil {
		t.Error("unknown slot accepted")
	}
	if err := Save("/r", map[string]string{"plan.extra": strings.Repeat("x", agent.MaxSlotLen+1)}); err == nil {
		t.Error("oversized slot accepted")
	}
}

// A file edited by hand with stale ids or bad JSON never stops a run.
func TestForToleratesBrokenFiles(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	_ = os.MkdirAll(paths.WorkflowsDir(), 0o755)
	_ = os.WriteFile(paths.WorkflowsFor("/r"), []byte(`{"slots":{"old.slot":"x","plan.extra":"keep"}}`), 0o644)
	if over, _ := Load("/r"); len(over) != 1 || over["plan.extra"] != "keep" {
		t.Errorf("stale ids not dropped: %q", over)
	}
	_ = os.WriteFile(paths.WorkflowsFor("/r"), []byte(`{nope`), 0o644)
	if For("/r").Customized() {
		t.Error("broken file should fall back to built-in prompts")
	}
}
