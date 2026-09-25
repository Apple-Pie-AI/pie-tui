// Package git manages per-ticket worktrees + branches (Decision 7).
package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// repoLocks serializes CreateWorktree calls per repo path. git worktree add
// and git fetch both write to .git/config and can't run concurrently on the
// same repo without hitting "could not lock config file .git/config".
var repoLocks sync.Map // map[string]*sync.Mutex

func repoLock(repo string) *sync.Mutex {
	v, _ := repoLocks.LoadOrStore(repo, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// DefaultBranch returns origin's default branch (best effort).
func DefaultBranch(repo string) string {
	if out, err := run(repo, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(out, "origin/")
	}
	for _, b := range []string{"main", "master"} {
		if _, err := run(repo, "rev-parse", "--verify", "origin/"+b); err == nil {
			return b
		}
	}
	return "main"
}

// Branch is one repository branch (local or remote).
type Branch struct {
	Name   string // bare name without "origin/" prefix
	Remote bool   // true if only on origin, false if local (or both)
}

// Branches lists local and remote branches sorted by most-recently-committed,
// deduplicating so a branch that is both local and remote appears once (preferring
// the local entry so in-progress parent branches are reachable). No network call.
func Branches(repo string) ([]Branch, error) {
	out, err := run(repo, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname:short)", "refs/heads", "refs/remotes/origin")
	if err != nil {
		return nil, err
	}
	type entry struct {
		name   string
		remote bool
		order  int
	}
	byName := map[string]*entry{}
	order := 0
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "origin/HEAD" {
			continue
		}
		remote := strings.HasPrefix(line, "origin/")
		name := strings.TrimPrefix(line, "origin/")
		if e, ok := byName[name]; ok {
			if !remote && e.remote {
				e.remote = false // upgrade to local
			}
		} else {
			byName[name] = &entry{name: name, remote: remote, order: order}
			order++
		}
	}
	entries := make([]*entry, 0, len(byName))
	for _, e := range byName {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })
	branches := make([]Branch, len(entries))
	for i, e := range entries {
		branches[i] = Branch{Name: e.name, Remote: e.remote}
	}
	return branches, nil
}

// RefExists reports whether ref resolves to a valid object in repo.
func RefExists(repo, ref string) bool {
	_, err := run(repo, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// ValidateRepo checks that repo is a usable git checkout - it exists, is a
// directory, holds a git repository, and has an "origin" remote - and returns a
// clear, actionable error naming the FIRST problem it finds. Every caller (the
// run preflight, doctor, CreateWorktree) surfaces this message verbatim, so it
// must stand on its own. One validator, so those call sites can't drift into
// three subtly different diagnoses of the same broken config - the exact failure
// this used to have, where a missing path read as "no origin remote" in doctor
// and an opaque "chdir: no such file or directory" at worktree creation.
func ValidateRepo(repo string) error {
	info, err := os.Stat(repo)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("repository path %q does not exist - set it to your project's git checkout in ~/.pie/config.toml (or Settings)", repo)
		}
		return fmt.Errorf("repository path %q: %w", repo, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repository path %q is not a directory - set it to your project's git checkout in ~/.pie/config.toml (or Settings)", repo)
	}
	if _, err := run(repo, "rev-parse", "--git-dir"); err != nil {
		return fmt.Errorf("repository path %q is not a git repository - clone your project there, or fix the path in ~/.pie/config.toml (or Settings)", repo)
	}
	if _, err := run(repo, "remote", "get-url", "origin"); err != nil {
		return fmt.Errorf("repository %q has no \"origin\" remote - add one with: git -C %q remote add origin <url>", repo, repo)
	}
	return nil
}

// RepoSuggestion is a local git checkout offered as a one-step fix for an
// invalid configured repo path. HasOrigin flags the Pie-ready ones (a worktree
// run fetches/pushes origin), so the picker can surface those first.
type RepoSuggestion struct {
	Path      string
	HasOrigin bool
}

// SuggestRepos returns local git checkouts the user likely meant for an invalid
// configured path: git-repo children of the path's parent directory (covers a
// wrong name or the setup default sitting next to the real checkout), plus the
// git root of the current working directory (covers launching Pie from inside
// the repo). Origin-having repos sort first; the list is capped for display and
// deduplicated. Best-effort and read-only - returns nil, never an error, when it
// finds nothing.
func SuggestRepos(configuredPath string) []RepoSuggestion {
	const max = 5
	seen := map[string]bool{}
	var out []RepoSuggestion
	add := func(dir string) {
		if dir == "" || seen[dir] || !isGitRepo(dir) {
			return
		}
		seen[dir] = true
		_, err := run(dir, "remote", "get-url", "origin")
		out = append(out, RepoSuggestion{Path: dir, HasOrigin: err == nil})
	}
	if entries, err := os.ReadDir(filepath.Dir(configuredPath)); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				add(filepath.Join(filepath.Dir(configuredPath), e.Name()))
			}
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if top, err := run(wd, "rev-parse", "--show-toplevel"); err == nil {
			add(top)
		}
	}
	// Pie-ready (origin) first, then alphabetical, so the strongest candidate is
	// the default selection. Stable within each group keeps the order predictable.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].HasOrigin != out[j].HasOrigin {
			return out[i].HasOrigin
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// isGitRepo reports whether dir has a .git entry (a dir for a normal checkout, a
// file for a worktree or submodule - either counts). A stat is cheaper than
// spawning git for every candidate directory in a scan.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// IsRepo reports whether dir is a git repository. Exported so callers deciding
// whether "create a new repo here" makes sense (a plain folder) versus not (one
// that's already a repo) don't reinvent the check.
func IsRepo(dir string) bool { return isGitRepo(dir) }

