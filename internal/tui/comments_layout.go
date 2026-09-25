// How the screen divides the terminal between its list and its detail pane.
//
// The arithmetic is exact rather than approximate: being one line over does not
// wrap, it pushes the instruction line and the footer off the bottom of the
// terminal. Split out of comments_view.go, which composes what these numbers
// size.
package tui

import (
	"fmt"
	"strings"
)

// listChrome is every line that is neither a comment row nor the detail pane:
// the two header lines and their rule, the blank under them, the blank and the
// three lines of the bordered button, the blank under that, and the frame's
// separator and footer.
const listChrome = 11

// minPane is the least the detail pane is worth drawing: its fixed lines plus
// one line of the reviewer's comment.
const minPane = paneFixed + 1

// listHeight is how many comment rows fit on screen.
//
// The list scrolls when there are more comments than lines. A PR with forty
// review threads is normal, and drawing forty rows pushed the button, the pane
// and the footer off the bottom of the terminal.
func (m monitorModel) listHeight() int {
	used := listChrome + minPane
	if m.hasAllRow() {
		used++
	}
	if m.notice != "" {
		used++
	}
	h := m.height - used
	if h < 1 {
		h = 1
	}
	if n := len(m.visible()); h > n {
		h = n
	}
	return h
}

// listWindow is the slice of comments on screen, scrolled to keep the cursor in
// view and anchored so the last comment can reach the bottom of the window.
func (m monitorModel) listWindow() (start, end int) {
	n, h := len(m.visible()), m.listHeight()
	if n <= h {
		return 0, n
	}
	cur := m.comments.cursor
	if cur < 0 {
		cur = 0
	}
	if cur >= n {
		cur = n - 1
	}
	start = cur - h/2
	if start < 0 {
		start = 0
	}
	if start+h > n {
		start = n - h
	}
	return start, start + h
}

// paneBudget is how many lines the detail pane has to work with: the terminal,
// less the chrome and the rows the list is actually drawing.
func (m monitorModel) paneBudget() int {
	used := listChrome + m.listHeight()
	if m.hasAllRow() {
		used++
	}
	if m.notice != "" {
		used++
	}
	return m.height - used
}

// paneFixed is what the pane spends on everything that is not the code window
// or the quote: the head and the blank under it, the blank above the quote, the
// quote's own head, and the blank and the instruction line.
const paneFixed = 6

// paneCodeRows is the code window, cut to what the pane can afford.
//
// When the terminal is short the code is what goes. The reviewer's words and
// the instruction you are about to give the agent are the two things the screen
// exists for; the diff is context.
func (m monitorModel) paneCodeRows(cw int) []string {
	rows := m.codeRows(cw)
	if len(rows) == 0 {
		rows = []string{stChrome.Render("no diff available for this comment")}
	}
	room := m.paneBudget() - paneFixed - 1
	if room <= 0 {
		return nil // the quote and the instruction line outrank the code
	}
	if len(rows) <= room {
		return rows
	}
	// The window centers on the ANCHOR, not the top: GitHub's hunks END at the
	// commented line, so cutting the tail hid exactly the line the whole pane
	// exists to show. The anchor is found by its line number in the gutter -
	// the restyle marks it with colour alone, which no text search can see -
	// and defaults to the last row, which is where a GitHub hunk anchors.
	anchor := len(rows) - 1
	if c := m.focused(); c != nil && c.Line > 0 {
		want := fmt.Sprint(c.Line)
		for i, r := range rows {
			if f := strings.Fields(stripAnsiStr(r)); len(f) >= 2 && f[0] == "┆" && f[1] == want {
				anchor = i
				break
			}
		}
	}
	start := anchor - room/2
	if start > len(rows)-room {
		start = len(rows) - room
	}
	if start < 0 {
		start = 0
	}
	out := append([]string{}, rows[start:start+room]...)
	// The edge indicators trade a content row each; a window too small to
	// afford them shows pure content (the anchor) and nothing else.
	if start > 0 && len(out) > 2 {
		out[0] = "  " + stChrome.Render(fmt.Sprintf("… %d line(s) above", start))
	}
	if rest := len(rows) - (start + room); rest > 0 && len(out) > 2 {
		out[len(out)-1] = "  " + stChrome.Render(fmt.Sprintf("… %d line(s) below - \"See the whole diff\" in the menu", rest))
	}
	return out
}

// paneQuote splits what is left of the pane between the reviewer's comment and
// the "there is more of it" hint, and is the single place those two numbers are
// decided - drawing a hint the budget did not allow for pushes the instruction
// line off the bottom of the screen.
func (m monitorModel) paneQuote() (lines int, hint bool) {
	avail := m.paneBudget() - paneFixed - len(m.paneCodeRows(m.paneWidth()))
	if avail < 1 {
		avail = 1
	}
	if avail > 1 && len(m.comments.lines) > avail {
		return avail - 1, true
	}
	return avail, false
}

// bodyHeight is how many rows of the reviewer's comment the pane can show. It
// is the screen's elastic quantity: everything else is a fixed line count.
func (m monitorModel) bodyHeight() int {
	n, _ := m.paneQuote()
	return n
}

func (m *monitorModel) clampCommentScroll() {
	maxScroll := len(m.comments.lines) - m.bodyHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.comments.scroll > maxScroll {
		m.comments.scroll = maxScroll
	}
	if m.comments.scroll < 0 {
		m.comments.scroll = 0
	}
}

// quoteWidth is the wrap width for the reviewer's own words: the content width,
// less the quote's spine and the gap after it.
//
// Not paneWidth - that is the plan viewer's, and it is the whole terminal less
// two. Wrapping the quote at it was already spilling the reviewer's words past
// the right margin, and once the content width follows the terminal it would
// have spilled by the full margin on every screen.
func (m monitorModel) quoteWidth() int {
	w := m.width
	if w == 0 {
		w = 80
	}
	_, cw := layout(w)
	if cw -= quoteIndent; cw < 1 {
		cw = 1
	}
	return cw
}

// quoteIndent is the spine and the gap after it, on every quoted line.
const quoteIndent = 3

// commentsFooter insets a footer hint to the screen's own margin, so the keys
// line up under the content they apply to.
func (m monitorModel) commentsFooter(w int, hint string) string {
	margin, cw := layout(w)
	return strings.Repeat(" ", margin) + stChrome.Render(truncate(hint, cw))
}
