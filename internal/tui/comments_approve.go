// The approve phase of the comments screen: the agent has fixed the queued
// comments LOCALLY (session parked at fix-review) and nothing is committed,
// pushed, or posted yet. This screen shows the human exactly what would ship -
// the worktree diff and the draft reply per comment - and offers three verbs:
// edit a reply, send the fixes back with feedback, or approve the lot.
//
// The git diff is read by a tea.Cmd, never on the render path, and cached
// until a new run is spawned - the worktree cannot change under an idle
// fix-review session.
package tui

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// approveDiffMsg carries the worktree diff, split per file.
type approveDiffMsg struct {
	ticket string
	files  map[string][]string
	err    error
}

// approveDiffCmd reads what approving would ship, off the render path.
//
// The base is origin's tip, not HEAD: approve pushes the branch, and a push
// sends every local commit - so if the agent committed mid-fix (it is told not
// to, but the preview must not depend on an agent's obedience), a HEAD-based
// diff would swear the worktree was clean while those commits shipped anyway.
// Against upstream, extra commits on the branch show like any other change;
// HEAD is only the fallback for a worktree with no upstream to compare to.
//
// Plain `git diff` was worse still: it shows only unstaged edits to tracked
// files. Staged edits need a base, and a brand-new file - what a "add tests
// for X" fix usually is - is untracked and appears in no diff at all.
func approveDiffCmd(worktree, ticket string) tea.Cmd {
	return func() tea.Msg {
		base := "HEAD"
		if out, err := exec.Command("git", "-C", worktree,
			"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}").Output(); err == nil {
			if ref := strings.TrimSpace(string(out)); ref != "" {
				base = ref
			}
		}
		out, err := exec.Command("git", "-C", worktree, "diff", base).CombinedOutput()
		if err != nil {
			return approveDiffMsg{ticket: ticket, err: fmt.Errorf("git diff %s: %v: %s", base, err, strings.TrimSpace(string(out)))}
		}
		files := splitDiffByFile(string(out))
		for path, lines := range untrackedAsHunks(worktree) {
			files[path] = lines
		}
		return approveDiffMsg{ticket: ticket, files: files}
	}
}

// untrackedAsHunks renders each untracked file as the all-added hunk it will
// become when the ship path commits it. git's own excludes apply, so `.agent/`
// scratch never shows up as a "change".
func untrackedAsHunks(worktree string) map[string][]string {
	out, err := exec.Command("git", "-C", worktree, "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil
	}
	// A new file bigger than this is not something to read in a TUI pane.
	const maxNewFile = 512 * 1024
	files := map[string][]string{}
	for _, path := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if path == "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(worktree, path))
		switch {
		case err != nil:
			continue
		case len(data) > maxNewFile || bytes.IndexByte(data, 0) >= 0:
			files[path] = []string{"@@ -0,0 +1 @@", "+(new file - binary or too large to preview)"}
			continue
		}
		lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
		hunk := make([]string, 0, len(lines)+1)
		hunk = append(hunk, fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)))
		for _, ln := range lines {
			hunk = append(hunk, "+"+ln)
		}
		files[path] = hunk
	}
	return files
}

// approveDiffIfNeeded issues the diff read once per approve visit: the phase
// is idle (the driver exited), so the worktree cannot change until this screen
// spawns the next run - which resets the cache.
func (m *monitorModel) approveDiffIfNeeded() tea.Cmd {
	if m.phase() != phaseApprove || m.comments.diffLoaded || m.comments.diffFetching {
		return nil
	}
	if m.comments.worktree == "" {
		return nil
	}
	m.comments.diffFetching = true
	return approveDiffCmd(m.comments.worktree, m.comments.ticket)
}

// splitDiffByFile cuts a unified diff into per-file hunk blocks, keyed by the
// new-side path - or the old-side path for a deletion, whose new side is
// /dev/null and which a "+++ b/" match alone silently dropped: a fix that
// removes a file is still a change the human is approving. File metadata
// (diff --git, index, ---, +++) is dropped: the hunk renderer wants @@ headers
// and +/-/context lines only.
func splitDiffByFile(diff string) map[string][]string {
	files := map[string][]string{}
	path := ""
	inHunks := false
	for _, ln := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(ln, "diff --git "):
			path, inHunks = "", false
		case strings.HasPrefix(ln, "--- a/"):
			path = strings.TrimPrefix(ln, "--- a/")
		case strings.HasPrefix(ln, "+++ b/"):
			path = strings.TrimPrefix(ln, "+++ b/")
		case strings.HasPrefix(ln, "@@"):
			inHunks = true
			if path != "" {
				files[path] = append(files[path], ln)
			}
		default:
			if inHunks && path != "" {
				files[path] = append(files[path], ln)
			}
		}
	}
	return files
}

// invalidateApproveDiff drops the cached worktree diff; the next approve visit
// re-reads it. Called when a run is spawned - the worktree is about to change.
func (m *monitorModel) invalidateApproveDiff() {
	m.comments.diff, m.comments.diffErr = nil, ""
	m.comments.diffLoaded, m.comments.diffFetching = false, false
}
