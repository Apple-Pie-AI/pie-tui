// Terminal-cell text helpers: everything here measures or wraps in display
// columns rather than bytes, which is what separates it from the plain string
// helpers in ticket.go.
package tui

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// renderMarkdownLines renders markdown to styled terminal rows via glamour,
// wrapped to width w. A fixed "dark" style avoids glamour querying the terminal
// for its background (which would clash with bubbletea's raw-mode screen). On any
// error it falls back to the raw markdown so the viewer is never blank.
func renderMarkdownLines(md string, w int) []string {
	if w < 20 {
		w = 20
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(w),
	)
	if err != nil {
		return strings.Split(md, "\n")
	}
	out, err := r.Render(md)
	if err != nil {
		return strings.Split(md, "\n")
	}
	return strings.Split(strings.TrimRight(out, "\n"), "\n")
}

// tailLines returns up to the last n non-blank lines of a file (reading ≤64KB).
func tailLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil
	}
	const maxRead = 64 * 1024
	start := int64(0)
	if fi.Size() > maxRead {
		start = fi.Size() - maxRead
	}
	buf := make([]byte, fi.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(strings.TrimRight(string(buf), "\n"), "\n") {
		if strings.TrimSpace(ln) != "" {
			out = append(out, ln)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func cleanActivity(lines []string, ticket string) string {
	if len(lines) == 0 {
		return ""
	}
	s := stripPrefix(lines[len(lines)-1], ticket)
	s = strings.TrimLeft(s, " •⚙─-")
	return strings.TrimSpace(s)
}

func stripPrefix(line, ticket string) string {
	return strings.TrimPrefix(line, "["+ticket+"] ")
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w <= 1 {
		return "…"
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// wrapLine breaks s into visual segments each at most w columns wide. Used in
// log views so long lines wrap instead of being cut off with "…".
func wrapLine(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	if lipgloss.Width(s) <= w {
		return []string{s}
	}
	var out []string
	r := []rune(s)
	for len(r) > 0 {
		take := w
		if take > len(r) {
			take = len(r)
		}
		for take > 0 && lipgloss.Width(string(r[:take])) > w {
			take--
		}
		if take == 0 {
			take = 1
		}
		out = append(out, string(r[:take]))
		r = r[take:]
	}
	return out
}

// wrapWords breaks s at word boundaries, never mid-word. wrapLine above cuts on
// the column, which turned "unconventional" into "unconventi / onal" - a break
// that costs a reader more than the ragged right edge it was avoiding.
//
// A single word longer than the width (a URL, a stack frame) still has to be
// cut, because the alternative is a line that overflows.
func wrapWords(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	if lipWidth(s) <= w {
		return []string{s}
	}
	// The leading whitespace is the line's indent, and it belongs to every
	// segment: continuation lines that start at column 0 read as new entries.
	indent := s[:len(s)-len(strings.TrimLeft(s, " \t"))]
	if lipWidth(indent)+8 > w {
		indent = "" // an indent that leaves no room for words is not an indent
	}
	var out []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = indent + word
		case lipWidth(cur)+1+lipWidth(word) <= w:
			cur += " " + word
		default:
			out = append(out, cur)
			cur = indent + word
		}
		// One word too long for the line: hard-cut it and carry the remainder.
		// Each pass emits w cells and re-indents, and the indent is capped well
		// below w above, so cur shrinks every time round.
		for lipWidth(cur) > w {
			seg := wrapLine(cur, w)
			out = append(out, seg[0])
			cur = indent + strings.Join(seg[1:], "")
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// dedupeAdjacent drops a line that repeats the one before it. Agents narrate the
// same finding twice - once as the message and again as a summary - and the log
// tail is short enough that one repeat costs a third of it.
//
// Only consecutive repeats, and compared on their squashed text, so "  Wrote
// report.json" and "Wrote  report.json" count as the same line. Two genuinely
// identical lines far apart stay: that is a loop, which is worth seeing.
func dedupeAdjacent(lines []string) []string {
	out := make([]string, 0, len(lines))
	prev := ""
	for _, ln := range lines {
		key := strings.ToLower(strings.Join(strings.Fields(ln), " "))
		if key != "" && key == prev {
			continue
		}
		prev = key
		out = append(out, ln)
	}
	return out
}

// stripEmphasis removes markdown emphasis markers from text bound for a plain
// terminal row. The agent writes markdown; the log pane is not a markdown
// renderer, so "**Duplicate VideoItem class**" arrived with its asterisks on.
//
// Only markers that actually wrap a phrase are dropped - the marker must sit
// on a word boundary (start-of-line/space/bracket outside, non-space inside).
// The old "any two occurrences" rule mangled content that merely contains
// the characters: `extra_allowed_tools` lost its underscores and
// `Bash(cd:*) Bash(./gradlew:*)` its asterisks - in the exact TOML line the
// permission park tells users to copy verbatim. Never lose content to a
// formatting fix.
func stripEmphasis(s string) string {
	for _, re := range emphasisRes {
		// A pass consumes the boundary character shared by adjacent pairs
		// ("*a* *b*"), so iterate until stable; 4 passes bounds any real line.
		for i := 0; i < 4; i++ {
			out := re.ReplaceAllString(s, "${1}${2}${3}")
			if out == s {
				break
			}
			s = out
		}
	}
	return s
}

// emphasisRes matches one emphasis-wrapped phrase per marker: an opening
// marker preceded by line start / whitespace / an opening bracket and
// followed by non-space, then the phrase, then a closing marker preceded by
// non-space and followed by line end / whitespace / closing punctuation.
var emphasisRes = func() []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, 6)
	for _, m := range []string{"***", "**", "*", "__", "_", "`"} {
		q := regexp.QuoteMeta(m)
		res = append(res, regexp.MustCompile(
			`(^|[\s(\[{"'])`+q+`(\S(?:.*?\S)?)`+q+`($|[\s)\].,:;!?"'}])`))
	}
	return res
}()

// Age renders how long ago t was, in the compact form the dashboard and
// `pie status` both use ("42s", "7m", "3h", "2d"). Exported because those two
// views must agree - a session that reads "3h" in one must not read "180m" in
// the other.
func Age(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// lipWidth is the display width of a string, ignoring ANSI escapes.
func lipWidth(s string) int { return lipgloss.Width(s) }
