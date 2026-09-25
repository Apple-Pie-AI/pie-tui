// Reconciling an existing worktree with origin before we edit it again.
//
// Push uses --force-with-lease, and the lease is the remote-tracking ref: it is
// only current because CreateWorktree fetched on the way in. Any path that
// reuses a worktree without going through CreateWorktree - addressing PR review
// comments on a branch someone else has since pushed to - must refresh that ref
// itself, or the push is rejected *after* the agent has already done the work.
package git

import (
	"fmt"
	"strconv"
	"strings"
)

// FetchBranch updates origin/<branch> in the worktree's repository.
func FetchBranch(worktreeDir, branch string) error {
	_, err := run(worktreeDir, "fetch", "origin", branch)
	return err
}

// Divergence reports how far the worktree's HEAD is behind and ahead of
// origin/<branch>. Call FetchBranch first - this reads the remote-tracking ref,
// not the remote.
func Divergence(worktreeDir, branch string) (behind, ahead int, err error) {
	out, err := run(worktreeDir, "rev-list", "--left-right", "--count",
		"origin/"+branch+"...HEAD")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("git rev-list: unexpected output %q", out)
	}
	// --left-right counts the left side (origin) first: commits origin has that
	// we don't = behind; the right side (HEAD) = ahead.
	if behind, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("git rev-list: behind %q: %w", fields[0], err)
	}
	if ahead, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("git rev-list: ahead %q: %w", fields[1], err)
	}
	return behind, ahead, nil
}

// FastForward advances the worktree to origin/<branch>, refusing anything that
// is not a fast-forward. Deliberately never a rebase or a merge: this runs
// headless, and a conflict would leave the worktree mid-operation with no human
// at the keyboard to finish it.
func FastForward(worktreeDir, branch string) error {
	_, err := run(worktreeDir, "merge", "--ff-only", "origin/"+branch)
	return err
}

// HeadSHA returns the worktree's current commit, or "" if it cannot be read.
func HeadSHA(worktreeDir string) string {
	out, err := run(worktreeDir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return out
}

// ShortSHA abbreviates a commit for display in a comment or log line.
func ShortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
