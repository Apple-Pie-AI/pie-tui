package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitIn runs a git command in dir, failing the test on error.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// commitFile writes a file and commits it.
func commitFile(t *testing.T, dir, name, body, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", msg)
}

// twoClones returns two clones of one origin, so one can push behind the other's
// back - exactly the situation that invalidates a --force-with-lease.
func twoClones(t *testing.T) (a, b string) {
	t.Helper()
	repo := setupRepo(t)
	origin := filepath.Join(filepath.Dir(repo), "origin.git")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, filepath.Dir(other), "clone", "-q", origin, other)
	gitIn(t, other, "config", "user.email", "t@t.co")
	gitIn(t, other, "config", "user.name", "T")
	return repo, other
}

func TestDivergenceAndFastForward(t *testing.T) {
	mine, theirs := twoClones(t)

	// Nobody has moved.
	if behind, ahead, err := Divergence(mine, "main"); err != nil || behind != 0 || ahead != 0 {
		t.Fatalf("Divergence = %d/%d err=%v, want 0/0", behind, ahead, err)
	}

	// A reviewer pushes a commit to the branch.
	commitFile(t, theirs, "review.md", "fix\n", "reviewer commit")
	gitIn(t, theirs, "push", "-q", "origin", "main")

	// Stale until we fetch - which is precisely why the push lease goes bad.
	if behind, _, _ := Divergence(mine, "main"); behind != 0 {
		t.Fatalf("behind = %d before fetching, want 0 (the tracking ref is stale)", behind)
	}
	if err := FetchBranch(mine, "main"); err != nil {
		t.Fatalf("FetchBranch: %v", err)
	}
	behind, ahead, err := Divergence(mine, "main")
	if err != nil || behind != 1 || ahead != 0 {
		t.Fatalf("Divergence = %d/%d err=%v, want 1/0 after fetch", behind, ahead, err)
	}

	if err := FastForward(mine, "main"); err != nil {
		t.Fatalf("FastForward: %v", err)
	}
	if behind, ahead, _ = Divergence(mine, "main"); behind != 0 || ahead != 0 {
		t.Fatalf("Divergence = %d/%d after fast-forward, want 0/0", behind, ahead)
	}
	if _, err := os.Stat(filepath.Join(mine, "review.md")); err != nil {
		t.Errorf("the reviewer's commit should be in the worktree: %v", err)
	}
}

// Genuinely diverged histories must be refused, not silently merged or rebased:
// there is no human at the keyboard to resolve a conflict in a headless run.
func TestFastForwardRefusesDivergence(t *testing.T) {
	mine, theirs := twoClones(t)

	commitFile(t, theirs, "theirs.md", "theirs\n", "their commit")
	gitIn(t, theirs, "push", "-q", "origin", "main")
	commitFile(t, mine, "mine.md", "mine\n", "my commit")

	if err := FetchBranch(mine, "main"); err != nil {
		t.Fatalf("FetchBranch: %v", err)
	}
	behind, ahead, err := Divergence(mine, "main")
	if err != nil || behind != 1 || ahead != 1 {
		t.Fatalf("Divergence = %d/%d err=%v, want 1/1", behind, ahead, err)
	}
	if err := FastForward(mine, "main"); err == nil {
		t.Fatal("FastForward must refuse a diverged branch")
	}
	// And it must leave the worktree usable, not mid-merge.
	if _, err := os.Stat(filepath.Join(mine, ".git", "MERGE_HEAD")); err == nil {
		t.Error("a refused fast-forward must not leave the worktree mid-merge")
	}
}

func TestDivergenceUnknownBranch(t *testing.T) {
	repo := setupRepo(t)
	if _, _, err := Divergence(repo, "no-such-branch"); err == nil {
		t.Fatal("want an error for a branch with no remote-tracking ref")
	}
}

func TestHeadSHA(t *testing.T) {
	repo := setupRepo(t)
	sha := HeadSHA(repo)
	if len(sha) != 40 || strings.ContainsAny(sha, " \n") {
		t.Fatalf("HeadSHA = %q, want a bare 40-char sha", sha)
	}
	commitFile(t, repo, "next.md", "x\n", "second")
	if next := HeadSHA(repo); next == sha {
		t.Error("HeadSHA should change after a commit")
	}
	if HeadSHA(filepath.Join(t.TempDir(), "nope")) != "" {
		t.Error("HeadSHA on a non-repo should be empty, not garbage")
	}
	if got := ShortSHA(sha); got != sha[:8] {
		t.Errorf("ShortSHA = %q", got)
	}
	if got := ShortSHA("abc"); got != "abc" {
		t.Errorf("ShortSHA(%q) = %q, want it unchanged", "abc", got)
	}
}
