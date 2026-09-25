// Package vcs opens PRs via `gh` and renders the PR body + Jira comment from
// user-editable templates (Decision 10).
package vcs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// PlanData is the template context for the plan Jira comment.
type PlanData struct {
	Ticket     string
	Summary    string
	Plan       string
	Steps      []string
	Questions  []string
	Confidence string
	Type       string
}

// PRData is the template context for both the PR body and the Jira comment.
type PRData struct {
	Ticket       string
	Summary      string
	Branch       string
	Base         string
	FilesChanged []string
	Tests        string
	Attempts     int
	PRURL        string
	JiraBaseURL  string
}

const defaultPRBody = `# [{{.Ticket}}] {{.Summary}}

Ticket: {{.Ticket}} · Branch: {{.Branch}}

## Summary
{{.Summary}}

## Changes
{{if .FilesChanged}}{{range .FilesChanged}}- {{.}}
{{end}}{{else}}- (not reported)
{{end}}
## Checks
{{if .Tests}}{{.Tests}}{{else}}(not reported){{end}}

🤖 Generated autonomously by Apple Pie · review before merge
`

const defaultJiraComment = `✅ PR ready for review → {{.PRURL}}
{{.Tests}} · {{.Attempts}} attempt(s)
`

const defaultPlanComment = `{{- if .Questions}}
🤖 *Apple Pie has questions before starting [{{.Ticket}}]*

*Please update the ticket description* with your answers to the questions below, then re-run:
{code}pie run {{.Ticket}}{code}

*Questions:*
{{range .Questions}}- {{.}}
{{end}}
----
*Proposed plan (pending your answers):*
{{.Plan}}
*Type:* {{.Type}} · *Confidence:* {{.Confidence}}
{{- if .Steps}}

*Steps:*
{{range .Steps}}- {{.}}
{{end}}
{{- end}}
{{- else}}
🤖 *Apple Pie plan for [{{.Ticket}}]*

{{.Plan}}
*Type:* {{.Type}} · *Confidence:* {{.Confidence}}
{{- if .Steps}}

*Steps:*
{{range .Steps}}- {{.}}
{{end}}
{{- end}}
No blockers - proceeding with implementation automatically.
{{- end}}`

func render(name, def string, data any) (string, error) {
	text := def
	if b, err := os.ReadFile(filepath.Join(paths.Templates(), name)); err == nil {
		text = string(b)
	}
	tmpl, err := template.New(name).Parse(text)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// RenderPRBody renders the PR description.
func RenderPRBody(d PRData) (string, error) { return render("pr_body.tmpl", defaultPRBody, d) }

// RenderJiraComment renders the ticket comment.
func RenderJiraComment(d PRData) (string, error) {
	return render("jira_comment.tmpl", defaultJiraComment, d)
}

// RenderPlanComment renders the plan stage Jira comment.
func RenderPlanComment(d PlanData) (string, error) {
	return render("plan_comment.tmpl", defaultPlanComment, d)
}

// prCreateArgs builds the gh argv for creating a PR. Split out so tests can
// pin the shape without shelling out to gh.
func prCreateArgs(base, title, bodyFile string, draft bool) []string {
	args := []string{"pr", "create", "--base", base, "--title", title, "--body-file", bodyFile}
	if draft {
		args = append(args, "--draft")
	}
	return args
}

// draftUnsupported reports whether gh refused the PR because the repository
// plan has no draft PRs (GitHub Free private repos). The caller retries
// without --draft then - a regular PR beats no PR.
func draftUnsupported(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "draft") && strings.Contains(s, "not supported")
}

