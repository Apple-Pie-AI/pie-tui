// The detail pane: the code a comment points at, the reviewer's own words, the
// instruction you can give the agent about it - and the comment menu, which is
// anchored into the top of this region so the list above never reflows.
//
// Split out of comments_view.go, which renders the list above it - the two
// panes share a screen but not a concern, and together they overran the
// file-size guideline.
package tui

import (
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// ---- detail pane -----------------------------------------------------------

// renderDetail is the code and full comment for the cursor's row, and the
// note input when it is open.
func (m monitorModel) renderDetail(w int) string {
	if m.onAllRow() {
		n := len(m.selectedIDs())
		return stChrome.Render(truncate(fmt.Sprintf(
			"all comments on %s · %d of %d checked - enter checks or unchecks every one",
			m.comments.prLabel, n, len(m.visible())), w)) + "\n"
	}
	if m.onRunBar() {
		return stChrome.Render(truncate(m.runBarExplainer(), w)) + "\n"
	}
	c := m.focused()
	if c == nil {
		return ""
	}
	if m.comments.mode == modeMenu {
		return m.renderMenu(w)
	}
	var b strings.Builder

	// The pane head: the full path with the line, and on the right the mock's
	// badge restating what the checkbox already decided - the decision, next to
	// the code it applies to.
	path := c.Path
	if path == "" {
		path = "overall review comment"
	} else if c.Line > 0 {
		path += fmt.Sprintf(" · line %d", c.Line)
	}
	// The link is measured by its label and emitted as an OSC 8 hyperlink, so a
	// terminal that supports them makes the thread one click away.
	link := shortURL(c.URL)
	b.WriteString(padBetween(
		stMeta.Render(truncate(path, w-lipWidth(link)-2)),
		stChrome.Render(osc8(c.URL, link)), w) + "\n\n")

	for _, ln := range m.paneCodeRows(w) {
		b.WriteString(ln + "\n")
	}
	b.WriteString("\n")

	// The comment sits behind an amber spine - the mock's quote border - so it
	// reads as the reviewer's words rather than the app's own copy. Amber while
	// the comment will be fixed, dim once it is skipped.
	// A bot's quote bar is tinted like its name in the list, so the two readings
	// of "who said this" agree.
	bar, headSty := stAmber, stAmber
	if c.Bot {
		bar, headSty = stCyan, stCyan
	}
	if !m.comments.sel[c.ID] {
		bar, headSty = stChrome, stChrome
	}
	spine := bar.Render("▎")
	quoteHead := displayAuthor(*c)
	if !c.FirstSeenAt.IsZero() {
		quoteHead += " · " + Age(c.FirstSeenAt) + " ago"
	}
	if tag, _ := severity(c.Gist()); tag != "" {
		quoteHead += " · " + tag
	}
	b.WriteString(spine + "  " + headSty.Bold(true).Render(truncate(quoteHead, w-3)) + "\n")
	viewH, hint := m.paneQuote()
	start, end := scrollWindow(len(m.comments.lines), m.comments.scroll, viewH)
	for _, ln := range m.comments.lines[start:end] {
		b.WriteString(spine + "  " + ln + "\n")
	}
	if hint {
		b.WriteString(spine + "  " + stChrome.Render(fmt.Sprintf("↕ PgUp/PgDn for the rest (%d–%d of %d)",
			start+1, end, len(m.comments.lines))) + "\n")
	}

	b.WriteString(m.renderInstruction(w, c))
	return b.String()
}

// renderInstruction is the guidance the agent reads before fixing this one
// comment - and, while it is being written, the field itself, on the very line
// that displays it.
//
// Never called a note: a note reads as something for the reviewer, and this is
// read only by the agent. The label is the one word that says which.
func (m monitorModel) renderInstruction(w int, c *store.Comment) string {
	const label, labW = "your instruction", 18
	if m.comments.mode == modeReplying {
		return "\n" + stAmber.Render(padRight("your reply", labW)) + " " +
			m.comments.nb.render() + "\n"
	}
	if m.comments.mode == modeEditing {
		return "\n" + stAmber.Render(padRight(label, labW)) + " " +
			m.comments.nb.render() + "\n"
	}
	val := stChrome.Render("none — enter opens the menu to write one")
	if c.UserNote != "" {
		val = stTitle.Render(truncate(c.UserNote, w-labW-2))
	}
	return "\n" + stMeta.Render(padRight(label, labW)) + " " + val + "\n"
}

// runBarExplainer says what the run will do, for when the cursor rests there.
func (m monitorModel) runBarExplainer() string {
	n := len(m.selectedIDs())
	if n == 0 {
		return "nothing is selected - open a comment and choose \"include this comment\""
	}
	return fmt.Sprintf("Apple Pie will fix %s in one run, then commit and push to %s.",
		plural(n, "comment"), m.comments.prLabel)
}

// codeRows is the code window: the real file when it could be read, GitHub's
// diff hunk when it could not. Something is always shown.
func (m monitorModel) codeRows(w int) []string {
	c := m.focused()
	if c == nil {
		return nil
	}
	if len(m.comments.code) > 0 {
		return renderCode(m.comments.code, c.Path, w)
	}
	out := renderHunk(strings.TrimSpace(c.DiffHunk), c.Path, c.Line, w)
	if len(out) == 0 {
		return nil
	}
	// The hunk is GitHub's excerpt and ends AT the commented line - there is
	// no code below the anchor in it. Say so, or the pane reads as truncated.
	return append(out, "  "+stChrome.Render(truncate(
		"(GitHub's excerpt - checkout the branch to see the code below this line)", w-2)))
}
