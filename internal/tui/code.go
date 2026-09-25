// Showing the code a review comment is actually about.
//
// GitHub's diffHunk is only the three or four lines that changed, which is
// rarely enough to judge a request like "route this through the repository
// instead". The worktree is already on disk, so the real file is read straight
// from it and a window around the commented line is shown - the thing you would
// otherwise leave the terminal to look at.
package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// codeLine is one source line in the context window.
type codeLine struct {
	num    int
	text   string
	marked bool // the line the comment is anchored to
}

// maxCodeFileBytes bounds what we are willing to read to show a few lines. A
// generated or vendored file can be enormous, and this runs on a keystroke.
const maxCodeFileBytes = 4 << 20 // 4 MB

// codeContext returns the lines around a comment's anchor, read from the
// worktree. radius is how many lines of context on each side.
//
// Returns nil (no error) when there is simply nothing to show - no path, no
// worktree, a deleted file - so the caller falls back to the diff hunk rather
// than reporting a failure the user cannot act on.
func codeContext(worktree, path string, line, radius int) []codeLine {
	if worktree == "" || path == "" || line <= 0 {
		return nil
	}
	// GitHub paths are repository-root relative and a worktree is a checkout of
	// the whole repository, so they line up directly. Cleaned and bounded to the
	// worktree so a crafted path cannot read outside it.
	full := filepath.Join(worktree, filepath.Clean("/"+path))
	if !strings.HasPrefix(full, filepath.Clean(worktree)+string(filepath.Separator)) {
		return nil
	}
	fi, err := os.Stat(full)
	if err != nil || fi.IsDir() || fi.Size() > maxCodeFileBytes {
		return nil
	}
	f, err := os.Open(full)
	if err != nil {
		return nil
	}
	defer f.Close()

	lo, hi := line-radius, line+radius
	if lo < 1 {
		lo = 1
	}
	var out []codeLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // a minified line must not abort the read
	for n := 1; sc.Scan() && n <= hi; n++ {
		if n < lo {
			continue
		}
		out = append(out, codeLine{num: n, text: sc.Text(), marked: n == line})
	}
	if err := sc.Err(); err != nil {
		return nil
	}
	// The anchor is past the end of the file: the comment refers to code that no
	// longer exists, so the diff hunk is the honest thing to show instead.
	if len(out) == 0 || out[len(out)-1].num < line {
		return nil
	}
	return out
}

// renderCode formats a context window into display rows: a line-number gutter,
// a marker on the commented line, and syntax highlighting.
func renderCode(lines []codeLine, filename string, width int) []string {
	if len(lines) == 0 {
		return nil
	}
	gutter := len(strconv.Itoa(lines[len(lines)-1].num))

	raw := make([]string, len(lines))
	for i, l := range lines {
		raw[i] = l.text
	}
	painted := highlight(strings.Join(raw, "\n"), filename)
	if len(painted) != len(raw) {
		painted = raw // highlighting disagreed about line count; show it plain
	}

	out := make([]string, 0, len(lines))
	for i, l := range lines {
		marker := "  "
		num := codeNumStyle.Render(pad(strconv.Itoa(l.num), gutter))
		if l.marked {
			marker = codeMarkStyle.Render("▶ ")
			num = codeNumHitStyle.Render(pad(strconv.Itoa(l.num), gutter))
		}
		// Truncate on the raw text's terms, not the painted one: cutting a string
		// mid-escape-sequence bleeds colour into the rest of the screen.
		body := painted[i]
		if len([]rune(raw[i]))+gutter+4 > width {
			body = truncate(raw[i], width-gutter-5)
		}
		out = append(out, " "+num+marker+body)
	}
	return out
}