// Init creates an empty git repository at dir (like `git init`), making dir and
// any missing parents first. It leaves the working tree untouched - nothing is
// staged or committed - so turning an existing folder into a repo is safe. Note
// that a Pie run still needs an "origin" remote, which this does not add.
func Init(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if _, err := run("", "init", dir); err != nil {
		return err
	}
	return nil
}

// Clone runs `git clone <url> <dir>`, creating any missing parent directories
// first (git clone creates the leaf, not the leading path). dir must not already
// exist as a non-empty directory - git's own rule - so the caller offers this
// only for a path that failed validation by being absent.
func Clone(url, dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	if _, err := run("", "clone", url, dir); err != nil {
		return err
	}
	return nil
}

// Fetch runs `git fetch origin --prune` against repo, serialized per repo the
// same way CreateWorktree/CheckoutWorktree are - concurrent fetches would
// race the same writes to .git/config those already guard against. Exported
// standalone (not shared with them - a plain sync.Mutex isn't reentrant, and
// they already hold this same lock across their own fetch) so a caller can
// refresh the branch list BEFORE a branch is picked, not just after
// (Branches itself makes no network call - see its own doc comment).
func Fetch(repo string) error {
	mu := repoLock(repo)
	mu.Lock()
	defer mu.Unlock()
	if err := ValidateRepo(repo); err != nil {
		return err
	}
	_, err := run(repo, "fetch", "origin", "--prune")
	return err
}

// CreateWorktree fetches origin and adds a fresh worktree on a new branch off
// baseRef (used verbatim as the git starting point). A stale worktree at the
// same path is removed first. When baseRef is empty it defaults to
// origin/<default branch>. The call is serialized per repo to avoid concurrent
// writes to .git/config.
func CreateWorktree(repo, worktreeDir, branch, baseRef string) error {
	mu := repoLock(repo)
	mu.Lock()
	defer mu.Unlock()

	// Validate up front so a broken repo path (the most common misconfiguration)
	// surfaces as a clear message instead of git's opaque "chdir <path>: no such
	// file or directory" buried under "fetch origin --prune".
	if err := ValidateRepo(repo); err != nil {
		return err
	}

	if _, err := run(repo, "fetch", "origin", "--prune"); err != nil {
		return err
	}
	if baseRef == "" {
		baseRef = "origin/" + DefaultBranch(repo)
	}
	// -B creates the branch, or RESETS it to baseRef if it already exists,
	// so a re-run starts clean whether or not the branch lingers from a prior run.
	return addWorktree(repo, worktreeDir, []string{"-B", branch, worktreeDir, baseRef})
}

// CheckoutWorktree fetches origin and adds a worktree ON an existing branch -
// the start-from-branch mode (addressing PR feedback, reviewing a PR's code).
// Unlike CreateWorktree it never resets: the branch's existing commits are the
// point. The local branch wins when it exists; a remote-only branch gets a
// local tracking branch. Serialized per repo like CreateWorktree.
func CheckoutWorktree(repo, worktreeDir, branch string) error {
	mu := repoLock(repo)
	mu.Lock()
	defer mu.Unlock()

	if err := ValidateRepo(repo); err != nil {
		return err
	}
	if _, err := run(repo, "fetch", "origin", "--prune"); err != nil {
		return err
	}
	switch {
	case RefExists(repo, "refs/heads/"+branch):
		return addWorktree(repo, worktreeDir, []string{worktreeDir, branch})
	case RefExists(repo, "refs/remotes/origin/"+branch):
		return addWorktree(repo, worktreeDir, []string{"--track", "-b", branch, worktreeDir, "origin/" + branch})
	default:
		return fmt.Errorf("branch %q not found locally or on origin - start-from-branch needs a branch that already exists", branch)
	}
}

