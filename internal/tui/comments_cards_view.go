// Rendering the card screen: one fix - quote, diff, reply, actions - or the
// ship card, or the full-screen diff viewer. Split from comments_cards.go,
// which owns the state and keys.
package tui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// renderCards is the whole card screen at a terminal width.
func (m monitorModel) renderCards(w int) string {
	if tooNarrow(w) {
		return narrowNotice(w)
	}
	margin, cw := layout(w)
	if m.comments.cardMode == cardDiff {
		return indent(m.renderCardDiffViewer(cw), margin)
	}
	fixes := m.cardFixes()
	var b strings.Builder
	if m.comments.cardIdx >= len(fixes) {
		b.WriteString(m.renderShipCard(cw))
	} else {
		b.WriteString(m.renderFixCard(cw))
	}
	return indent(b.String(), margin)
}

// cardHeader names the card and reassures - the reassurance drops below 96
// columns, the counter never does. reassure overrides the default line for
// cards where "nothing is posted until the last card" would be a lie (the
// answer card's primary posts immediately).
func (m monitorModel) cardHeader(cw int, what string) string {
	return m.cardHeaderWith(cw, what, "nothing is committed or posted until the last card")
}

func (m monitorModel) cardHeaderWith(cw int, what, reassure string) string {
	left := stMeta.Render("apple pie · "+m.comments.prLabel+" · ") + stAccent.Render(what)
	right := ""
	if cw >= 96 {
		right = stChrome.Render(reassure)
	}
	return padBetween(left, right, cw) + "\n" + rule(cw) + "\n"
}

