// Content fingerprinting and worktree snapshots for the change-review gate
// (review-before-PR): the fingerprint tells the approve run whether anything
// moved since the park (hand-edits, reworks, reverts), and the snapshot tree
// is the "changes since your feedback" baseline. Both are read-only with
// respect to the worktree and its real index.
package git

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ChangeFingerprint is a stable fingerprint of the worktree's content: the
// HEAD sha plus a hash over `git diff HEAD` and the sorted untracked file
// list with per-file content hashes. Two calls agree iff nothing - tracked
// edits, commits, or untracked files - changed in between. Best-effort: an
// unreadable worktree yields "" (callers treat that as "changed").
func ChangeFingerprint(worktreeDir string) string {
	head := HeadSHA(worktreeDir)
	if head == "" {
		return ""
	}
	diff, err := run(worktreeDir, "diff", "HEAD")
	if err != nil {
		return ""
	}
	untracked, err := run(worktreeDir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return ""
	}
	files := strings.Fields(untracked)
	sort.Strings(files)
	h := sha256.New()
	h.Write([]byte(diff))
	for _, f := range files {
		h.Write([]byte(f))
		b, _ := os.ReadFile(filepath.Join(worktreeDir, f))
		h.Write(b)
	}
	return head + ":" + fmt.Sprintf("%x", h.Sum(nil))
}

// RevertFile restores one file to its HEAD content: tracked files are checked
// out, a file HEAD never had (an untracked addition) is deleted. For the
// change-review gate nothing is committed until approve, so HEAD is the
// change's base.
func RevertFile(worktreeDir, path string) error {
	if _, err := run(worktreeDir, "checkout", "HEAD", "--", path); err == nil {
		return nil
	}
	return os.Remove(filepath.Join(worktreeDir, path))
}

// SnapshotTree writes the worktree's current content (untracked included) as
// a git tree object through a TEMPORARY index - the real index and the
// worktree are untouched - and returns the tree's sha. The object lives in
// the repository's object store, unreferenced, so `git diff <tree>` works
// against it until a gc; that is fine for a review baseline that only needs
// to outlive the park.
func SnapshotTree(worktreeDir string) (string, error) {
	tmp, err := os.CreateTemp("", "pie-snap-index-*")
	if err != nil {
		return "", err
	}
	tmp.Close()
	os.Remove(tmp.Name()) // git wants to create the index file itself
	defer os.Remove(tmp.Name())

	env := append(os.Environ(), "GIT_INDEX_FILE="+tmp.Name())
	add := exec.Command("git", "-C", worktreeDir, "add", "-A")
	add.Env = env
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("snapshot add: %w: %s", err, out)
	}
	write := exec.Command("git", "-C", worktreeDir, "write-tree")
	write.Env = env
	out, err := write.Output()
	if err != nil {
		return "", fmt.Errorf("snapshot write-tree: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