// addWorktree clears anything stale at worktreeDir, runs `git worktree add
// <addArgs...>`, and retries once after tearing down a stale worktree that
// still holds the branch. Callers hold the repo lock and have fetched.
func addWorktree(repo, worktreeDir string, addArgs []string) error {
	if err := os.MkdirAll(filepath.Dir(worktreeDir), 0o755); err != nil {
		return err
	}
	_, _ = run(repo, "worktree", "remove", "--force", worktreeDir) // de-register if registered
	_, _ = run(repo, "worktree", "prune")                          // drop stale admin entries
	// Nuke any leftover directory still occupying the path (e.g. a stale .idea/
	// left by opening the worktree in an IDE). git refuses to add a worktree onto
	// a non-empty dir, and `worktree remove` can't clear an unregistered one.
	if err := os.RemoveAll(worktreeDir); err != nil {
		return fmt.Errorf("clear stale worktree dir: %w", err)
	}
	args := append([]string{"worktree", "add"}, addArgs...)
	if _, err := run(repo, args...); err != nil {
		// The branch may be checked out in a stale worktree at a different path
		// (e.g. the repo was moved, or the worktree naming changed between runs).
		// Parse that path from the error and remove it, then retry once.
		if stale := parseStaleWorktreePath(err.Error()); stale != "" {
			// Recovery is only for worktrees Apple Pie itself owns. The holder
			// can just as well be the USER'S checkout - starting from a branch
			// that is checked out in their main repo lands exactly here - and
			// `worktree remove --force` on a user-owned linked worktree would
			// delete their directory. Git happens to refuse for a main working
			// tree, but "git refused" is not a safety model.
			if !paths.UnderWorktrees(stale) {
				return fmt.Errorf("this branch is checked out at %s - git allows a branch in only one worktree at a time. Either work on it there directly, or switch that checkout to another branch first (git -C %q switch -) and run this ticket again", stale, stale)
			}
			_, _ = run(repo, "worktree", "remove", "--force", stale)
			_ = os.RemoveAll(stale)
			_, _ = run(repo, "worktree", "prune")
			if _, retryErr := run(repo, args...); retryErr != nil {
				return retryErr
			}
		} else {
			return err
		}
	}
	excludeAgentDir(worktreeDir)
	return nil
}