// OpenPR ensures the current branch has a PR, and returns its URL plus
// whether it already existed. It checks FIRST, before ever shelling out to
// `gh pr create`: a --from-branch ticket continuing an open PR (addressing
// review feedback, say) must never get a second, competing PR opened against
// it, and a second resume/ship on the SAME ticket must not either. Only when
// no PR exists yet does it create one - as a DRAFT, since the pipeline stops
// at review by design (never auto-merge) and a draft says "machine-made,
// human not yet vouching" to reviewers and CI. Repos whose plan lacks draft
// PRs get a regular PR instead.
func OpenPR(worktreeDir, base, title, body string) (url string, existed bool, err error) {
	if existing := BranchPR(worktreeDir); existing != "" {
		return existing, true, nil
	}

	tmp, err := os.CreateTemp("", "pie-pr-*.md")
	if err != nil {
		return "", false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(body); err != nil {
		return "", false, err
	}
	tmp.Close()

	cmd := exec.Command("gh", prCreateArgs(base, title, tmp.Name(), true)...)
	cmd.Dir = worktreeDir
	out, err := cmd.CombinedOutput()
	if err != nil && draftUnsupported(out) {
		cmd = exec.Command("gh", prCreateArgs(base, title, tmp.Name(), false)...)
		cmd.Dir = worktreeDir
		out, err = cmd.CombinedOutput()
	}
	if err != nil {
		// A PR may have appeared between the check above and this call (a race
		// with another ship, or the check itself missing it) - re-check once
		// rather than fail a ticket that actually has a PR.
		if existing := BranchPR(worktreeDir); existing != "" {
			return existing, true, nil
		}
		return "", false, fmt.Errorf("gh pr create: %w\n%s", err, out)
	}
	for _, f := range strings.Fields(string(out)) {
		if strings.HasPrefix(f, "http") {
			return f, false, nil
		}
	}
	return strings.TrimSpace(string(out)), false, nil
}

// BranchPR returns the URL of the PR for the current branch in worktreeDir,
// or "" if there is none (or gh can't tell). This is what lets OpenPR detect a
// branch's PR before deciding whether to create one.
func BranchPR(worktreeDir string) string {
	cmd := exec.Command("gh", "pr", "view", "--json", "url", "-q", ".url")
	cmd.Dir = worktreeDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// PRState returns the PR's state via gh: "OPEN", "MERGED", or "CLOSED".
// repoDir gives gh the repository context.
func PRState(repoDir, prURL string) (string, error) {
	cmd := exec.Command("gh", "pr", "view", prURL, "--json", "state", "-q", ".state")
	cmd.Dir = repoDir
	// Output(), not CombinedOutput(): the result is parsed by the daemon to
	// decide whether to tear down a worktree, and gh writes upgrade notices and
	// other chatter to stderr. Folding that into the value makes every state
	// comparison miss, so cleanup silently never fires.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh pr view: %w\n%s", err, stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

var headingRe = regexp.MustCompile(`(?m)^#{1,6}\s+(.+)$`)

// ReadRepoTemplate returns the content of the first PR template found in the
// worktree (.github/PULL_REQUEST_TEMPLATE.md etc.), or "" when none exists.
func ReadRepoTemplate(worktreeDir string) string {
	for _, rel := range []string{
		".github/PULL_REQUEST_TEMPLATE.md",
		".github/pull_request_template.md",
		"docs/PULL_REQUEST_TEMPLATE.md",
	} {
		if b, err := os.ReadFile(filepath.Join(worktreeDir, rel)); err == nil {
			return string(b)
		}
	}
	return ""
}

// MissingSections returns the heading text of every section in tmpl whose
// heading does not appear in body (case-insensitive). Checklist lines are not
// examined — only the headings themselves are compared.
func MissingSections(tmpl, body string) []string {
	bodyLower := strings.ToLower(body)
	var missing []string
	for _, m := range headingRe.FindAllStringSubmatch(tmpl, -1) {
		heading := strings.TrimSpace(m[1])
		if !strings.Contains(bodyLower, strings.ToLower(heading)) {
			missing = append(missing, heading)
		}
	}
	return missing
}

// RepairSections appends the verbatim template content for each of the
// missingSections to body. Only the template's own wording is restored — no
// invented text is added.
func RepairSections(tmpl, body string, missingSections []string) string {
	if len(missingSections) == 0 {
		return body
	}
	missing := make(map[string]bool, len(missingSections))
	for _, s := range missingSections {
		missing[strings.ToLower(strings.TrimSpace(s))] = true
	}

	// Locate every heading's start position in the template.
	type sectionSpan struct {
		start, end int
		heading    string
	}
	allIdx := headingRe.FindAllStringSubmatchIndex(tmpl, -1)
	secs := make([]sectionSpan, 0, len(allIdx))
	for _, idx := range allIdx {
		heading := strings.TrimSpace(tmpl[idx[2]:idx[3]])
		secs = append(secs, sectionSpan{start: idx[0], end: len(tmpl), heading: heading})
		if len(secs) > 1 {
			secs[len(secs)-2].end = idx[0]
		}
	}

	var appended strings.Builder
	for _, sec := range secs {
		if missing[strings.ToLower(sec.heading)] {
			appended.WriteString("\n\n")
			appended.WriteString(strings.TrimRight(tmpl[sec.start:sec.end], "\n"))
		}
	}
	return body + appended.String()
}

// WriteDefaultTemplates drops the default templates if they don't exist yet.
func WriteDefaultTemplates() error {
	defaults := map[string]string{
		"pr_body.tmpl":      defaultPRBody,
		"jira_comment.tmpl": defaultJiraComment,
		"plan_comment.tmpl": defaultPlanComment,
	}
	for name, def := range defaults {
		p := filepath.Join(paths.Templates(), name)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			if err := os.WriteFile(p, []byte(def), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
