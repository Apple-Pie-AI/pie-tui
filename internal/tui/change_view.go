// Rendering for the change screen (change.go): the split view (file list,
// diff pane, the always-visible feedback box), the full-screen file view
// with its line-cursor scroll mode, and the box embedded under full-screen
// while composing a line comment. Split from change.go, the
// comments.go/comments_view.go precedent.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m monitorModel) renderChange(w int) string {
	if tooNarrow(w) {
		return narrowNotice(w)
	}
	margin, cw := layout(w)
	var b strings.Builder
	b.WriteString(m.changeHeader(cw))
	switch {
	case m.change.mode == chBox && m.change.boxOrigin.mode == chScroll:
		// Composing a line comment: still the full-screen file view, with the
		// box replacing the normal file-position footer.
		b.WriteString(m.renderChangeFullBox(cw))
	case m.change.mode == chBox && m.change.discussFullScreen():
		// A Plan-mode conversation is under way: the chat, not the diff, is
		// the point of the screen now - see changeState.discussFullScreen.
		b.WriteString(m.renderChangeDiscussFull(cw))
	case m.change.mode == chFull || m.change.mode == chScroll:
		b.WriteString(m.renderChangeFull(cw))
	default:
		b.WriteString(m.renderChangeSplit(cw))
	}
	return indent(b.String(), margin)
}

// changeHeader is the screen's identity line: ticket/title left, worktree +
// round right - and the approve error, when one is stored, on its own red
// line below. The leading "←" is a static glyph naming the back affordance;
// the real binding stays on esc/left as always.
func (m monitorModel) changeHeader(cw int) string {
	c := m.change
	left := stChrome.Render("← ") + stAccent.Render(c.ticket) + stMeta.Render(" · "+truncate(c.summary, 40))
	right := fmt.Sprintf("wt/%s · round %d", truncate(c.branch, 24), max(c.round, 1))
	out := padBetween(left, stChrome.Render(right), cw) + "\n" + rule(cw) + "\n"
	if c.changeErr != "" {
		out += stRed.Render(truncate("✗ last attempt: "+c.changeErr, cw)) + "\n"
	}
	return out
}

// ---- split view ------------------------------------------------------------

func (m monitorModel) renderChangeSplit(cw int) string {
	c := m.change
	leftW := 38
	if cw < 96 {
		leftW = cw / 2
	}
	rightW := cw - leftW - 3

	left := m.changeListLines(leftW)
	right := m.changePaneLines(rightW)
	boxLines := m.renderChangeBox(cw)

	// The pane budget: terminal minus header(2-3) + rule + the box + footer(1).
	reserved := 4 + len(boxLines)
	if c.changeErr != "" {
		reserved++
	}
	budget := m.changeBodyHeight() - reserved
	if budget < 6 {
		budget = 6
	}
	shown := right
	from, to := 0, len(right)
	if len(right) > budget {
		shown = right[:budget]
		to = budget
	}

	var b strings.Builder
	rows := len(left)
	if len(shown) > rows {
		rows = len(shown)
	}
	for i := 0; i < rows && i < budget; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(shown) {
			r = shown[i]
		}
		b.WriteString(padRight(l, leftW) + stChrome.Render(" │ ") + r + "\n")
	}
	b.WriteString(rule(cw) + "\n")
	for _, ln := range boxLines {
		b.WriteString(ln + "\n")
	}
	if c.mode != chBox {
		counter := ""
		if len(right) > budget {
			counter = fmt.Sprintf("showing %d–%d of %d", from+1, to, len(right))
		}
		verb := "—"
		switch {
		case c.fileAt(c.cursor) != nil:
			verb = "comment"
		case c.cursor == c.ctaAt():
			verb = "approve"
		case c.cursor == c.showAllAt():
			verb = "toggle"
		}
		past := "↓ past last row: feedback"
		if c.ctaAt() >= 0 {
			past = "↓ past approve: feedback"
		}
		keys := fmt.Sprintf("↑↓ move   enter %s   → full screen   %s   esc dashboard", verb, past)
		b.WriteString(padBetween(stChrome.Render(keys), stChrome.Render(counter), cw) + "\n")
	}
	return b.String()
}