// highlight returns src's lines with terminal colour applied, or the plain
// lines when the language is unknown or anything goes wrong. Highlighting is
// decoration: it must never be the reason code fails to appear.
func highlight(src, filename string) []string {
	plain := strings.Split(src, "\n")

	lexer := lexers.Match(filename)
	if lexer == nil {
		return plain
	}
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		return plain
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		return plain
	}
	var buf strings.Builder
	if err := formatter.Format(&buf, styles.Get("dracula"), it); err != nil {
		return plain
	}
	got := strings.Split(buf.String(), "\n")
	if len(got) != len(plain) {
		return plain
	}
	// Terminate every line so a token left open at a line break cannot colour
	// the UI drawn after it.
	for i := range got {
		got[i] += "\033[0m"
	}
	return got
}

// pad right-aligns a line number in the gutter.
func pad(s string, w int) string {
	for len(s) < w {
		s = " " + s
	}
	return s
}

// shortenPath drops leading directories so the FILENAME always survives - the
// part that identifies the file is the end, not the start.
func shortenPath(path string, max int) string {
	if len(path) <= max || max <= 3 {
		return path
	}
	parts := strings.Split(path, "/")
	// Always keep the basename, even when it alone is too long.
	out := parts[len(parts)-1]
	if len(out)+1 >= max {
		return "…/" + out
	}
	for i := len(parts) - 2; i >= 0; i-- {
		cand := parts[i] + "/" + out
		if len(cand)+2 > max {
			return "…/" + out
		}
		out = cand
	}
	return out
}

// renderHunk draws GitHub's diff hunk as a diff.
//
// Fixed structure per line: a rail, a right-aligned line number, a sign, then
// the source. Added and removed lines get a foreground AND a background wash
// spanning the full content width - the wash is what makes it read as a diff
// rather than as source code that happens to be coloured, which is what it was
// when + and - differed only in one glyph.
//
// A removed line carries no new-file number, because it does not have one.
func renderHunk(hunk, filename string, anchor, cw int) []string {
	if hunk == "" {
		return nil
	}
	type row struct {
		num  int
		text string
		kind byte // '@' header, '+' added, '-' removed, ' ' context
	}
	var rows []row
	newNo := 0
	for _, ln := range strings.Split(hunk, "\n") {
		switch {
		case strings.HasPrefix(ln, "@@"):
			newNo = hunkStart(ln)
			rows = append(rows, row{0, ln, '@'})
		case strings.HasPrefix(ln, "+"):
			rows = append(rows, row{newNo, ln[1:], '+'})
			newNo++
		case strings.HasPrefix(ln, "-"):
			rows = append(rows, row{0, ln[1:], '-'})
		default:
			rows = append(rows, row{newNo, strings.TrimPrefix(ln, " "), ' '})
			newNo++
		}
	}

	// The rail, a five-column number and a two-column sign, so the source starts
	// on the same column on every line whatever its number or kind.
	const railW, numW, signW = 3, 5, 3
	srcW := cw - railW - numW - signW
	if srcW < 8 {
		srcW = 8
	}

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.kind == '@' {
			out = append(out, stDiffRail.Render(" ┆ ")+stDiffHunk.Render(truncate(r.text, cw-railW)))
			continue
		}
		num, sign, sty := "", "   ", stDiffCtx
		switch r.kind {
		case '+':
			num, sign, sty = fmt.Sprint(r.num), " + ", stDiffAdd
		case '-':
			sign, sty = " - ", stDiffDel // no new-file number: it has none
		default:
			num = fmt.Sprint(r.num)
		}
		numSty := stDiffNum
		if r.num == anchor && r.num > 0 {
			// The commented line outranks the diff marker - it is what the
			// reviewer is actually talking about.
			numSty = stDiffNum.Bold(true).Foreground(amberC)
		}
		body := sty.Render(padRight(truncate(r.text, srcW), srcW))
		out = append(out, stDiffRail.Render(" ┆ ")+
			numSty.Render(padLeft(num, numW))+sty.Render(sign)+body)
	}
	return out
}

// hunkStart reads the new-file starting line number off an @@ header.
func hunkStart(header string) int {
	for _, f := range strings.Fields(header) {
		if strings.HasPrefix(f, "+") {
			var n int
			fmt.Sscanf(f, "+%d", &n)
			return n
		}
	}
	return 0
}
