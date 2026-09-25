package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestProjectDirIn(t *testing.T) {
	// Git root with the Android project in a subdirectory (monorepo layout).
	root := t.TempDir()
	repo := filepath.Join(root, "Compass")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "init", "-q").Run(); err != nil {
		t.Skip("git unavailable:", err)
	}

	// The worktree mirrors the repo, so the project sits at <wt>/Compass.
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, "Compass"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := ProjectDirIn(wt, repo), filepath.Join(wt, "Compass"); got != want {
		t.Errorf("subdir repo: got %q, want %q", got, want)
	}

	// Repo path IS the git root → open the worktree root itself.
	if got := ProjectDirIn(wt, root); got != wt {
		t.Errorf("root repo: got %q, want %q", got, wt)
	}

	// Not a git repo at all → fall back to the worktree root.
	if got := ProjectDirIn(wt, t.TempDir()); got != wt {
		t.Errorf("non-git repo: got %q, want %q", got, wt)
	}
}
