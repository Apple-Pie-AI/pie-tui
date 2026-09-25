// The .agent/ JSON contract: the files a headless Claude Code session writes to
// report back, and the readers that load them (Decision 6). The agent owns the
// worktree's .agent/ scratch dir; it is git-excluded and never lands in a PR.
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Report is the .agent/report.json the session must write as its last step.
type Report struct {
	Status       string   `json:"status"` // "ready_for_build" | "needs_human"
	Summary      string   `json:"summary"`
	FilesChanged []string `json:"filesChanged"`
	Branch       string   `json:"branch"`
	Tests        string   `json:"tests"`
	// PRBody is the repo's own pull-request template, filled in for this change.
	// Empty when the repo has no template (the orchestrator then falls back to
	// Apple Pie's default PR body).
	PRBody string `json:"prBody,omitempty"`
	// Verified is the agent's self-certification from the verify stage: true once
	// it has itself run this project's build + tests and seen them pass. nil/false
	// means unverified - the orchestrator stops at needs-you instead of opening a
	// PR. Apple Pie trusts this flag rather than running its own Gradle commands, so
	// per-project build setups and company-managed build skills all work.
	Verified *bool `json:"verified,omitempty"`
	// VerifyLog is a human-readable note of what the verify stage ran and the
	// outcome (and, on failure, the failing output tail and why).
	VerifyLog string `json:"verifyLog,omitempty"`
}

// Plan is the .agent/plan.json the plan stage must write before implementation.
// It no longer records build/test commands - the verify stage discovers and runs
// verification itself. NeedsEmulator is kept so the orchestrator can coordinate
// the shared emulator across concurrent tickets before the verify stage runs.
type Plan struct {
	Plan          string   `json:"plan"`           // the full plan, free-form markdown - Claude structures it however it wants
	Steps         []string `json:"steps"`          // legacy: no longer requested; still injected/rendered if a plan carries them
	Questions     []string `json:"questions"`      // blocking ambiguities; empty = proceed
	Confidence    string   `json:"confidence"`     // "high" | "medium" | "low"
	Type          string   `json:"type"`           // "bug" | "feature" | "chore"
	NeedsEmulator bool     `json:"needs_emulator"` // true if task requires instrumentation tests
	Diagram       string   `json:"diagram"`        // legacy: no longer requested; still rendered if a plan carries one
}

// Review is the .agent/review.json the self-review stage must write.
type Review struct {
	Verdict string   `json:"verdict"` // "pass" | "fix"
	Issues  []string `json:"issues"`  // actionable fix items when verdict=="fix"
}

// Fix verdict values in CommentFix.Action.
const (
	FixActionFixed   = "fixed"
	FixActionSkipped = "skipped"
)

// CommentFix is the agent's verdict on one review comment.
type CommentFix struct {
	ID     string `json:"id"`     // the id given to it in the prompt
	Action string `json:"action"` // FixActionFixed | FixActionSkipped
	Note   string `json:"note"`   // what changed, or why it was skipped
	// Reply is the agent's DRAFT of the reviewer-facing reply for a fixed
	// comment. It is a proposal, not a post: the human reads and may rewrite it
	// in the approve screen before anything reaches GitHub.
	Reply string `json:"reply,omitempty"`
}

// CommentFixes is the .agent/comment_fixes.json the address-comments stage must
// write. Without a per-comment verdict the orchestrator could only assume the
// whole batch was handled, which would silently mark a reviewer's request
// answered when the agent had in fact declined it.
type CommentFixes struct {
	Fixes []CommentFix `json:"fixes"`
}

// The three accessors below tolerate a nil receiver on purpose: the agent
// failing to write comment_fixes.json at all is a normal degraded path, and the
// orchestrator carries on from it with a warning rather than abandoning a fix
// that is already committed and pushed.

// Fixed returns the ids the agent reported actually fixing.
func (c *CommentFixes) Fixed() []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, f := range c.Fixes {
		if f.Action == FixActionFixed {
			out = append(out, f.ID)
		}
	}
	return out
}

// Skipped returns the fixes the agent declined, so the reason survives.
func (c *CommentFixes) Skipped() []CommentFix {
	if c == nil {
		return nil
	}
	var out []CommentFix
	for _, f := range c.Fixes {
		if f.Action != FixActionFixed {
			out = append(out, f)
		}
	}
	return out
}

// NoteFor returns the agent's note for one comment id.
func (c *CommentFixes) NoteFor(id string) string {
	if c == nil {
		return ""
	}
	for _, f := range c.Fixes {
		if f.ID == id {
			return f.Note
		}
	}
	return ""
}