// renderFixCard is one fix: the comment, what changed, the reply, the actions.
func (m monitorModel) renderFixCard(cw int) string {
	fixes := m.cardFixes()
	c := fixes[m.comments.cardIdx]
	decided := m.comments.decisions[c.ID]
	declined := c.DraftReply == "" && strings.TrimSpace(c.AgentNote) != ""

	counter := fmt.Sprintf("fix %d of %d", m.comments.cardIdx+1, len(fixes))
	if len(fixes) == 1 {
		counter = "approve the fix"
	}
	switch decided {
	case cardOK:
		counter += " · approved"
	case cardSkip:
		counter += " · skipped"
	case cardRedo:
		counter += " · sent back"
	}
	var b strings.Builder
	if declined && c.Kind == review.KindThread {
		b.WriteString(m.cardHeaderWith(cw, counter, "the answer posts from this card - code ships only from the last one"))
	} else {
		b.WriteString(m.cardHeader(cw, counter))
	}
	b.WriteString("\n")

	// The reviewer's own words, behind the same bars as the review screen:
	// amber for a human, cyan for a bot.
	bar := stAmber
	if c.Bot {
		bar = stCyan
	}
	head := displayAuthor(c)
	if !c.FirstSeenAt.IsZero() {
		head += " · " + Age(c.FirstSeenAt) + " ago"
	}
	if tag, _ := severity(c.Gist()); tag != "" {
		head += " · " + tag
	}
	b.WriteString(bar.Render("│ ") + bar.Bold(true).Render(truncate(head, cw-2)) + "\n")
	_, body := severity(c.Body)
	for _, ln := range wrapWords(body, cw-2) {
		b.WriteString(bar.Render("│ ") + stTitle.Render(ln) + "\n")
	}
	if m.comments.reworked[c.ID] {
		note := "reworked just now"
		if n := m.comments.redoNotes[c.ID]; n != "" {
			note += " — your note: " + truncate(n, 60)
		}
		b.WriteString(stChrome.Render(truncate(note, cw)) + "\n")
	}

	// The reply (or the decline) sits with the decision; the diff goes last so
	// it can take every remaining line and scroll in place.
	if declined {
		b.WriteString("\n" + stMeta.Render(letterSpace("THE AGENT DECLINED")) + "\n")
		if m.comments.cardMode == cardReply {
			// Editing the answer in place - it stays the agent's note, so the
			// card remains an answer card and the primary posts the new wording.
			for _, ln := range wrapWords(m.comments.nb.render()+"▏", cw-2) {
				b.WriteString(stRed.Render("│ ") + stBodySel.Render(ln) + "\n")
			}
			b.WriteString(stChrome.Render("type to edit   enter save   esc cancel") + "\n")
			return b.String()
		}
		for _, ln := range wrapWords(c.AgentNote, cw-2) {
			b.WriteString(stRed.Render("│ ") + stTitle.Render(ln) + "\n")
		}
	} else {
		b.WriteString("\n" + stMeta.Render(letterSpace("REPLY TO "+strings.ToUpper(displayAuthor(c)))) + "\n")
		if m.comments.cardMode == cardReply {
			for _, ln := range wrapWords(m.comments.nb.render(), cw-2) {
				b.WriteString(stAccent.Render("│ ") + stBodySel.Render(ln) + "\n")
			}
			b.WriteString(stChrome.Render("type to edit   enter save   esc cancel") + "\n")
			return b.String()
		}
		reply := c.DraftReply
		if reply == "" {
			reply = "(no reply drafted - nothing will be posted on this thread)"
		}
		for _, ln := range wrapWords(reply, cw-2) {
			b.WriteString(stAccent.Render("│ ") + stTitle.Render(ln) + "\n")
		}
	}
	if m.comments.cardMode == cardResolve {
		// The reply about to post, editable in place. This block also serves the
		// declined primary, which arrives here pre-filled with the agent's answer.
		hint := "type to edit   enter post & resolve   esc cancel"
		if m.noResolve {
			hint = "type to edit   enter post   esc cancel"
		}
		b.WriteString("\n" + stMeta.Render(letterSpace("YOUR REPLY - IT POSTS NOW, NO AGENT")) + "\n")
		for _, ln := range wrapWords(m.comments.nb.render()+"▏", cw-2) {
			b.WriteString(stAccent.Render("│ ") + stBodySel.Render(ln) + "\n")
		}
		b.WriteString(stChrome.Render(hint) + "\n")
		return b.String()
	}
	if m.comments.cardMode == cardAsk {
		b.WriteString("\n" + stMeta.Render(letterSpace("WHAT SHOULD IT DO INSTEAD")) + "\n")
		for _, ln := range wrapWords(m.comments.nb.render()+"▏", cw-2) {
			b.WriteString(stAmber.Render("│ ") + stBodySel.Render(ln) + "\n")
		}
		b.WriteString(stChrome.Render("type your note   enter send it back   esc cancel") + "\n")
		return b.String()
	}

	b.WriteString("\n" + rule(cw) + "\n\n")
	b.WriteString(m.renderCardActions(c, decided, declined, cw))

	// The diff, last and elastic: it takes every line the card has left, and
	// scrolls in place - PgUp/PgDn or the mouse wheel - so reading a long fix
	// needs no mode at all. ↑↓ still mean the action rows and nothing else.
	b.WriteString("\n" + stMeta.Render(letterSpace("WHAT CHANGED")) + "\n")
	rows, budget := m.cardDiffWindowSpec(&c, cw)
	scroll := m.comments.diffScroll
	if maxS := len(rows) - budget; scroll > maxS {
		scroll = max(0, maxS)
	}
	end := scroll + budget
	if end > len(rows) {
		end = len(rows)
	}
	if len(rows) > budget {
		end-- // the position line below takes the window's last row
	}
	for _, ln := range rows[scroll:end] {
		b.WriteString(ln + "\n")
	}
	if len(rows) > budget {
		b.WriteString("  " + cmtDimSty.Render(fmt.Sprintf(
			"↕ %d–%d of %d · PgUp/PgDn scrolls · \"See the whole diff\" for full screen",
			scroll+1, end, len(rows))) + "\n")
	}
	ok, skip, _, left := m.cardTally()
	tally := ""
	if cw >= 100 && len(fixes) > 1 {
		tally = fmt.Sprintf("approved %d · skipped %d · left %d", ok, skip, left)
	}
	keys := "↑↓ move   enter choose   esc dashboard"
	if len(fixes) > 1 {
		keys = "↑↓ move   enter choose   ←→ other fixes   esc dashboard"
	}
	b.WriteString(rule(cw) + "\n")
	b.WriteString(padBetween(stChrome.Render(truncate(keys, cw)), stChrome.Render(tally), cw) + "\n")
	return b.String()
}

