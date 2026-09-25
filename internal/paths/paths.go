// Package paths centralizes the ~/.pie directory layout.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Root is ~/.pie (override with $PIE_HOME, mainly for tests).
func Root() string {
	if v := os.Getenv("PIE_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pie")
}

func Config() string    { return filepath.Join(Root(), "config.toml") }
func StateDB() string   { return filepath.Join(Root(), "state.db") }
func Worktrees() string { return filepath.Join(Root(), "worktrees") }
func Logs() string      { return filepath.Join(Root(), "logs") }
func Templates() string { return filepath.Join(Root(), "templates") }
func Reports() string   { return filepath.Join(Root(), "reports") }
func Plans() string     { return filepath.Join(Root(), "plans") }
func Pasted() string    { return filepath.Join(Root(), "pasted") }
func DaemonLog() string { return filepath.Join(Root(), "daemon.log") }
func PidFile() string   { return filepath.Join(Root(), "daemon.pid") }

// ValidTicketID reports whether id is safe to use as a single path element under
// Apple Pie's home. Ticket ids arrive from CLI args and the hub's free-form
// prompt, and the paths built from them are handed to os.RemoveAll (see
// git.CreateWorktree), so anything that could traverse out of ~/.pie has to
// be rejected at the boundary rather than sanitized silently.
func ValidTicketID(id string) bool {
	if strings.TrimSpace(id) != id || id == "" {
		return false
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ticketElem reduces a ticket id to a single traversal-free path element. This
// is the last line of defense behind ValidTicketID: callers validate and report
// a clear error, and anything that slips through still lands inside the
// Apple Pie home instead of escaping it.
func ticketElem(ticket string) string {
	e := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, strings.TrimSpace(ticket))
	e = strings.Trim(e, ".") // collapses "." and ".." to ""
	if e == "" {
		e = "_"
	}
	return e
}

// WorktreeFor returns a ticket's worktree path under ~/.pie/worktrees/<ticket>.
// All worktrees live in Apple Pie's own home so they're easy to find regardless
// of which repo the ticket came from.
func WorktreeFor(_, ticket string) string {
	return filepath.Join(Worktrees(), ticketElem(ticket))
}

func LogFor(ticket string) string { return filepath.Join(Logs(), ticketElem(ticket)+".log") }

// ReportFor is where Apple Pie archives a ticket's completion report, kept in
// Apple Pie's own home so it never lands in the target repo / PR.
func ReportFor(ticket string) string { return filepath.Join(Reports(), ticketElem(ticket)+".json") }

// PlanMDFor / PlanJSONFor are the durable copies of a ticket's plan, archived in
// Apple Pie's own home right after planning - the worktree's .agent/ scratch is
// git-excluded and disposable, so these never land in the target repo / PR.
func PlanMDFor(ticket string) string   { return filepath.Join(Plans(), ticketElem(ticket)+".md") }
func PlanJSONFor(ticket string) string { return filepath.Join(Plans(), ticketElem(ticket)+".json") }

// UnderWorktrees reports whether p resolves inside Apple Pie's worktrees root.
// Callers use this to gate recursive deletes on paths they did not construct
// themselves.
func UnderWorktrees(p string) bool {
	if p == "" || !filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	root := filepath.Clean(Worktrees())
	if clean == root {
		return false // the root itself is never a worktree
	}
	rel, err := filepath.Rel(root, clean)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WorktreeReady reports whether dir is a usable git worktree, i.e. it still has
// its .git link.
//
// This encodes one user-facing rule and both sides must agree on it: the runner
// refuses --resume/--ship without a ready worktree, and the TUI decides from the
// same answer whether to offer Resume / Open Studio / Open Claude at all. When
// they drifted apart the menu offered actions the runner would then reject.
func WorktreeReady(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// WriteLocalProperties writes sdk.dir to <dir>/local.properties so Gradle can
// locate the Android SDK in fresh worktrees where the file is git-ignored.
// Call it with the PROJECT dir (next to settings.gradle - in a monorepo that is
// a subdirectory of the worktree), not the worktree root, or Gradle never sees
// it. An existing file is left untouched: a seeded or hand-placed copy carries
// this machine's real SDK path and must win. No-op when sdkPath is empty.
func WriteLocalProperties(dir, sdkPath string) error {
	if sdkPath == "" {
		return nil
	}
	p := filepath.Join(dir, "local.properties")
	if _, err := os.Stat(p); err == nil {
		return nil
	}
	return os.WriteFile(p, []byte(fmt.Sprintf("sdk.dir=%s\n", sdkPath)), 0o644)
}

// EnsureDirs creates the directory skeleton if missing.
func EnsureDirs() error {
	root := Root()
	for _, d := range []string{root, Worktrees(), Logs(), Templates(), Reports(), Plans(), Pasted()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