// PlanMarkdown renders a plan as a markdown document (shared by the TUI's plan
// viewer and the durable ~/.pie/plans/<ticket>.md archive).
func PlanMarkdown(title string, plan *Plan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if plan.Confidence != "" || plan.Type != "" {
		fmt.Fprintf(&b, "**Confidence:** %s  ·  **Type:** %s\n\n", plan.Confidence, plan.Type)
	}
	// The plan body is free-form markdown with its own structure - include it
	// verbatim rather than forcing it under a heading.
	if strings.TrimSpace(plan.Plan) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(plan.Plan))
	}
	if strings.TrimSpace(plan.Diagram) != "" {
		fmt.Fprintf(&b, "## Diagram\n\n%s\n\n", plan.Diagram)
	}
	if len(plan.Steps) > 0 {
		b.WriteString("## Steps\n\n")
		for i, step := range plan.Steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
		b.WriteString("\n")
	}
	if len(plan.Questions) > 0 {
		b.WriteString("## Open questions\n\n")
		for _, q := range plan.Questions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// readAgentFile reads .agent/<name> from the worktree. The contract is that the
// agent writes its scratch dir at the worktree root, but when the configured
// repo is a module inside a larger repo, `git worktree add` checks out the whole
// containing repo and the actual project (where the agent works and writes
// .agent/) is a SUBDIRECTORY of the worktree. So if the file isn't at the root
// we search the tree for the shallowest .agent/<name> and use that.
func readAgentFile(worktreeDir, name string) ([]byte, error) {
	root := filepath.Join(worktreeDir, ".agent", name)
	if b, err := os.ReadFile(root); err == nil {
		return b, nil
	}
	if found := findAgentFile(worktreeDir, name); found != "" {
		if b, err := os.ReadFile(found); err == nil {
			return b, nil
		}
	}
	// Return the root-path error so the message names the expected location.
	return os.ReadFile(root)
}

// resolveAgentFile returns the path readAgentFile would read, or "" if none
// exists. Same precedence: worktree root first, then the shallowest match.
func resolveAgentFile(worktreeDir, name string) string {
	root := filepath.Join(worktreeDir, ".agent", name)
	if _, err := os.Stat(root); err == nil {
		return root
	}
	return findAgentFile(worktreeDir, name)
}

// findAgentFile returns the shallowest path to a "<dir>/.agent/<name>" anywhere
// under worktreeDir, or "" if none. Heavy/irrelevant dirs are skipped to keep the
// walk cheap; this only runs on the fallback path (file not at the root).
func findAgentFile(worktreeDir, name string) string {
	best, bestDepth := "", 1<<30
	_ = filepath.WalkDir(worktreeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		switch d.Name() {
		case ".git", "build", ".gradle", "node_modules", ".idea":
			return filepath.SkipDir
		}
		cand := filepath.Join(path, ".agent", name)
		if _, statErr := os.Stat(cand); statErr == nil {
			if depth := strings.Count(path, string(os.PathSeparator)); depth < bestDepth {
				best, bestDepth = cand, depth
			}
		}
		return nil
	})
	return best
}

// ReadReport loads .agent/report.json from the worktree.
func ReadReport(worktreeDir string) (*Report, error) {
	b, err := readAgentFile(worktreeDir, "report.json")
	if err != nil {
		return nil, err
	}
	var r Report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("parse report.json: %w", err)
	}
	return &r, nil
}

// WriteReport writes the report to <worktree>/.agent/report.json, creating
// the directory if needed. The change-review park uses it to persist the
// adopted in-memory report (with its PR-body restores) across the process
// boundary before any later approve process re-reads it.
func WriteReport(worktreeDir string, r *Report) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Join(worktreeDir, ".agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "report.json"), b, 0o644)
}

// ErrStaleReport is returned by ReadReportSince when the report on disk predates
// the stage that was supposed to write it.
var ErrStaleReport = errors.New("report.json predates this stage")

// ReadReportSince is ReadReport with a freshness guard, for the verify stage
// whose Verified flag is the only gate before a real PR. .agent/ is
// git-excluded and never purged between runs, so a session that dies before
// rewriting report.json would otherwise leave the PREVIOUS run's verdict on
// disk - and readAgentFile's fallback walk will happily surface a stale copy
// from a subdirectory. Failing closed here just stops the ticket at needs-you.
func ReadReportSince(worktreeDir string, notBefore time.Time) (*Report, error) {
	p := resolveAgentFile(worktreeDir, "report.json")
	if p == "" {
		// Preserve ReadReport's error, which names the expected location.
		return ReadReport(worktreeDir)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if fi.ModTime().Before(notBefore) {
		return nil, fmt.Errorf("%w: %s last written %s, stage began %s",
			ErrStaleReport, p,
			fi.ModTime().Format(time.RFC3339), notBefore.Format(time.RFC3339))
	}
	return ReadReport(worktreeDir)
}

// ReadPlan loads .agent/plan.json from the worktree.
func ReadPlan(worktreeDir string) (*Plan, error) {
	b, err := readAgentFile(worktreeDir, "plan.json")
	if err != nil {
		return nil, err
	}
	var plan Plan
	if err := json.Unmarshal(b, &plan); err != nil {
		return nil, fmt.Errorf("parse plan.json: %w", err)
	}
	return &plan, nil
}

// ReadCommentFixes loads .agent/comment_fixes.json from the worktree.
func ReadCommentFixes(worktreeDir string) (*CommentFixes, error) {
	b, err := readAgentFile(worktreeDir, "comment_fixes.json")
	if err != nil {
		return nil, err
	}
	var cf CommentFixes
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse comment_fixes.json: %w", err)
	}
	return &cf, nil
}

// ReadReview loads .agent/review.json from the worktree.
func ReadReview(worktreeDir string) (*Review, error) {
	b, err := readAgentFile(worktreeDir, "review.json")
	if err != nil {
		return nil, err
	}
	var rev Review
	if err := json.Unmarshal(b, &rev); err != nil {
		return nil, fmt.Errorf("parse review.json: %w", err)
	}
	return &rev, nil
}
