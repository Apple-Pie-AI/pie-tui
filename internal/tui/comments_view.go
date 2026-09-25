// Composing the triage screen: header, list, run button, detail pane.
//
// Everything is drawn at the content width and then inset by the margin, so the
// screen never stretches to the terminal. Blank lines are the rhythm: exactly
// one between the header and the list, the list and the button, the button and
// the pane. None between comment rows - the list is one block, and a gap
// between rows turns eight comments into eight islands.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// The row's fixed columns, in display cells: the checkbox and its gap, the
// cursor mark and its gap, and the gap that keeps prose off the author's name.
const (
	colBox  = 4
	colMark = 2
	colGap  = 2
)

func (m monitorModel) renderComments(w int) string {
	switch m.phase() {
	case phaseRunning:
		// A revise run spawned from the cards leaves the user deciding the
		// rest of the batch; only a fix run they are not part of gets the
		// frozen log screen.
		if len(m.comments.decisions) > 0 {
			return m.renderCards(w)
		}
		return m.renderRunning(w)
	case phaseApprove:
		return m.renderCards(w)
	case phaseDone:
		return m.renderDone(w)
	}
	margin, cw := layout(w)
	if m.comments.cardMode == cardDiff {
		return indent(m.renderTriageDiffViewer(cw), margin)
	}

	var b strings.Builder
	b.WriteString(m.renderCommentsHeader(cw))
	b.WriteString("\n")
	b.WriteString(m.renderAllRow(cw))
	b.WriteString(m.renderCommentList(cw))
	b.WriteString("\n")
	b.WriteString(m.renderRunButton(cw))
	b.WriteString("\n")
	b.WriteString(m.renderDetail(cw))
	return indent(b.String(), margin)
}

