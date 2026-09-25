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

// repoWithBranch builds a work repo (with bare origin, as ValidateRepo
// requires) whose branch "pr-fix" carries a commit adding fix.txt, then
// returns to main so the branch is free for a worktree.
func repoWithBranch(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "work")
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
	gitc(root, "init", "-q", "--bare", origin)
	gitc(root, "init", "-q", repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", "-A")
	gitc(repo, "commit", "-qm", "init")
	gitc(repo, "branch", "-M", "main")
	gitc(repo, "remote", "add", "origin", origin)
	gitc(repo, "push", "-qu", "origin", "main")
	gitc(repo, "checkout", "-qb", "pr-fix")
	if err := os.WriteFile(filepath.Join(repo, "fix.txt"), []byte("the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc(repo, "add", "-A")
	gitc(repo, "commit", "-qm", "the fix")
	gitc(repo, "checkout", "-q", "main")
	return repo
}

// A FromBranch run must check the existing branch out AS-IS - its commits are
// the work being continued (PR feedback, agent code review), and the create
// path's -B reset would destroy them. Cancelling from OnField (which fires
// right after worktree provisioning) stops the run before any agent stage.
func TestRunFromBranchChecksOutExisting(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	repo := repoWithBranch(t)

	ctx, cancel := context.WithCancel(context.Background())
	var logLines []string
	Run(ctx, Task{
		Ticket: "FB-1",
		Repo:   &config.Repo{Path: repo, Branch: "{ticket}-{slug}"},
		Cfg:    &config.Config{},
		Branch: "pr-fix", FromBranch: true,
	}, Hooks{
		Logf:    func(f string, a ...interface{}) { logLines = append(logLines, fmt.Sprintf(f, a...)) },
		OnField: func(_, _, _ string) { cancel() },
	})

	wt := paths.WorktreeFor(repo, "FB-1")
	if _, err := os.Stat(filepath.Join(wt, "fix.txt")); err != nil {
		t.Fatalf("existing branch commit missing from the worktree (reset instead of checkout?): %v", err)
	}
	joined := strings.Join(logLines, "\n")
	if !strings.Contains(joined, "existing branch pr-fix") {
		t.Errorf("log should say the run is on an existing branch:\n%s", joined)
	}
}
