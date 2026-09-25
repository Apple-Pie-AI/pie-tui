// Seeding fresh worktrees with the git-ignored files a build actually needs.
// `git worktree add` materializes only TRACKED files, so machine-local config
// the project depends on - local.properties (the Android SDK path),
// google-services.json, .env, signing keystores - never arrives, and Gradle
// fails in ways that look like the agent's fault. CreateWorktree also RemoveAlls
// the directory on re-runs, destroying hand-placed copies.
package git

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// seedMaxFileBytes / seedMaxTotalBytes bound what seeding will copy: ignored
// files larger than this are build artifacts or binaries, not config.
const (
	seedMaxFileBytes  = 2 << 20  // 2 MB per file
	seedMaxTotalBytes = 64 << 20 // 64 MB per worktree
)

// seedExcludedDirs never contain machine-local config worth seeding - they are
// build output and IDE state, and on a real Android monorepo they hold tens of
// thousands of ignored files. They are pruned inside git itself (pathspec
// excludes) so enumeration stays fast; Go never sees those paths.
var seedExcludedDirs = []string{
	"build", ".gradle", ".idea", "node_modules", "out", ".kotlin", ".cxx",
	".git", ".agent",
}

// SeedStats summarizes one seeding pass, for the caller's log line.
type SeedStats struct {
	Copied         int
	CopiedBytes    int64
	SkippedLarge   int
	SkippedSymlink int
	TotalCapHit    bool
}

// SeedIgnoredFiles copies git-ignored files from the repo's main checkout into
// the worktree at the same relative paths.
//
// Scope is deliberate on three axes:
//   - IGNORED files only, not plain untracked ones: the tracked .gitignore
//     applies inside the worktree too, so a seeded file can never reach the
//     `git add -A` in the ship stage or the PR. A developer's untracked WIP
//     source file WOULD be committed, so it is excluded by design.
//   - Enumerated from the git ROOT (not the configured sub-path): the worktree
//     mirrors the whole repository, so relative paths map 1:1.
//   - Existing destination files are never overwritten (idempotent), so
//     re-seeding a worktree the human has edited is safe.
//
// Symlinks are skipped (never followed - a link out of the repo must not be
// materialized), as are oversized files. Note the copies can include local
// secrets (keystores, .env); they stay on this machine, remain gitignored in
// the worktree, and are never pushed. Per-file copy failures are skipped, not
// fatal; only the enumeration itself returns an error.
func SeedIgnoredFiles(repo, worktreeDir string) (SeedStats, error) {
	var st SeedStats
	top, err := run(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return st, fmt.Errorf("seed: not a git checkout: %w", err)
	}

	// Push the directory exclusions into git so it prunes build output during
	// enumeration instead of handing us every file under build/ to filter.
	args := []string{"ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--"}
	for _, d := range seedExcludedDirs {
		args = append(args, ":!**/"+d+"/**", ":!"+d+"/**")
	}
	out, err := runRaw(top, args...)
	if err != nil {
		return st, fmt.Errorf("seed: list ignored files: %w", err)
	}

	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" || filepath.Base(rel) == ".DS_Store" {
			continue
		}
		src := filepath.Join(top, rel)
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() {
			if err == nil && fi.Mode()&os.ModeSymlink != 0 {
				st.SkippedSymlink++
			}
			continue
		}
		if fi.Size() > seedMaxFileBytes {
			st.SkippedLarge++
			continue
		}
		if st.CopiedBytes+fi.Size() > seedMaxTotalBytes {
			st.TotalCapHit = true
			break
		}
		dst := filepath.Join(worktreeDir, rel)
		if _, err := os.Lstat(dst); err == nil {
			continue // never overwrite - the worktree's copy wins
		}
		if err := copyFile(src, dst, fi.Mode().Perm()); err != nil {
			continue // best-effort per file
		}
		st.Copied++
		st.CopiedBytes += fi.Size()
	}
	return st, nil
}

// copyFile copies src to dst (creating parent dirs), preserving the source's
// permission bits so seeded helper scripts stay executable.
func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// runRaw is `run` minus the TrimSpace and minus stderr folding: NUL-separated
// output is data byte for byte, and parsing must never see a stray git notice
// (the lesson from vcs.PRState, which parsed CombinedOutput and silently
// matched nothing).
func runRaw(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