// indent insets every line by the margin. Applied once, at the end, so no
// component has to know where on the terminal it sits.
func indent(s string, margin int) string {
	if margin <= 0 {
		return s
	}
	pad := strings.Repeat(" ", margin)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// renderCommentsHeader is the brand, what screen this is, and the sync state,
// then one dim line of context and a rule. Nothing in it competes with the run
// button, which is the only thing on the screen allowed to shout.
func (m monitorModel) renderCommentsHeader(cw int) string {
	left := stBrand.Render(letterSpace("APPLE PIE")) + "   " + stMeta.Render("review comments")
	right := stChrome.Render("● " + m.syncLabel() + " · " + m.lastRefresh.Format("15:04"))

	// The second line is the context that makes the first trustworthy: which PR,
	// which branch, and how much review is actually on it.
	ctx := stTitle.Render(m.comments.prLabel)
	if s := m.sessionByTicket(m.comments.ticket); s != nil && s.Branch != "" {
		ctx += stMeta.Render(" · " + s.Branch)
	}
	if n := len(m.visible()); n > 0 {
		ctx += stMeta.Render(fmt.Sprintf(" · %s from %s",
			plural(n, "comment"), plural(m.reviewerCount(), "reviewer")))
	}
	return padBetween(left, right, cw) + "\n" + truncate(ctx, cw) + "\n" + rule(cw) + "\n"
}

// reviewerCount is how many distinct people (and bots) are on this review.
func (m monitorModel) reviewerCount() int {
	seen := map[string]bool{}
	for _, c := range m.visible() {
		seen[c.Author] = true
	}
	return len(seen)
}

// padBetween lays two rendered segments against opposite edges of the content
// width, measured on display cells so styling never shifts the right one.
func padBetween(left, right string, cw int) string {
	pad := cw - lipWidth(left) - lipWidth(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

// syncLabel states where the GitHub fetch stands. Fetching is automatic, so
// this is the whole of its interface: nothing to press, only something to read.
func (m monitorModel) syncLabel() string {
	switch {
	case m.comments.fetching:
		return "syncing…"
	case m.comments.syncFailed:
		return "sync failed — retrying"
	case m.comments.lastFetch.IsZero():
		return "syncing…"
	default:
		return "synced " + Age(m.comments.lastFetch) + " ago"
	}
}

// commentCols solves the row's flexible columns for a content width.
//
// Shrinking, the body gives up space first: it is already truncated prose and
// degrades gracefully. Then the author, and the location shrinks last and never
// below 18 - a row whose file and line you cannot read is a row you cannot act
// on. Growing, that order reverses.
func commentCols(cw int) (loc, body, auth int) {
	loc, auth = 22, 13
	const fixed = colBox + colMark + colGap
	const minBody, minAuth, minLoc = 24, 8, 18
	// What the location and the author are worth at full length, and the point
	// past which the body stops needing more.
	const maxLoc, maxAuth, fullBody = 34, 20, 80

	body = cw - fixed - loc - auth
	if body >= minBody {
		// A wide terminal is surplus. Handing all of it to the body leaves a
		// 130-column excerpt next to a path truncated at 22 and an author cut to
		// "github-copil…", so the two short columns take their fill first.
		if grow := min(body-fullBody, maxLoc-loc); grow > 0 {
			loc, body = loc+grow, body-grow
		}
		if grow := min(body-fullBody, maxAuth-auth); grow > 0 {
			auth, body = auth+grow, body-grow
		}
		return loc, body, auth
	}
	if need := minBody - body; need > 0 {
		take := min(need, auth-minAuth)
		auth, body = auth-take, body+take
	}
	if need := minBody - body; need > 0 {
		take := min(need, loc-minLoc)
		loc, body = loc-take, body+take
	}
	if body < 1 {
		body = 1
	}
	return loc, body, auth
}

// renderAllRow is the aggregate checkbox: the one bulk control on the screen,
// and a row rather than a menu item so it costs one line and no new vocabulary.
func (m monitorModel) renderAllRow(cw int) string {
	if !m.hasAllRow() {
		return ""
	}
	vis := m.visible()
	n := len(m.selectedIDs())
	cursor := m.onAllRow()

	// A partial selection is neither [x] nor [ ]; saying [ ] would claim nothing
	// is checked while the button counts six.
	box := "[ ]"
	switch {
	case m.allChecked():
		box = "[x]"
	case n > 0:
		box = "[~]"
	}
	locW, bodyW, authW := commentCols(cw)
	boxSty := stMeta
	if n > 0 {
		boxSty = stAccent
	}
	metaW := bodyW + colGap + authW
	return band(cursor, cw,
		cell(boxSty, cursor, colBox, lipgloss.Left, box),
		cell(stChrome, cursor, colMark, lipgloss.Left, "▸"),
		cell(rowLabelStyle(cursor), cursor, locW, lipgloss.Left,
			truncate(fmt.Sprintf("all comments (%d)", len(vis)), locW)),
		cell(stMeta, cursor, metaW, lipgloss.Right,
			truncate(fmt.Sprintf("%d checked · %d skipped", n, len(vis)-n), metaW)),
	) + "\n"
}

// renderCommentList draws the comments on consecutive lines - no headings and
// no blanks, so the list reads as one block.
func (m monitorModel) renderCommentList(cw int) string {
	vis := m.visible()
	if len(vis) == 0 {
		return stChrome.Render("no unresolved review comments on this PR") + "\n"
	}
	var b strings.Builder
	start, end := m.listWindow()
	for i, c := range vis[start:end] {
		b.WriteString(m.renderCommentRow(c, cw, start+i == m.comments.cursor) + "\n")
	}
	return b.String()
}

// renderRunButton is the primary action, and the only bordered or filled thing
// on the screen - which is what makes it unmistakably the primary action.
//
// Its label carries the live count and the consequence, so the screen can run
// without a confirmation dialog: the thing you press already says exactly what
// it is about to do. At zero checked it goes inert.
func (m monitorModel) renderRunButton(cw int) string {
	if len(m.visible()) == 0 {
		return ""
	}
	n := len(m.selectedIDs())
	// Lip Gloss counts padding inside Width, so the box is set to the content
	// width less its border and the label is sized to what is left after the
	// padding. Getting this wrong wraps the button onto a second line.
	boxW := cw - 2
	inner := boxW - 2*GutterX

	label, meta := "", ""
	switch {
	case m.phase() == phaseDone:
		label = "▶  BACK TO THE DASHBOARD"
	case n == 0:
		label, meta = "▶  NOTHING TO FIX", "press enter on a comment to check it"
	default:
		// "AND PUSH" was the truth once; since the approval gate it is a lie -
		// the run fixes locally and parks at the cards, and nothing commits,
		// pushes or posts until SHIP IT on the last card.
		label = fmt.Sprintf("▶  FIX %s AND PREVIEW THE FIXES", strings.ToUpper(plural(n, "comment")))
		meta = fmt.Sprintf("%d checked · nothing ships until you approve", n)
	}
	gap := inner - lipWidth(label) - lipWidth(meta)
	if gap < 2 {
		meta = truncate(meta, max(0, inner-lipWidth(label)-3))
		gap = inner - lipWidth(label) - lipWidth(meta)
	}
	if gap < 0 {
		gap = 0
	}
	text := label + strings.Repeat(" ", gap) + meta

	switch {
	case n == 0:
		return stButtonOff.Width(boxW).Render(text) + "\n"
	case m.onRunBar():
		return stButtonFocus.Width(boxW).Render(text) + "\n"
	default:
		return stButton.Width(boxW).Render(text) + "\n"
	}
}

// ---- the approve and done screens ------------------------------------------

// locOf is a row's location for the running and done screens, which have no
// severity column to lift a tag into.
func locOf(c store.Comment) string {
	loc := filepath.Base(c.Path)
	if loc == "." || loc == "" {
		loc = "(review summary)"
	} else if c.Line > 0 {
		loc += ":" + fmt.Sprint(c.Line)
	}
	if c.UserNote != "" {
		loc = "✎ " + loc
	}
	return loc
}

// shortSHA abbreviates a commit for a status row.
func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// renderTriageDiffViewer is the whole of the focused comment's code - the real
// file context or GitHub's full hunk - scrollable, with the ▶ anchor line in
// it somewhere instead of cut off below the pane's budget.
func (m monitorModel) renderTriageDiffViewer(cw int) string {
	c := m.focused()
	if c == nil {
		return ""
	}
	var b strings.Builder
	left := stMeta.Render("apple pie · "+m.comments.prLabel+" · ") + stAccent.Render("the whole diff")
	b.WriteString(padBetween(left, stChrome.Render(truncate(locationOf(*c), 40)), cw) + "\n" + rule(cw) + "\n")
	rows := m.codeRows(cw)
	// Say WHICH view this is: GitHub's hunk ends at the commented line - no
	// code below the anchor exists in it - and without the label that read as
	// the viewer being broken rather than the data running out.
	if len(m.comments.code) == 0 {
		note := "GitHub's excerpt - it ends at the commented line; checkout the branch to see the code below"
		rows = append(rows, "", "  "+stChrome.Render(truncate(note, cw-2)))
	}
	view := m.height - 6
	if view < 4 {
		view = 4
	}
	scroll := m.comments.diffScroll
	if maxS := len(rows) - view; scroll > maxS {
		scroll = max(0, maxS)
	}
	end := scroll + view
	if end > len(rows) {
		end = len(rows)
	}
	for _, ln := range rows[scroll:end] {
		b.WriteString(ln + "\n")
	}
	b.WriteString(rule(cw) + "\n")
	b.WriteString(stChrome.Render(truncate(fmt.Sprintf(
		"↑↓/PgUp/PgDn scroll   esc back to the comments   %d–%d of %d", scroll+1, end, len(rows)), cw)) + "\n")
	return b.String()
}