// renderCardActions is the primary button and the action rows below it.
func (m monitorModel) renderCardActions(c store.Comment, decided string, declined bool, cw int) string {
	fixes := m.cardFixes()
	label, meta := "APPROVE THIS FIX", ""
	// A declined thread still has a deliverable - the agent's answer - and the
	// primary posts it. A declined review body has no reply target on GitHub,
	// so only there does the button stay inert.
	answerable := declined && c.Kind == review.KindThread
	switch {
	case decided == cardOK:
		label, meta = "UNDO — PUT IT BACK TO UNDECIDED", "it will not ship"
	case decided == cardRedo:
		label, meta = "APPROVE THIS FIX", "waiting on the agent"
	case answerable:
		label, meta = "POST THE ANSWER & RESOLVE", "no code change - replies with the agent's answer, right now"
		if m.noResolve {
			label = "POST THE ANSWER"
		}
	case declined:
		meta = "nothing was fixed - ask again or skip"
	case len(fixes) == 1:
		label, meta = "APPROVE AND SHIP", "verify · commit · push · post the reply"
	}
	var b strings.Builder
	b.WriteString(m.renderCardButton(label, meta, cw, m.comments.cardSel == 0,
		decided == cardRedo || (declined && !answerable)))
	replyHint := "answer it yourself, no agent"
	if m.comments.posting[c.ID] {
		replyHint = "posting…"
	}
	acts := []struct{ l, h string }{
		{m.replyResolveLabel(), replyHint},
		{"Ask for a different fix", "say what's wrong and the agent redoes it"},
		{"Edit the reply", "change the wording before it posts"},
		{"Skip this one", "leave the comment unanswered"},
	}
	for i, a := range acts {
		b.WriteString(m.renderCardRow(a.l, a.h, cw, m.comments.cardSel == i+1))
	}
	if m.cardDiffTruncated() {
		b.WriteString(m.renderCardRow("See the whole diff", "full screen, ↑↓ scrolls, esc returns",
			cw, m.comments.cardSel == 5))
	}
	return b.String()
}

// renderShipCard is the only place anything leaves the machine, with the full
// inventory of what is about to happen above the one button that does it.
func (m monitorModel) renderShipCard(cw int) string {
	ok, skip, redo, left := m.cardTally()
	var b strings.Builder
	b.WriteString(m.cardHeader(cw, "ready to ship"))
	b.WriteString("\n")
	line := stAccent.Render(plural(ok, "fix") + " approved")
	if skip > 0 {
		line += stMeta.Render(fmt.Sprintf("  ·  %d skipped", skip))
	}
	if redo > 0 {
		line += stAmber.Render(fmt.Sprintf("  ·  %d out with the agent", redo))
	}
	if left > 0 {
		line += stAmber.Render(fmt.Sprintf("  ·  %d still undecided", left))
	}
	b.WriteString(line + "\n\n")
	for _, c := range m.cardFixes() {
		if m.comments.decisions[c.ID] == cardOK {
			b.WriteString(stTitle.Render(truncate(locationOf(c), cw-24)) +
				stChrome.Render("   reply to "+displayAuthor(c)) + "\n")
		}
	}
	b.WriteString("\n" + rule(cw) + "\n\n")

	blocked := left > 0 || redo > 0
	label, meta := "SHIP IT", ""
	if s := m.sessionByTicket(m.comments.ticket); s != nil && s.Branch != "" {
		meta = fmt.Sprintf("verify · commit · push to %s · post %s", s.Branch, plural(ok, "reply"))
	} else {
		meta = fmt.Sprintf("verify · commit · push · post %s", plural(ok, "reply"))
	}
	switch {
	case blocked:
		label = "CAN'T SHIP YET"
		meta = plural(left+redo, "fix") + " still undecided or out with the agent"
	case ok == 0:
		// Every fix was skipped: nothing to verify/commit/push/post, so the
		// button must say what it actually does - release the ticket back to
		// review - not the ship copy above, which would be a lie here.
		label = "RELEASE (nothing approved)"
		meta = "clears the fix queue · back to review · nothing is shipped"
	}
	b.WriteString(m.renderCardButton(label, meta, cw, m.comments.cardSel == 0, blocked))
	if blocked {
		b.WriteString(m.renderCardRow("Go to the first undecided fix",
			fmt.Sprintf("there are %d left", left), cw, m.comments.cardSel == 1))
	} else {
		b.WriteString(m.renderCardRow("Go back and re-check", "start again at fix 1", cw, m.comments.cardSel == 1))
	}
	b.WriteString("\n" + rule(cw) + "\n")
	b.WriteString(stChrome.Render(truncate("↑↓ move   enter choose   ←→ other fixes   esc dashboard", cw)) + "\n")
	return b.String()
}

