package git

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// seedRepo builds a monorepo-shaped checkout: tracked skeleton committed,
// .gitignore covering the machine-local files, and the ignored files present
// only in the main checkout. Returns (repo, worktree) with the worktree
// populated the way CreateWorktree leaves it: tracked files only.
func seedRepo(t *testing.T) (string, string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) {
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
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"),
		[]byte("local.properties\n.env\n*.keystore\nbig.bin\nlink.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "App", "settings.gradle"),
		[]byte("rootProject.name = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-q", "-m", "skeleton")

	// The worktree as git materializes it: tracked files only.
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, "App"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "App", "settings.gradle"),
		[]byte("rootProject.name = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo, wt
}

func writeIgnored(t *testing.T, repo, rel, content string) {
	t.Helper()
	p := filepath.Join(repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSeedIgnoredFilesCopiesAtSameRelPaths(t *testing.T) {
	repo, wt := seedRepo(t)
	writeIgnored(t, repo, "App/local.properties", "sdk.dir=/sdk\n")
	writeIgnored(t, repo, ".env", "SECRET=1\n")
	writeIgnored(t, repo, "App/release.keystore", "binary")

	st, err := SeedIgnoredFiles(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 3 {
		t.Errorf("copied %d files, want 3 (%+v)", st.Copied, st)
	}
	for _, rel := range []string{"App/local.properties", ".env", "App/release.keystore"} {
		if _, err := os.Stat(filepath.Join(wt, rel)); err != nil {
			t.Errorf("%s not seeded: %v", rel, err)
		}
	}
	// The seeded files must remain invisible to `git add -A` in the worktree:
	// they are ignored there too, so they can never leak into a PR. Approximate
	// the worktree with check-ignore against the same tracked .gitignore.
	out, _ := exec.Command("git", "-C", repo, "check-ignore", "App/local.properties").Output()
	if len(bytes.TrimSpace(out)) == 0 {
		t.Error("seeded file is not gitignored - it would land in the PR")
	}
}

func TestSeedSkipsLargeFilesAndSymlinks(t *testing.T) {
	repo, wt := seedRepo(t)
	writeIgnored(t, repo, "big.bin", string(make([]byte, seedMaxFileBytes+1)))
	if err := os.Symlink("/etc/hosts", filepath.Join(repo, "link.txt")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	writeIgnored(t, repo, ".env", "ok\n")

	st, err := SeedIgnoredFiles(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 1 || st.SkippedLarge != 1 || st.SkippedSymlink != 1 {
		t.Errorf("stats %+v, want 1 copied / 1 large / 1 symlink", st)
	}
	if _, err := os.Lstat(filepath.Join(wt, "link.txt")); err == nil {
		t.Error("symlink was materialized in the worktree")
	}
	if _, err := os.Stat(filepath.Join(wt, "big.bin")); err == nil {
		t.Error("oversized file was copied")
	}
}

func TestSeedPrunesBuildOutputDirs(t *testing.T) {
	repo, wt := seedRepo(t)
	// Ignored files inside pruned dirs: must never be enumerated or copied.
	writeIgnored(t, repo, "App/build/out.apk", "apk")
	writeIgnored(t, repo, ".gradle/cache.lock", "lock")
	writeIgnored(t, repo, "App/local.properties", "sdk.dir=/sdk\n")

	// The pruned dirs need ignore rules or ls-files would list them anyway.
	f, err := os.OpenFile(filepath.Join(repo, ".gitignore"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("build/\n.gradle/\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	st, err := SeedIgnoredFiles(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 1 {
		t.Errorf("copied %d, want only local.properties (%+v)", st.Copied, st)
	}
	if _, err := os.Stat(filepath.Join(wt, "App", "build", "out.apk")); err == nil {
		t.Error("build output was seeded")
	}
}

func TestSeedNeverOverwritesExisting(t *testing.T) {
	repo, wt := seedRepo(t)
	writeIgnored(t, repo, "App/local.properties", "sdk.dir=/theirs\n")
	// The human already fixed this file in the worktree.
	if err := os.WriteFile(filepath.Join(wt, "App", "local.properties"),
		[]byte("sdk.dir=/mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := SeedIgnoredFiles(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 0 {
		t.Errorf("copied %d, want 0", st.Copied)
	}
	got, _ := os.ReadFile(filepath.Join(wt, "App", "local.properties"))
	if string(got) != "sdk.dir=/mine\n" {
		t.Errorf("worktree copy was overwritten: %q", got)
	}
}

func TestSeedIgnoresUntrackedNonIgnoredFiles(t *testing.T) {
	repo, wt := seedRepo(t)
	// A developer's WIP source file: untracked but NOT gitignored. Seeding it
	// would let `git add -A` commit it into the ticket's PR.
	writeIgnored(t, repo, "App/Wip.kt", "class Wip\n")
	writeIgnored(t, repo, ".env", "ok\n")

	st, err := SeedIgnoredFiles(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 1 {
		t.Errorf("copied %d, want only .env (%+v)", st.Copied, st)
	}
	if _, err := os.Stat(filepath.Join(wt, "App", "Wip.kt")); err == nil {
		t.Error("untracked non-ignored file was seeded - it would land in the PR")
	}
}

func TestSeedNotAGitRepo(t *testing.T) {
	if _, err := SeedIgnoredFiles(t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("expected an error for a non-repo")
	}
}

func TestSeedPreservesExecBit(t *testing.T) {
	repo, wt := seedRepo(t)
	p := filepath.Join(repo, ".env")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := SeedIgnoredFiles(repo, wt); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(wt, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("exec bit lost: %v", fi.Mode())
	}
}

// The user's Claude Code settings are the BASE permission layer: the agent's
// --allowed-tools is additive on top of them and can never restrict them, so
// the worktree must carry the user's .claude/settings.local.json exactly like
// the main checkout does. Stripping it would silently narrow the user's own
// configuration - which pie never does. This is a deliberate product decision
// (a .claude seeding exclusion was once half-built and reversed); any change
// that makes this test fail is reversing that decision, not fixing a leak.
func TestSeedCopiesClaudeLocalSettings(t *testing.T) {
	repo, wt := seedRepo(t)
	f, err := os.OpenFile(filepath.Join(repo, ".gitignore"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(".claude/\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	const settings = `{"permissions":{"allow":["Bash(./gradlew assembleDebug *)"]}}`
	writeIgnored(t, repo, ".claude/settings.local.json", settings)

	st, err := SeedIgnoredFiles(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	if st.Copied != 1 {
		t.Errorf("copied %d file(s), want the settings file counted (%+v)", st.Copied, st)
	}
	got, err := os.ReadFile(filepath.Join(wt, ".claude", "settings.local.json"))
	if err != nil {
		t.Fatalf("Claude settings not seeded: %v", err)
	}
	if string(got) != settings {
		t.Errorf("seeded content differs: %q", got)
	}
}

// Monorepos carry per-project Claude settings (App/.claude/...); seeding must
// preserve them at the same relative depth, same as root-level ones.
func TestSeedCopiesNestedClaudeSettings(t *testing.T) {
	repo, wt := seedRepo(t)
	f, err := os.OpenFile(filepath.Join(repo, ".gitignore"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(".claude/\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	writeIgnored(t, repo, "App/.claude/settings.local.json", `{"permissions":{}}`)

	if _, err := SeedIgnoredFiles(repo, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "App", ".claude", "settings.local.json")); err != nil {
		t.Errorf("nested Claude settings not seeded: %v", err)
	}
}