// excludeAgentDir adds Apple Pie's scratch dir (.agent/{plan,report,review}.json)
// to the worktree's git exclude so `git add -A` never stages it - Apple Pie's
// artifacts must not leak into the target repo's commits/PRs. Best-effort.
func excludeAgentDir(worktreeDir string) {
	rel, err := run(worktreeDir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return
	}
	excl := rel
	if !filepath.IsAbs(excl) {
		excl = filepath.Join(worktreeDir, excl)
	}
	if data, err := os.ReadFile(excl); err == nil && strings.Contains(string(data), ".agent/") {
		return // already excluded
	}
	if err := os.MkdirAll(filepath.Dir(excl), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(excl, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n# Apple Pie scratch (plan/report/review) - never commit\n.agent/\n")
}

// UntrackAgentDir removes a previously-committed .agent/ scratch dir from the
// index (no-op via --ignore-unmatch if it was never tracked) so it stops
// appearing in PRs. Combined with the worktree exclude, this keeps Apple Pie's
// artifacts out of the target repo.
func UntrackAgentDir(worktreeDir string) {
	_, _ = run(worktreeDir, "rm", "-r", "--cached", "--ignore-unmatch", ".agent")
}

// RemoveWorktree tears down a worktree (called on PR close in Phase 2).
func RemoveWorktree(repo, worktreeDir string) error {
	_, err := run(repo, "worktree", "remove", "--force", worktreeDir)
	return err
}

// DeleteBranch force-deletes a local branch. -D rather than -d: the callers
// reach here only after the branch's PR resolved on GitHub, so origin has
// every commit, and a stale local main would make -d's merged-check refuse a
// branch that is in fact merged.
func DeleteBranch(repo, branch string) error {
	_, err := run(repo, "branch", "-D", branch)
	return err
}

// ReclaimResolved is the full cleanup for a ticket whose PR merged or closed:
// the worktree (unless it holds uncommitted work) and then the local branch.
// Deleting the branch loses nothing - a PR existed, so origin has it - but a
// dirty worktree vetoes the whole reclaim: the human's edits survive, and git
// would refuse to delete a checked-out branch anyway. Returns whether the
// reclaim happened (false with a nil error = spared for dirtiness).
// Idempotent: a worktree already gone from disk skips straight to the branch,
// and a branch already gone is not an error.
func ReclaimResolved(repo, worktreeDir, branch string) (bool, error) {
	if worktreeDir != "" {
		if _, err := os.Stat(worktreeDir); err == nil {
			if HasChanges(worktreeDir) {
				return false, nil
			}
			if err := RemoveWorktree(repo, worktreeDir); err != nil {
				return false, err
			}
		}
	}
	if branch != "" && RefExists(repo, branch) {
		if err := DeleteBranch(repo, branch); err != nil {
			return false, err
		}
	}
	return true, nil
}

// parseStaleWorktreePath extracts the path from a git error of the form:
//
//	fatal: 'branch' is already used by worktree at '/some/path'
//	fatal: 'branch' is already checked out at '/some/path'
//
// (the first comes from `worktree add -B`, the second from a plain
// `worktree add <dir> <branch>` - the CheckoutWorktree path).
// The path is the last quoted run on its line, so we scan to the LAST quote
// rather than the first: repo paths legitimately contain apostrophes
// ("/Users/me/Mike's Repos/KAN-1") and stopping at the first one would yield a
// truncated ancestor directory - which the caller then deletes recursively.
func parseStaleWorktreePath(msg string) string {
	var rest string
	for _, marker := range []string{"is already used by worktree at '", "is already checked out at '"} {
		if i := strings.Index(msg, marker); i != -1 {
			rest = msg[i+len(marker):]
			break
		}
	}
	if rest == "" {
		return ""
	}
	// Confine the scan to the line carrying the marker; later lines may quote
	// unrelated text.
	if nl := strings.IndexByte(rest, '\n'); nl != -1 {
		rest = rest[:nl]
	}
	j := strings.LastIndexByte(rest, '\'')
	if j <= 0 {
		return "" // unterminated - refuse to guess at a path we may delete
	}
	return rest[:j]
}

// HasChanges reports whether the worktree has uncommitted changes. Apple
// Pie's own .agent/ scratch dir never counts: the per-worktree git exclude
// usually hides it already, but a checkout that missed excludeAgentDir (test
// fixtures, hand-made worktrees) must not read pie's own contract files or
// mcp-config as "the agent changed something".
func HasChanges(worktreeDir string) bool {
	out, _ := run(worktreeDir, "status", "--porcelain", "--", ".", ":(exclude).agent")
	return strings.TrimSpace(out) != ""
}

// CommitAll stages and commits everything in the worktree.
func CommitAll(worktreeDir, msg string) error {
	if _, err := run(worktreeDir, "add", "-A"); err != nil {
		return err
	}
	_, err := run(worktreeDir, "commit", "-m", msg)
	return err
}

// Push force-pushes the branch to origin (with lease) and sets upstream. The
// ai/<ticket>-<slug> branches are Apple Pie-owned and regenerated each run, so a
// fresh re-run rewrites history and a plain push would be rejected as non-fast-
// forward. --force-with-lease overwrites the prior remote branch safely:
// CreateWorktree fetches first, so the lease reflects the current remote.
func Push(worktreeDir, branch string) error {
	_, err := run(worktreeDir, "push", "--force-with-lease", "-u", "origin", branch)
	return err
}

// BranchHolder returns the worktree path that has branch checked out, or ""
// when the branch is free. The start-from-branch flow asks BEFORE spawning a
// run, so the user can authorize the reconciliation inside the tool instead of
// meeting a refusal after the fact.
func BranchHolder(repo, branch string) string {
	out, err := run(repo, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	path := ""
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			path = strings.TrimPrefix(ln, "worktree ")
		case ln == "branch refs/heads/"+branch:
			return path
		}
	}
	return ""
}

// DetachHolder frees a branch by detaching the holding worktree's HEAD at its
// current commit. Nothing else moves: same files, same commit, uncommitted
// changes untouched - the checkout merely stops naming the branch, which is
// the smallest possible reconciliation and the only one safe to automate.
func DetachHolder(holder string) error {
	_, err := run(holder, "switch", "--detach")
	return err
}
