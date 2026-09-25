package ticket

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// Normalize canonicalises a ticket id as typed by a human: upper-cased and
// trimmed. Every entrypoint that accepts an id runs it through this so
// "  proj-1 " and "PROJ-1" resolve to the same worktree, branch and log file.
func Normalize(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Spec is one resolved unit of work, from Jira or a local file.
// It is source-agnostic: the runner only ever sees ticket/summary/desc.
type Spec struct {
	Ticket     string // CLEAN id, safe for WorktreeFor/branch/LogFor
	Summary    string // may be "" for Jira (filled in after FetchIssue)
	Desc       string
	Local      bool     // true => skip Jira fetch/transition and never comment to Jira
	SourcePath string   // absolute path to the .md file; empty for Jira tickets
	Images     []string // image files attached to the ticket (pasted-content flow)
	StackBase  string   // bare base branch name for stacked diffs; "" = default
	Branch     string   // exact branch name (--branch); "" = derive from the repo pattern
	FromBranch bool     // Branch names an EXISTING branch to work on (checkout, never reset)
}

var (
	h1Re    = regexp.MustCompile(`^#\s+(.*\S)\s*$`)
	nonIDRe = regexp.MustCompile(`[^A-Z0-9]+`)
	// ticketIDRe matches a Jira-style key anywhere in text: a project key (a
	// letter followed by letters/digits) then a dash and a number, e.g. PROJ-123.
	ticketIDRe = regexp.MustCompile(`\b([A-Za-z][A-Za-z0-9]*-\d+)\b`)
	// ticketKeyRe is ticketIDRe anchored: the whole (trimmed, single-line) string
	// IS a bare key. Used to classify a pasted token as a Jira key vs content.
	ticketKeyRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*-\d+$`)
)

// sectionHeads are generic section headings that are not real ticket titles.
// Spec-/template-style tickets often open with "# Description" or
// "# Acceptance Criteria"; using those as the title is useless, so we skip them
// and fall back to the first real line of prose.
var sectionHeads = map[string]bool{
	"description":          true,
	"summary":              true,
	"overview":             true,
	"context":              true,
	"background":           true,
	"details":              true,
	"notes":                true,
	"dependencies":         true,
	"acceptance criteria":  true,
	"requirements":         true,
	"implementation":       true,
	"implementation notes": true,
	"goal":                 true,
	"goals":                true,
	"non-goals":            true,
	"problem":              true,
	"solution":             true,
	"tasks":                true,
	"scope":                true,
	"testing":              true,
	"test plan":            true,
}

// maxDerivedTitleLen caps a title derived from a body line so a long opening
// sentence doesn't become an unwieldy PR title. H1 titles are left untouched.
const maxDerivedTitleLen = 100

// LoadLocal reads a markdown/text file into a Spec.
// Title = first H1 ("# ...") line, else the first non-blank line.
// Body  = everything after the H1 line (or the full content if there is no H1).
// The ticket id is derived from the base filename (see IDFromPath).
func LoadLocal(path string) (Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("read %s: %w", path, err)
	}
	title, body, err := ParseContent(string(raw))
	if err != nil {
		return Spec{}, fmt.Errorf("%s: %w", path, err)
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	id := IDFromPath(path)
	return Spec{
		Ticket:     id,
		Summary:    StripIDPrefix(title, id),
		Desc:       body,
		Local:      true,
		SourcePath: abs,
		Images:     DiscoverPastedImages(abs),
	}, nil
}

// DiscoverPastedImages returns sibling image files when the ticket .md lives in a
// Apple Pie-controlled pasted dir (~/.pie/pasted/<id>/). Returns nil for a
// user's own .md file, so we never sweep in unrelated images from their repo.
func DiscoverPastedImages(mdPath string) []string {
	dir := filepath.Dir(mdPath)
	pastedAbs, err := filepath.Abs(paths.Pasted())
	if err != nil || !strings.HasPrefix(dir, pastedAbs+string(os.PathSeparator)) {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var imgs []string
	for _, e := range entries {
		if !e.IsDir() && IsImageExt(e.Name()) {
			imgs = append(imgs, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(imgs)
	return imgs
}

// IsImageExt reports whether name has a supported image extension.
func IsImageExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// StripIDPrefix removes a leading ticket-id from a title so it isn't shown (and
// slugified into the branch) twice. Jira-style titles often read "TICKET-3 - Add
// X"; with the id already in the chip and the "ai/{ticket}-{slug}" branch, that
// prefix is redundant. Only strips when a separator (or end) follows the id, so
// "TICKET-30 …" is left intact when the id is "TICKET-3".
func StripIDPrefix(title, id string) string {
	if id == "" {
		return title
	}
	t := strings.TrimSpace(title)
	if len(t) < len(id) || !strings.EqualFold(t[:len(id)], id) {
		return title
	}
	rest := t[len(id):]
	if rest == "" {
		return title // title is only the id
	}
	trimmed := strings.TrimLeft(rest, " \t--–:·|.")
	if trimmed == rest || trimmed == "" {
		return title // no separator after the id, or nothing left
	}
	return trimmed
}

// ParseContent derives a (title, body) from raw markdown/text ticket
// content. Title = first H1 ("# ...") that is a real title (not a generic
// section header like "# Description"), else the first non-blank line of prose.
// Body = everything after that H1, or the whole content when there is no H1.
// Shared by LoadLocal (file source) and pasted-content tickets (TUI).
func ParseContent(raw string) (title, body string, err error) {
	lines := strings.Split(raw, "\n")
	body = raw
	for i, line := range lines {
		m := h1Re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if sectionHeads[strings.ToLower(m[1])] {
			continue // a section header, not a title - keep the body intact
		}
		title = m[1]
		body = strings.TrimLeft(strings.Join(lines[i+1:], "\n"), "\n")
		break
	}
	if title == "" {
		for _, line := range lines {
			s := strings.TrimSpace(line)
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			title = TruncateTitle(s)
			break
		}
	}
	if title == "" {
		return "", "", fmt.Errorf("could not derive a title (empty content?)")
	}
	return title, strings.TrimSpace(body), nil
}

// DetectID pulls the first Jira-style key (e.g. PROJ-123) out of free-form
// ticket text - the instant, offline fast-path before falling back to an AI
// extraction pass. Returns the id uppercased, or ("", false) when none is found.
func DetectID(content string) (string, bool) {
	if m := ticketIDRe.FindStringSubmatch(content); m != nil {
		return strings.ToUpper(m[1]), true
	}
	return "", false
}

// IDFromBranch derives a ticket id from a branch name: once the user has
// pointed a ticket at an existing branch, that branch IS its identity - there
// is nothing left for a separately-typed id to name. Branch namespaces
// ("feedback/pr-42") and any other character invalid in a worktree/dashboard-
// row id collapse to "-"; re-running against the same branch reuses the same
// id on purpose, the same way re-running a Jira key reuses its worktree.
func IDFromBranch(branch string) string {
	id := strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ' ' || r == '\t' || r < 0x20 || r == 0x7f:
			return '-'
		default:
			return r
		}
	}, strings.TrimSpace(branch))
	for strings.Contains(id, "--") {
		id = strings.ReplaceAll(id, "--", "-")
	}
	id = strings.Trim(id, "-")
	if id == "" {
		id = "branch"
	}
	return id
}

// NormalizeNewlines converts CRLF and lone CR line endings to LF. Terminal
// bracketed paste transmits newlines as carriage returns, so pasted content
// arrives with \r; without this, line counting and title parsing see one giant
// line and stray \r carriage-returns corrupt the on-screen render.
func NormalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// IsKey reports whether s (trimmed, single line) is itself a bare Jira key.
// Used to classify a pasted token as a Jira key rather than ticket content.
func IsKey(s string) bool {
	return ticketKeyRe.MatchString(strings.TrimSpace(s))
}

// TruncateTitle shortens an over-long body-derived title at a word boundary,
// appending an ellipsis. Short titles pass through unchanged.
func TruncateTitle(s string) string {
	r := []rune(s)
	if len(r) <= maxDerivedTitleLen {
		return s
	}
	cut := string(r[:maxDerivedTitleLen])
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}

// TruncateWords returns s truncated to at most n words (space-joined).
// Unicode-safe; each whitespace run counts as a word boundary.
func TruncateWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return s
	}
	return strings.Join(words[:n], " ")
}

// FirstSentence returns the text up to (but not including) the first period or
// newline, trimmed. Returns the whole string when neither is found.
func FirstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

// FirstNonEmpty returns the first argument whose trimmed value is non-empty.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// IDFromPath turns a/b/ticket_1.md into "TICKET-1": strip dir + extension,
// uppercase, collapse every run of non-[A-Z0-9] into a single '-', trim '-'.
// Falls back to "LOCAL" if nothing usable remains. The result is safe to feed
// into paths.WorktreeFor / paths.LogFor and the "ai/{ticket}-{slug}" template.
func IDFromPath(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	id := nonIDRe.ReplaceAllString(strings.ToUpper(base), "-")
	id = strings.Trim(id, "-")
	if id == "" {
		return "LOCAL"
	}
	return id
}

// ---- pasted tickets --------------------------------------------------------
//
// A ticket typed or pasted into the TUI is written to ~/.pie/pasted/<id>/ as
// <id>.md plus img-N.<ext> siblings. WritePasted below and
// DiscoverPastedImages above are the two halves of that layout and must agree
// on it; they used to live ~1,000 lines apart in different files.

// WritePasted saves a pasted ticket to ~/.pie/pasted/<id>/<id>.md (so
// WritePasted saves a pasted ticket to ~/.pie/pasted/<id>/<id>.md (so
// IDFromPath recovers <id>) and copies its images beside it. Returns the
// .md path for `pie run`. Images are copied (not referenced) so a later
// move/delete of the user's original screenshot can't break the run.
func WritePasted(id, body string, images []string) (string, error) {
	if err := paths.EnsureDirs(); err != nil {
		return "", err
	}
	if id == "" {
		id = "PASTED"
	}
	dir := filepath.Join(paths.Pasted(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	mdPath := filepath.Join(dir, id+".md")
	if err := os.WriteFile(mdPath, []byte(body), 0o644); err != nil {
		return "", err
	}
	for i, src := range images {
		data, err := os.ReadFile(src)
		if err != nil {
			return "", fmt.Errorf("read image %s: %w", src, err)
		}
		dst := filepath.Join(dir, fmt.Sprintf("img-%d%s", i+1, strings.ToLower(filepath.Ext(src))))
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", fmt.Errorf("write image %s: %w", dst, err)
		}
	}
	return mdPath, nil
}

// ParseDroppedPaths splits terminal-inserted file paths, honoring single/double
// quotes and backslash-escaped spaces (how a terminal encodes a dragged path).
func ParseDroppedPaths(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle, inDouble, esc := false, false, false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case esc:
			cur.WriteRune(r)
			esc = false
		case r == '\\' && !inSingle:
			esc = true
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t' || r == '\n' || r == '\r') && !inSingle && !inDouble:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// IsImageFile reports whether p is an existing file with an image extension.
func IsImageFile(p string) bool {
	if !IsImageExt(p) {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// LineCount returns the number of lines in s, ignoring a single trailing newline.
func LineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func ImgSuffix(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(" · %d img", n)
}

// ---- doctor output ---------------------------------------------------------
