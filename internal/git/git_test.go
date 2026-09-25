package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// setupRepo creates a work repo with a bare origin and one commit on main.
func setupRepo(t *testing.T) (repo string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo = filepath.Join(root, "work")
	must := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	must(root, "init", "-q", "--bare", origin)
	must(root, "init", "-q", repo)
	must(repo, "config", "user.email", "t@t.co")
	must(repo, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(repo, "add", "-A")
	must(repo, "commit", "-qm", "init")
	must(repo, "branch", "-M", "main")
	must(repo, "remote", "add", "origin", origin)
	must(repo, "push", "-qu", "origin", "main")
	return repo
}

func TestCreateWorktreeIdempotent(t *testing.T) {
	repo := setupRepo(t)
	wt := filepath.Join(t.TempDir(), "KAN-1")
	branch := "ai/kan-1-x"

	// First create succeeds and checks out a real worktree.
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if !fileExists(filepath.Join(wt, ".git")) || !fileExists(filepath.Join(wt, "README.md")) {
		t.Fatal("worktree not populated on first create")
	}

	// Re-create over the existing (registered) worktree: must still succeed.
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatalf("re-create over registered worktree: %v", err)
	}

	// The real-world bug: a stale, UNREGISTERED leftover dir occupies the path
	// (branch lingers, dir has junk, no .git). Simulate it and re-create.
	_, _ = run(repo, "worktree", "remove", "--force", wt)
	_ = os.MkdirAll(filepath.Join(wt, ".idea"), 0o755) // leftover from an IDE
	_ = os.WriteFile(filepath.Join(wt, ".idea", "x"), []byte("junk"), 0o644)

	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatalf("re-create over stale leftover dir: %v", err)
	}
	if !fileExists(filepath.Join(wt, ".git")) || !fileExists(filepath.Join(wt, "README.md")) {
		t.Fatal("worktree not populated after clearing stale dir")
	}
}

func TestCreateWorktreeMissingRepo(t *testing.T) {
	// A configured repo path that doesn't exist must yield a clear, actionable
	// error - not git's opaque "chdir <path>: no such file or directory".
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	wt := filepath.Join(t.TempDir(), "KAN-1")

	err := CreateWorktree(missing, wt, "ai/kan-1-x", "main")
	if err == nil {
		t.Fatal("expected error for missing repo path, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "chdir") {
		t.Fatalf("error should name the missing repo path, not leak git's chdir error: %v", err)
	}
}

func TestValidateRepo(t *testing.T) {
	good := setupRepo(t)

	// A directory that exists and is a git repo but has no origin remote.
	noOrigin := filepath.Join(t.TempDir(), "no-origin")
	must := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(noOrigin, 0o755); err != nil {
		t.Fatal(err)
	}
	must(noOrigin, "init", "-q")

	// A path that exists but is a plain file, not a directory.
	notDir := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		repo string
		want string // substring the error must contain; "" means expect no error
	}{
		{"valid", good, ""},
		{"missing", filepath.Join(t.TempDir(), "nope"), "does not exist"},
		{"not a directory", notDir, "not a directory"},
		{"not a git repo", t.TempDir(), "not a git repository"},
		{"no origin remote", noOrigin, "no \"origin\" remote"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRepo(tc.repo)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should contain %q, got %v", tc.want, err)
			}
			// No message may leak git's opaque chdir/fatal internals as the headline.
			if strings.Contains(err.Error(), "chdir") {
				t.Fatalf("error leaks git's chdir internals: %v", err)
			}
		})
	}
}

