package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/workflows"
)

// A repo's saved workflow reaches the agent a run spawns - next to the
// unchanged contract - and a repo without one gets the built-in prompt.
func TestRunUsesTheRepoWorkflow(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	custom, plain := t.TempDir(), t.TempDir()
	if err := workflows.Save(custom, map[string]string{
		"verify.extra":           "Use ./scripts/ci-test.sh; it wraps our flaky-test retries.",
		agent.SlotVerifyApproach: "Read docs/BUILD.md first.",
	}); err != nil {
		t.Fatal(err)
	}

	argvFor := func(repo string) string {
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
		task := Task{Ticket: "K-3", Repo: &config.Repo{Path: repo}, Cfg: &config.Config{}}
		verifyWithAgent(t.Context(), task, wt, "", "", true, "", func(string, ...interface{}) {}, func(string, int) {})
		argv, err := os.ReadFile(filepath.Join(wt, ".agent", "argv.txt"))
		if err != nil {
			t.Fatalf("stub never ran: %v", err)
		}
		return string(argv)
	}

	got := argvFor(custom)
	for _, want := range []string{"1. Read docs/BUILD.md first.", "TEAM INSTRUCTIONS for this stage:\nUse ./scripts/ci-test.sh",
		`"verified": true | false`, `prefixed exactly "BLOCKED: "`} {
		if !strings.Contains(got, want) {
			t.Errorf("customized verify prompt lacks %q", want)
		}
	}
	got = argvFor(plain)
	if strings.Contains(got, "TEAM INSTRUCTIONS") || !strings.Contains(got, agent.SlotDefault(agent.SlotVerifyApproach)) {
		t.Error("a repo with no workflow file should get the built-in verify prompt")
	}
}
