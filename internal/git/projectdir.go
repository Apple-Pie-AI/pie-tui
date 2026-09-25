// Locating the configured project inside a worktree. Moved here from the TUI
// (where only "Open in Android Studio" used it) because the runner needs the
// same mapping: local.properties and seeded files must land in the PROJECT
// directory, not the worktree root, or Gradle never sees them.
package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ProjectDirIn maps the configured repo path to its location inside a worktree.
// When the repo path is a subdirectory of its git repository (monorepo: git
// root at /repo, Android project at /repo/App), the worktree mirrors the whole
// repository, so the project sits at the same relative offset - Gradle files,
// Android Studio and the agent's build all belong at <worktree>/App, not the
// worktree root. Falls back to the worktree root when the repo path IS the git
// root or anything fails to resolve.
func ProjectDirIn(worktreeDir, repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return worktreeDir
	}
	// git resolves symlinks in --show-toplevel; resolve the configured path the
	// same way or Rel would mismatch (e.g. /var vs /private/var on macOS).
	top := strings.TrimSpace(string(out))
	repoPath, err := filepath.EvalSymlinks(filepath.Clean(repo))
	if err != nil {
		return worktreeDir
	}
	rel, err := filepath.Rel(top, repoPath)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return worktreeDir
	}
	if fi, err := os.Stat(filepath.Join(worktreeDir, rel)); err == nil && fi.IsDir() {
		return filepath.Join(worktreeDir, rel)
	}
	return worktreeDir
}