// changeListLines is the left column: the changed-files summary, Summary,
// the file rows, then [Show all changes], then the amber CTA (with its
// inline confirm expansion when open).
func (m monitorModel) changeListLines(w int) []string {
	c := m.change
	adds, dels := 0, 0
	for _, f := range c.files {
		adds += f.adds
		dels += f.dels
	}
	var out []string
	out = append(out, stMeta.Render(fmt.Sprintf("Changed files  %d  ", len(c.files)))+
		stTitle.Render(fmt.Sprintf("+%d ", adds))+stRed.Render(fmt.Sprintf("−%d", dels)))
	out = append(out, "")

	// listFocused is whether the split list itself currently owns the
	// keyboard - false once Enter/↓ has handed off to the box (chBox) or a
	// file's full-screen view (chFull/chScroll). Every row's "on" state below
	// is gated by it: without this, the row the box was opened FROM (its
	// changeBoxOrigin.cursor never moves) kept rendering as selected at the
	// same time as the box itself, a "two things selected at once" look.
	listFocused := c.mode == chSplit

	// One list row: a mark cell plus a label cell, banded the full column
	// width on selection - the dashboard/comments convention (comments_row.go's
	// cell/band/rowLabelStyle), not a foreground-only highlight hugging the text.
	row := func(i int, text string) {
		on := listFocused && i == c.cursor && !c.approveOpen
		out = append(out, band(on, w,
			cell(stAccent, on, 2, lipgloss.Left, markFor(on)),
			cell(rowLabelStyle(on), on, w-2, lipgloss.Left, truncate(text, w-2)),
		))
	}
	row(0, "PR description")
	for i, f := range c.files {
		name := shortPath(f.path, w-12)
		row(c.fileStart()+i, fmt.Sprintf("%s  %s →", name, f.status))
	}
	out = append(out, "")
	if a := c.showAllAt(); a >= 0 {
		label := "Show all changes"
		if c.showAll {
			label = "Show changes since your feedback"
		}
		row(a, label)
	}

	if !c.needsApprove() {
		// The worktree already matches the open PR exactly - nothing to
		// approve. Say so plainly instead of showing a button that would be
		// a no-op dressed up as an action.
		out = append(out, "  "+stChrome.Render("Up to date with the open PR - nothing to approve"))
	} else {
		ctaText := "Approve and create pull request"
		if c.hasPR() {
			ctaText = "Approve and push updates to the PR"
		}
		ctaOn := listFocused && c.cursor == c.ctaAt() && !c.approveOpen
		switch {
		case ctaOn:
			out = append(out, band(true, w,
				cell(stAccent, true, 2, lipgloss.Left, markFor(true)),
				cell(rowLabelStyle(true), true, w-2, lipgloss.Left, truncate(ctaText, w-2)),
			))
		default:
			out = append(out, "  "+stAmber.Render(ctaText))
		}
	}
	if c.approveOpen {
		confirmLabel := "Create pull request"
		if c.hasPR() {
			confirmLabel = "Push updates"
		}
		for i, label := range []string{confirmLabel, "Cancel"} {
			on := i == c.approveSel
			out = append(out, "  "+band(on, w-2,
				cell(stAccent, on, 2, lipgloss.Left, markFor(on)),
				cell(rowLabelStyle(on), on, w-4, lipgloss.Left, label),
			))
		}
	}
	return out
}

// changePaneLines is the right pane: the focused file's diff from the top,
// or the summary block when the cursor is not on a file.
func (m monitorModel) changePaneLines(w int) []string {
	c := m.change
	if c.diffErr != "" {
		return []string{stRed.Render(truncate("reading the diff: "+c.diffErr, w))}
	}
	if !c.diffLoaded {
		return []string{stChrome.Render("reading the change…")}
	}
	f := c.fileAt(c.cursor)
	if f == nil {
		return m.changeSummaryLines(w)
	}
	head := fmt.Sprintf("%s   %s  +%d −%d", f.path, f.status, f.adds, f.dels)
	out := []string{stTitle.Render(truncate(head, w)), stChrome.Render(strings.Repeat("─", min(w, 60)))}
	return append(out, renderHunk(strings.Join(c.diff[f.path], "\n"), f.path, 0, w)...)
}

// changeSummaryLines is the "PR description" row's pane: the actual text
// this change will ship with - changePRBody resolves the same body whether a
// PR already exists (this IS what got posted) or doesn't yet (this is what
// will be) - plus a 2-segment verify strip. There is no separate "build"
// signal in agent.Report - Verified and Tests are all that exist, so the
// strip is verify ✓/✗ and, when present, the raw Tests string - never a
// fabricated third checkmark.
func (m monitorModel) changeSummaryLines(w int) []string {
	c := m.change
	title := "PR description · not created yet, this is the intended text"
	if c.hasPR() {
		title = "PR description"
	}
	out := []string{stTitle.Render(title)}
	if strings.TrimSpace(c.prBody) == "" {
		out = append(out, stChrome.Render("  no PR description available"))
	} else {
		for _, ln := range strings.Split(c.prBody, "\n") {
			if strings.TrimSpace(ln) == "" {
				out = append(out, "")
				continue
			}
			for _, wrapped := range wrapWords(ln, w-2) {
				out = append(out, "  "+wrapped)
			}
		}
	}
	if r := c.report; r != nil {
		verify := stRed.Render("verify ✗")
		if r.Verified != nil && *r.Verified {
			verify = stTitle.Render("verify ✓")
		}
		strip := "  " + verify
		if strings.TrimSpace(r.Tests) != "" {
			strip += stMeta.Render("  ·  tests: " + truncate(r.Tests, w-20))
		}
		out = append(out, "", strip)
		if len(r.FilesChanged) > 0 {
			out = append(out, "", stMeta.Render("  files the agent reported changing:"))
			for _, p := range r.FilesChanged {
				out = append(out, stChrome.Render("    "+truncate(p, w-4)))
			}
		}
	} else {
		out = append(out, stChrome.Render("  no verify report on file"))
	}
	if c.diffLoaded && len(c.files) == 0 {
		out = append(out, "", stChrome.Render("  no changes to review - the branch matches its base and the worktree is clean."))
		return out
	}
	out = append(out, "", stChrome.Render("  ↓ moves through the files; each shows its diff here."))
	return out
}