// renderCardButton is the one bordered element on any card.
func (m monitorModel) renderCardButton(label, meta string, cw int, on, off bool) string {
	boxW := cw - 2
	inner := boxW - 2*GutterX
	if cw < 92 {
		meta = ""
	}
	gap := inner - lipWidth(label) - lipWidth(meta)
	if gap < 2 {
		meta = truncate(meta, max(0, inner-lipWidth(label)-3))
		gap = max(0, inner-lipWidth(label)-lipWidth(meta))
	}
	text := label + strings.Repeat(" ", gap) + meta
	switch {
	case off && on:
		return stButtonOff.BorderForeground(amberC).Foreground(amberC).Width(boxW).Render(text) + "\n"
	case off:
		return stButtonOff.Width(boxW).Render(text) + "\n"
	case on:
		return stButtonFocus.Width(boxW).Render(text) + "\n"
	default:
		return stButton.Width(boxW).Render(text) + "\n"
	}
}

// renderCardRow is one action row: cursor, verb, consequence.
func (m monitorModel) renderCardRow(label, hint string, cw int, on bool) string {
	mark, sty := " ", stTitle
	if on {
		mark, sty = "▸", stFocus
	}
	verbW := min(30, max(16, cw/3))
	hintW := cw - dashCursorW - verbW - dashGapW
	cells := []string{
		cell(stAccent, on, dashCursorW, lipgloss.Left, mark),
		cell(sty, on, verbW+dashGapW, lipgloss.Left, truncate(label, verbW)),
	}
	if hintW > 4 && cw >= 88 {
		cells = append(cells, cell(stChrome, on, hintW, lipgloss.Left, truncate(hint, hintW)))
	}
	return band(on, cw, cells...) + "\n"
}

// cardDiffRows is the fix's whole diff, uncapped - the card windows it itself.
func (m monitorModel) cardDiffRows(c *store.Comment, cw int) ([]string, bool) {
	m.height = 1 << 20 // value receiver: lifts approveDiffRows' cap locally
	rows := m.approveDiffRows(c, cw)
	return rows, false
}

// cardDiffWindowSpec is the whole diff plus how many lines of it the card can
// show: the terminal, less every fixed line the card draws around it.
func (m monitorModel) cardDiffWindowSpec(c *store.Comment, cw int) ([]string, int) {
	rows, _ := m.cardDiffRows(c, cw)
	quote := len(wrapWords(c.Body, cw-2))
	reply := c.DraftReply
	if reply == "" {
		reply = c.AgentNote
	}
	replyLines := len(wrapWords(reply, cw-2))
	acts := 3
	if m.cardDiffTruncated() {
		acts = 4
	}
	budget := m.height - quote - replyLines - acts - 20
	if budget < 5 {
		budget = 5
	}
	return rows, budget
}

// cardDiffTruncated is whether the diff outgrows the card's window - which is
// what puts "See the whole diff" on the action list.
func (m monitorModel) cardDiffTruncated() bool {
	c := m.cardCurrent()
	if c == nil {
		return false
	}
	cw := contentWidthFor(m.width)
	rows, _ := m.cardDiffRows(c, cw)
	quote := len(wrapWords(c.Body, cw-2))
	reply := c.DraftReply
	if reply == "" {
		reply = c.AgentNote
	}
	budget := m.height - quote - len(wrapWords(reply, cw-2)) - 23
	if budget < 5 {
		budget = 5
	}
	return len(rows) > budget
}

// renderCardDiffViewer is the full-screen diff: ↑↓ scrolls, esc returns. The
// card itself never scrolls - that would give the arrows two meanings on one
// screen, which is the mistake this design keeps undoing.
func (m monitorModel) renderCardDiffViewer(cw int) string {
	c := m.cardCurrent()
	if c == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(m.cardHeader(cw, "the whole diff"))
	// Uncapped: rebuild the rows with an effectively unlimited budget by
	// rendering each file in full.
	saved := m.height
	m.height = 1 << 20
	rows, _ := m.cardDiffRows(c, cw)
	m.height = saved

	view := m.height - 6
	if view < 4 {
		view = 4
	}
	maxScroll := len(rows) - view
	if maxScroll < 0 {
		maxScroll = 0
	}
	scroll := m.comments.diffScroll
	if scroll > maxScroll {
		scroll = maxScroll
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
		"↑↓ scroll   esc back to the card   %d–%d of %d", scroll+1, end, len(rows)), cw)) + "\n")
	return b.String()
}

// contentWidthFor is layout()'s content for a raw terminal width.
func contentWidthFor(w int) int {
	if w == 0 {
		w = 80
	}
	_, cw := layout(w)
	return cw
}

// stripAnsiRe strips styling for render-side text inspection.
var stripAnsiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripAnsiStr(s string) string { return stripAnsiRe.ReplaceAllString(s, "") }
