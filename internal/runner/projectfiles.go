// Making a worktree buildable before any agent touches it. `git worktree add`
// materializes only tracked files, so the machine-local config a build needs
// (local.properties with the SDK path, google-services.json, .env, keystores)
// is seeded from the main checkout here - and the SDK path is synthesized as a
// fallback when the main checkout has nothing to seed.
package runner

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// ensureProjectFiles seeds the worktree with the main checkout's git-ignored
// files and guarantees a local.properties in the PROJECT directory - which in
// a monorepo is a subdirectory of the worktree, where Gradle actually looks
// for it; the old root-level write was invisible to Gradle there. Idempotent
// (seeding never overwrites), so every worktree entrypoint calls it: fresh
// runs, --from-plan reuse, --resume and --ship - the latter two also heal
// worktrees created before seeding existed. Returns the project dir.
func ensureProjectFiles(t Task, worktree string, logf logFn) string {
	projectDir := git.ProjectDirIn(worktree, t.Repo.Path)

	if st, err := git.SeedIgnoredFiles(t.Repo.Path, worktree); err != nil {
		logf("[%s] (warn) seeding ignored files: %v", t.Ticket, err)
	} else if st.Copied > 0 || st.SkippedLarge > 0 || st.SkippedSymlink > 0 || st.TotalCapHit {
		logf("[%s] seeded %d git-ignored file(s) (%d KB) from the main checkout (skipped: %d oversized, %d symlink)",
			t.Ticket, st.Copied, st.CopiedBytes/1024, st.SkippedLarge, st.SkippedSymlink)
		if st.TotalCapHit {
			logf("[%s] (warn) seeding stopped at the %d MB total cap - remaining ignored files were not copied",
				t.Ticket, seedTotalCapMB)
		}
	}

	// A seeded (or human-placed) local.properties wins: it carries this
	// machine's real SDK path. Synthesize one only when nothing exists.
	if _, err := os.Stat(filepath.Join(projectDir, "local.properties")); err == nil {
		return projectDir
	}
	if t.Cfg.AndroidSDKPath == "" {
		// Loud on purpose: this used to be a silent no-op, and the failure
		// surfaced much later as an inexplicable Gradle "SDK location not found".
		logf("[%s] (warn) cannot write local.properties: android_sdk_path is unset and $ANDROID_HOME/$ANDROID_SDK_ROOT are empty - Gradle will not find the SDK; set android_sdk_path in %s",
			t.Ticket, paths.Config())
		return projectDir
	}
	if err := paths.WriteLocalProperties(projectDir, t.Cfg.AndroidSDKPath); err != nil {
		logf("[%s] (warn) local.properties: %v", t.Ticket, err)
	}
	return projectDir
}

// seedTotalCapMB mirrors git.SeedIgnoredFiles' total-bytes cap for the log line.
const seedTotalCapMB = 64

// relProjectDir is projectDir relative to the worktree root, "" when the build
// lives at the root - the form the prompts speak ("cd <dir> && ./gradlew ...").
func relProjectDir(worktree, projectDir string) string {
	rel, err := filepath.Rel(worktree, projectDir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	return rel
}
