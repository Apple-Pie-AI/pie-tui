// Deriving names and text from a ticket: the branch, the PR title, the slug, and
// the staged-image references appended to the description.
package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
)

// stageImages copies the task's attached images into <worktree>/.agent/images and
// returns the description with a reference section appended so the plan/implement
// prompts tell the agent to Read them. Returns the plain description on any issue.
func stageImages(t Task, worktree string, logf func(string, ...interface{})) string {
	if len(t.Images) == 0 {
		return t.Description
	}
	dir := filepath.Join(worktree, ".agent", "images")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logf("[%s] (warn) image dir: %v", t.Ticket, err)
		return t.Description
	}
	var refs []string
	for i, src := range t.Images {
		name := fmt.Sprintf("img-%d%s", i+1, strings.ToLower(filepath.Ext(src)))
		data, err := os.ReadFile(src)
		if err != nil {
			logf("[%s] (warn) read image %s: %v", t.Ticket, src, err)
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			logf("[%s] (warn) write image %s: %v", t.Ticket, name, err)
			continue
		}
		refs = append(refs, ".agent/images/"+name)
	}
	if len(refs) == 0 {
		return t.Description
	}
	return t.Description +
		"\n\nAttached screenshots - use the Read tool to view each before planning and implementing:\n- " +
		strings.Join(refs, "\n- ")
}

// branchFor picks the branch for a run: an explicit override (the name the user
// confirmed at creation, via --branch) wins; else the branch recorded by a
// previous run, so re-runs and resume/ship keep a customized name instead of
// silently recomputing the pattern; else the repo pattern.
func branchFor(t Task) string {
	if t.Branch != "" {
		return t.Branch
	}
	if t.Store != nil {
		if sess, err := t.Store.Get(t.Ticket); err == nil && sess != nil && sess.Branch != "" {
			return sess.Branch
		}
	}
	return ResolveBranch(t.Repo.Branch, t.Ticket, t.Summary)
}

// ResolveBranch fills a repo's branch pattern for a ticket, substituting the
// {ticket} (lowercased id) and {slug} (kebab-case title) placeholders. Exported
// so the TUI can pre-fill the creation-time branch field with the same result.
func ResolveBranch(pattern, ticket, summary string) string {
	return strings.NewReplacer(
		"{ticket}", strings.ToLower(ticket),
		"{slug}", slugify(summary),
	).Replace(pattern)
}

// prTitleFor builds the PR title, prepending a [feat]/[bug]/[chore] prefix
// when the ticket's plan.json records a type.
func prTitleFor(worktree, ticket, summary string) string {
	prefix := ""
	if p, err := agent.ReadPlan(worktree); err == nil && p != nil {
		switch p.Type {
		case "feature":
			prefix = "[feat]"
		case "bug", "chore":
			prefix = "[" + p.Type + "]"
		}
	}
	return fmt.Sprintf("%s[%s] %s", prefix, ticket, summary)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.Trim(slugRe.ReplaceAllString(s, "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "change"
	}
	return s
}

func tail(s string, n int) string {
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}

func oneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