// ---- full screen + scroll --------------------------------------------------

func (m monitorModel) renderChangeFull(cw int) string {
	c := m.change
	f := c.files[c.fileIdx]
	rows := renderHunk(strings.Join(c.diff[f.path], "\n"), f.path, 0, cw)
	c.clampYOff(len(rows), m.changeBodyHeight())

	var b strings.Builder
	head := fmt.Sprintf("%s   %s  +%d −%d", f.path, f.status, f.adds, f.dels)
	b.WriteString(padBetween(stTitle.Render(truncate(head, cw-14)),
		stChrome.Render(fmt.Sprintf("file %d/%d", c.fileIdx+1, len(c.files))), cw) + "\n")
	b.WriteString(rule(cw) + "\n")

	budget := m.changeBodyHeight() - 6
	if budget < 5 {
		budget = 5
	}
	from, to := c.yOff, c.yOff+budget
	if to > len(rows) {
		to = len(rows)
	}
	for i := from; i < to; i++ {
		if c.mode == chScroll && i == c.lineCur {
			b.WriteString(band(true, cw, stripAnsiStr(rows[i])) + "\n")
			continue
		}
		b.WriteString(rows[i] + "\n")
	}
	b.WriteString(rule(cw) + "\n")

	var keys string
	if c.mode == chScroll {
		keys = stAmber.Render("SCROLL") + stChrome.Render("   ↑↓ move line   enter comment on line   ← exit scroll")
	} else {
		keys = stChrome.Render("↑↓ prev/next file   enter scroll   ← back")
	}
	pos := stChrome.Render(fmt.Sprintf("lines %d–%d of %d", from+1, to, len(rows)))
	b.WriteString(padBetween(keys, pos, cw) + "\n")
	return b.String()
}

// renderChangeFullBox is the full-screen file view while composing a line
// comment: the same file, a static window of context around the line the
// comment is about, and the box in place of the position footer.
func (m monitorModel) renderChangeFullBox(cw int) string {
	c := m.change
	f := c.files[c.boxOrigin.fileIdx]
	rows := renderHunk(strings.Join(c.diff[f.path], "\n"), f.path, 0, cw)

	var b strings.Builder
	head := fmt.Sprintf("%s   %s  +%d −%d", f.path, f.status, f.adds, f.dels)
	b.WriteString(padBetween(stTitle.Render(truncate(head, cw-14)),
		stChrome.Render(fmt.Sprintf("file %d/%d", c.boxOrigin.fileIdx+1, len(c.files))), cw) + "\n")
	b.WriteString(rule(cw) + "\n")

	boxLines := m.renderChangeBox(cw)
	budget := m.changeBodyHeight() - 6 - len(boxLines)
	if budget < 3 {
		budget = 3
	}
	from := c.boxOrigin.yOff
	if from > len(rows) {
		from = len(rows)
	}
	to := from + budget
	if to > len(rows) {
		to = len(rows)
	}
	for i := from; i < to; i++ {
		if i == c.boxOrigin.lineCur {
			b.WriteString(band(true, cw, stripAnsiStr(rows[i])) + "\n")
			continue
		}
		b.WriteString(rows[i] + "\n")
	}
	b.WriteString(rule(cw) + "\n")
	for _, ln := range boxLines {
		b.WriteString(ln + "\n")
	}
	return b.String()
}

// shortPath keeps the basename readable when the path outgrows the pane.
//
// The boundary case is the one that crashed: when the basename plus the "…/"
// prefix lands EXACTLY at w, len(base)-(w-1) computed to -1 under the old
// >= check (it should have been the <= "fits" case, not the "doesn't fit"
// one) - `s[-1:]` panics rather than returning a value. Fixed by using <=
// for the fits-with-prefix case and clamping the truncated tail's length so
// it can never reach or exceed len(base).
func shortPath(p string, w int) string {
	if w < 8 {
		w = 8
	}
	if len(p) <= w {
		return p
	}
	parts := strings.Split(p, "/")
	base := parts[len(parts)-1]
	if len(base)+2 <= w {
		return "…/" + base
	}
	keep := w - 1
	if keep < 1 {
		keep = 1
	}
	if keep >= len(base) {
		return base
	}
	return "…" + base[len(base)-keep:]
}