func TestSuggestRepos(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	parent := t.TempDir()
	must := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	mkrepo := func(name string, origin bool) string {
		dir := filepath.Join(parent, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		must(dir, "init", "-q")
		if origin {
			must(dir, "remote", "add", "origin", "https://example.com/x.git")
		}
		return dir
	}
	withOrigin := mkrepo("app-with-origin", true)
	noOrigin := mkrepo("app-no-origin", false)
	if err := os.MkdirAll(filepath.Join(parent, "not-a-repo"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Point at a sibling that doesn't exist - the real checkouts sit next to it.
	got := SuggestRepos(filepath.Join(parent, "android-app"))

	byPath := map[string]RepoSuggestion{}
	for _, s := range got {
		byPath[s.Path] = s
	}
	if s, ok := byPath[withOrigin]; !ok || !s.HasOrigin {
		t.Errorf("origin repo should be suggested with HasOrigin=true, got %+v (ok=%v)", s, ok)
	}
	if s, ok := byPath[noOrigin]; !ok || s.HasOrigin {
		t.Errorf("no-origin repo should be suggested with HasOrigin=false, got %+v (ok=%v)", s, ok)
	}
	if _, ok := byPath[filepath.Join(parent, "not-a-repo")]; ok {
		t.Error("a non-git directory must never be suggested")
	}
	// Origin-having repos must all precede any without one (the picker's default
	// selection depends on it). Robust to the extra cwd-root suggestion.
	seenNoOrigin := false
	for _, s := range got {
		if !s.HasOrigin {
			seenNoOrigin = true
		} else if seenNoOrigin {
			t.Errorf("origin repo %q sorted after a no-origin one", s.Path)
		}
	}
}

func TestClone(t *testing.T) {
	src := setupRepo(t) // a real checkout with a commit; a local path is a valid clone URL

	// Into an existing parent.
	dst := filepath.Join(t.TempDir(), "cloned")
	if err := Clone(src, dst); err != nil {
		t.Fatalf("clone: %v", err)
	}
	if !isGitRepo(dst) {
		t.Fatal("clone did not produce a git repo")
	}
	// Into a path whose leading directories don't exist yet.
	nested := filepath.Join(t.TempDir(), "a", "b", "cloned")
	if err := Clone(src, nested); err != nil {
		t.Fatalf("clone into nested path: %v", err)
	}
	if !isGitRepo(nested) {
		t.Fatal("clone did not create leading directories")
	}
}

func TestBranches(t *testing.T) {
	repo := setupRepo(t)
	must := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	// Create a local-only branch to test dedup logic.
	must(repo, "checkout", "-b", "feature/local-only")
	must(repo, "checkout", "main")
	// Push a remote branch the local side doesn't have locally.
	must(repo, "push", "origin", "HEAD:refs/heads/feature/remote-only")
	must(repo, "fetch", "origin")

	branches, err := Branches(repo)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Branch{}
	for _, b := range branches {
		byName[b.Name] = b
	}

	// main: exists locally and remotely → should appear as local.
	if b, ok := byName["main"]; !ok || b.Remote {
		t.Errorf("main: want local, got %+v ok=%v", b, ok)
	}
	// local-only branch.
	if b, ok := byName["feature/local-only"]; !ok || b.Remote {
		t.Errorf("feature/local-only: want local, got %+v ok=%v", b, ok)
	}
	// remote-only branch.
	if b, ok := byName["feature/remote-only"]; !ok || !b.Remote {
		t.Errorf("feature/remote-only: want remote, got %+v ok=%v", b, ok)
	}
	// origin/HEAD sentinel must not appear.
	if _, ok := byName["HEAD"]; ok {
		t.Error("HEAD sentinel should be filtered out")
	}
}

func TestRefExists(t *testing.T) {
	repo := setupRepo(t)
	if !RefExists(repo, "main") {
		t.Error("main should exist")
	}
	if !RefExists(repo, "origin/main") {
		t.Error("origin/main should exist")
	}
	if RefExists(repo, "no-such-branch-xyz") {
		t.Error("no-such-branch-xyz should not exist")
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// CreateWorktree must exclude Apple Pie's .agent/ scratch dir so it never lands
// in a commit - even though the agent writes report.json there and the
// orchestrator commits with `git add -A`.
func TestWorktreeExcludesAgentDir(t *testing.T) {
	repo := setupRepo(t)
	wt := filepath.Join(t.TempDir(), "KAN-1")
	if err := CreateWorktree(repo, wt, "ai/kan-1-x", "main"); err != nil {
		t.Fatal(err)
	}
	// The agent writes its report here; an ordinary source file lands too.
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".agent", "report.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "Real.kt"), []byte("class Real"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(wt, "work"); err != nil {
		t.Fatal(err)
	}
	files, err := run(wt, "show", "--name-only", "--pretty=format:", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(files, ".agent/") {
		t.Errorf(".agent/ leaked into the commit:\n%s", files)
	}
	if !strings.Contains(files, "Real.kt") {
		t.Errorf("expected the real source file to be committed, got:\n%s", files)
	}
}

func TestPushForceAllowsRerun(t *testing.T) {
	repo := setupRepo(t)
	wt := filepath.Join(t.TempDir(), "KAN-1")
	branch := "ai/kan-1-x"

	// First run: create worktree, commit, push (creates the remote branch).
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(wt, "v1"); err != nil {
		t.Fatal(err)
	}
	if err := Push(wt, branch); err != nil {
		t.Fatalf("first push: %v", err)
	}

	// Fresh re-run: CreateWorktree resets the branch to origin/main (-B), new
	// commit → divergent history. A plain push is rejected (non-fast-forward);
	// force-with-lease must let it through.
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CommitAll(wt, "v2"); err != nil {
		t.Fatal(err)
	}
	if err := Push(wt, branch); err != nil {
		t.Fatalf("re-run push should force past the divergent remote branch: %v", err)
	}
}

// parseStaleWorktreePath feeds os.RemoveAll. Stopping at the FIRST quote turned a
// repo path containing an apostrophe into a truncated ancestor directory, which
// Apple Pie then deleted recursively - the highest-blast-radius bug in the repo.
func TestParseStaleWorktreePath(t *testing.T) {
	const pre = "git worktree add -B ai/kan-1: exit status 128\nfatal: 'ai/kan-1' "
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{
			"plain path",
			pre + "is already used by worktree at '/Users/me/.pie/worktrees/KAN-1'\n",
			"/Users/me/.pie/worktrees/KAN-1",
		},
		{
			"path containing an apostrophe",
			pre + "is already used by worktree at '/Users/me/Mike's Repos/KAN-1'\n",
			"/Users/me/Mike's Repos/KAN-1",
		},
		{
			"two apostrophes",
			pre + "is already used by worktree at '/a/b's/c's/KAN-1'\n",
			"/a/b's/c's/KAN-1",
		},
		{
			"trailing quoted text on a later line is ignored",
			pre + "is already used by worktree at '/Users/me/wt/KAN-1'\nhint: use 'git worktree remove'\n",
			"/Users/me/wt/KAN-1",
		},
		{"no marker", "fatal: something else entirely\n", ""},
		{"unterminated quote", pre + "is already used by worktree at '/Users/me/wt/KAN-1\n", ""},
		{"empty path", pre + "is already used by worktree at ''\n", ""},
		{"empty message", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseStaleWorktreePath(c.msg); got != c.want {
				t.Errorf("parseStaleWorktreePath() = %q, want %q", got, c.want)
			}
		})
	}
}

// Even a correctly-parsed path is only safe to RemoveAll when Apple Pie owns it.
// This pins the pairing of the parser with the containment gate the caller uses.
func TestStaleWorktreeDeleteIsGatedByOwnership(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIE_HOME", tmp)

	owned := filepath.Join(tmp, "worktrees", "KAN-1")
	msg := "fatal: 'b' is already used by worktree at '" + owned + "'\n"
	if got := parseStaleWorktreePath(msg); got != owned || !paths.UnderWorktrees(got) {
		t.Errorf("owned worktree %q should parse and be deletable, got %q", owned, got)
	}

	// A truncated/hostile path must parse but be refused by the ownership gate.
	for _, p := range []string{"/Users/me", "/", "/etc"} {
		msg := "fatal: 'b' is already used by worktree at '" + p + "'\n"
		got := parseStaleWorktreePath(msg)
		if got != p {
			t.Fatalf("parse = %q, want %q", got, p)
		}
		if paths.UnderWorktrees(got) {
			t.Errorf("UnderWorktrees(%q) = true - Apple Pie would rm -rf a path it does not own", got)
		}
	}
}

// CheckoutWorktree on a local branch must PRESERVE its commits - the whole
// point of start-from-branch is working on what's already there, and the
// CreateWorktree `-B` reset destroying prior commits is exactly the hazard
// this function exists to avoid.
func TestCheckoutWorktreePreservesLocalCommits(t *testing.T) {
	repo := setupRepo(t)
	must := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	must(repo, "checkout", "-qb", "pr-fix")
	if err := os.WriteFile(filepath.Join(repo, "fix.txt"), []byte("the fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(repo, "add", "-A")
	must(repo, "commit", "-qm", "the fix commit")
	must(repo, "checkout", "-q", "main") // free the branch for the worktree

	wt := filepath.Join(t.TempDir(), "T-1")
	if err := CheckoutWorktree(repo, wt, "pr-fix"); err != nil {
		t.Fatalf("checkout worktree: %v", err)
	}
	if !fileExists(filepath.Join(wt, "fix.txt")) {
		t.Fatal("existing branch commit was lost - the worktree must check out the branch as-is, never reset it")
	}
}

// A branch that only exists on origin gets a local tracking branch.
func TestCheckoutWorktreeRemoteOnlyBranch(t *testing.T) {
	repo := setupRepo(t)
	must := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	must(repo, "checkout", "-qb", "remote-fix")
	if err := os.WriteFile(filepath.Join(repo, "remote.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	must(repo, "add", "-A")
	must(repo, "commit", "-qm", "remote commit")
	must(repo, "push", "-qu", "origin", "remote-fix")
	must(repo, "checkout", "-q", "main")
	must(repo, "branch", "-qD", "remote-fix") // now origin-only

	wt := filepath.Join(t.TempDir(), "T-2")
	if err := CheckoutWorktree(repo, wt, "remote-fix"); err != nil {
		t.Fatalf("checkout remote-only branch: %v", err)
	}
	if !fileExists(filepath.Join(wt, "remote.txt")) {
		t.Fatal("remote branch content missing from the worktree")
	}
	if out, _ := run(wt, "rev-parse", "--abbrev-ref", "HEAD"); strings.TrimSpace(out) != "remote-fix" {
		t.Fatalf("worktree HEAD = %q, want local branch remote-fix", out)
	}
}

// A branch that exists nowhere is a clear, actionable error.
func TestCheckoutWorktreeMissingBranch(t *testing.T) {
	repo := setupRepo(t)
	err := CheckoutWorktree(repo, filepath.Join(t.TempDir(), "T-3"), "no-such-branch")
	if err == nil || !strings.Contains(err.Error(), "no-such-branch") {
		t.Fatalf("want a not-found error naming the branch, got %v", err)
	}
}

// The stale-worktree retry also fires for the plain-checkout message shape
// ("is already checked out at"), not just the -B one.
func TestParseStaleWorktreePathCheckedOut(t *testing.T) {
	msg := "fatal: 'pr-fix' is already checked out at '/Users/me/.pie/worktrees/OLD'"
	if got := parseStaleWorktreePath(msg); got != "/Users/me/.pie/worktrees/OLD" {
		t.Errorf("parse checked-out shape = %q", got)
	}
}

// PLEX-57766: starting from a branch that is checked out in the USER'S own
// repo. Recovery must not touch their checkout - the old path ran
// `worktree remove --force` on it, and only git's own refusal to remove a
// main working tree stood between that and data loss. The error must name
// the holder and the way out instead.
func TestCheckoutWorktreeRefusesToTouchTheUsersCheckout(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir()) // pie worktrees live elsewhere
	repo := setupRepo(t)
	if _, err := run(repo, "checkout", "-b", "feature/held"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "PIE-1")
	err := CheckoutWorktree(repo, dest, "feature/held")
	if err == nil {
		t.Fatal("checking out a branch held by the user's repo must fail")
	}
	for _, want := range []string{repo, "switch"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q - the user needs the holder and the way out", err, want)
		}
	}
	if _, statErr := os.Stat(repo); statErr != nil {
		t.Fatalf("the user's checkout is gone: %v", statErr)
	}
	if out, _ := run(repo, "rev-parse", "--abbrev-ref", "HEAD"); !strings.Contains(out, "feature/held") {
		t.Fatalf("the user's checkout was switched off its branch: %s", out)
	}
}

// The nastier variant: the branch's holder is a user-owned LINKED worktree.
// Git only refuses `worktree remove --force` for a MAIN working tree, so the
// old recovery would have deregistered this one and deleted its directory -
// silently. Ownership, not main-vs-linked, is the safety rule: anything
// outside ~/.pie/worktrees gets the refusal.
func TestCheckoutWorktreeRefusesToTouchUsersLinkedWorktree(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	repo := setupRepo(t)
	users := filepath.Join(t.TempDir(), "my-feature-checkout")
	if _, err := run(repo, "worktree", "add", "-b", "feature/held2", users); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(users, "uncommitted-work.txt")
	if err := os.WriteFile(marker, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(t.TempDir(), "PIE-2")
	err := CheckoutWorktree(repo, dest, "feature/held2")
	if err == nil {
		t.Fatal("a branch held by the user's linked worktree must refuse")
	}
	if !strings.Contains(err.Error(), users) {
		t.Errorf("error should name the holder %q: %v", users, err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("the user's linked worktree (and their uncommitted work) is gone: %v", statErr)
	}
}

// The authorize-in-tool primitives: find who holds a branch, and free it by
// detaching - same commit, same files, uncommitted work untouched.
func TestBranchHolderAndDetach(t *testing.T) {
	repo := setupRepo(t)
	users := filepath.Join(t.TempDir(), "held")
	if _, err := run(repo, "worktree", "add", "-b", "feature/h3", users); err != nil {
		t.Fatal(err)
	}
	// git reports the symlink-resolved path (/private/var vs /var on macOS).
	if r, err := filepath.EvalSymlinks(users); err == nil {
		users = r
	}
	if err := os.WriteFile(filepath.Join(users, "wip.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := BranchHolder(repo, "feature/h3"); got != users {
		t.Fatalf("BranchHolder = %q, want %q", got, users)
	}
	if got := BranchHolder(repo, "feature/free"); got != "" {
		t.Fatalf("BranchHolder(free) = %q, want empty", got)
	}
	if err := DetachHolder(users); err != nil {
		t.Fatal(err)
	}
	if got := BranchHolder(repo, "feature/h3"); got != "" {
		t.Fatalf("branch still held after detach: %q", got)
	}
	if _, err := os.Stat(filepath.Join(users, "wip.txt")); err != nil {
		t.Fatal("detach lost uncommitted work:", err)
	}
}

// ReclaimResolved is the CLOSED-transition cleanup: worktree gone, local
// branch gone. A resolved PR means origin has the branch, so deleting the
// local copy loses nothing - but uncommitted work in the worktree vetoes the
// whole reclaim (branch deletion would strand a checked-out branch anyway).
func TestReclaimResolved(t *testing.T) {
	repo := setupRepo(t)
	wt := filepath.Join(t.TempDir(), "KAN-9")
	const branch = "ai/kan-9-done"
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}

	removed, err := ReclaimResolved(repo, wt, branch)
	if err != nil || !removed {
		t.Fatalf("ReclaimResolved = (%v, %v), want a clean reclaim", removed, err)
	}
	if fileExists(filepath.Join(wt, ".git")) {
		t.Error("worktree still on disk")
	}
	if RefExists(repo, branch) {
		t.Error("local branch survived the reclaim")
	}

	// Idempotent: a second reclaim of the same (now gone) pair is a no-op win.
	if removed, err = ReclaimResolved(repo, wt, branch); err != nil || !removed {
		t.Fatalf("second reclaim = (%v, %v), want (true, nil)", removed, err)
	}
}

func TestReclaimResolvedSparesDirtyWorktree(t *testing.T) {
	repo := setupRepo(t)
	wt := filepath.Join(t.TempDir(), "KAN-10")
	const branch = "ai/kan-10-wip"
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "wip.txt"), []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := ReclaimResolved(repo, wt, branch)
	if err != nil || removed {
		t.Fatalf("ReclaimResolved on dirty = (%v, %v), want (false, nil)", removed, err)
	}
	if !fileExists(filepath.Join(wt, "wip.txt")) {
		t.Fatal("uncommitted work deleted - the dirty-guard failed")
	}
	if !RefExists(repo, branch) {
		t.Error("branch deleted while its worktree was spared")
	}
}

// No worktree recorded (or already removed by an older pie) but the branch
// lingers: the branch alone is still reclaimed.
func TestReclaimResolvedBranchOnly(t *testing.T) {
	repo := setupRepo(t)
	wt := filepath.Join(t.TempDir(), "KAN-11")
	const branch = "ai/kan-11-old"
	if err := CreateWorktree(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveWorktree(repo, wt); err != nil {
		t.Fatal(err)
	}

	removed, err := ReclaimResolved(repo, wt, branch)
	if err != nil || !removed {
		t.Fatalf("ReclaimResolved = (%v, %v), want (true, nil)", removed, err)
	}
	if RefExists(repo, branch) {
		t.Error("lingering branch survived")
	}
}

// Fetch is what CreateWorktree/CheckoutWorktree already do inline before
// listing/using branches - exported standalone so a caller (the TUI's branch
// pickers) can refresh what Branches sees BEFORE a branch is picked, not
// just after. Branches itself makes no network call (its own doc comment),
// so a branch pushed to origin from elsewhere is invisible to it until
// something fetches.
func TestFetch(t *testing.T) {
	repo := setupRepo(t)
	origin := filepath.Join(filepath.Dir(repo), "origin.git")

	// A second clone pushes a new branch straight to origin - repo has never
	// fetched since, so Branches(repo) must not see it yet.
	other := filepath.Join(t.TempDir(), "other")
	must := func(dir string, args ...string) {
		c := exec.Command("git", args...)
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	must(t.TempDir(), "clone", "-q", origin, other)
	must(other, "checkout", "-q", "-b", "feature/late")
	must(other, "push", "-q", "origin", "feature/late")

	before, err := Branches(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range before {
		if b.Name == "feature/late" {
			t.Fatal("test setup bug: repo already knows feature/late before any fetch")
		}
	}

	if err := Fetch(repo); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	after, err := Branches(repo)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range after {
		if b.Name == "feature/late" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Fetch must make feature/late visible to Branches, got %+v", after)
	}
}

// A missing repo path must yield a clear error, the same as CreateWorktree's
// own guard - not git's opaque "chdir ...".
func TestFetchMissingRepo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	err := Fetch(missing)
	if err == nil {
		t.Fatal("expected error for missing repo path, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "chdir") {
		t.Fatalf("error should name the missing repo path, not leak git's chdir error: %v", err)
	}
}
