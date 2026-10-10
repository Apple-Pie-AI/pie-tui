// The dashboard: triaging sessions into groups, and rendering one agent per row
// with the reason it is blocked.
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// stoppedAfter is how long a running-state session can be idle (no log/state
// activity) before the monitor treats its process as gone. Generous so a slow
// Gradle gate isn't mislabeled; the real dead ones sit idle for hours/days.
const stoppedAfter = 10 * time.Minute

// freshest is the most recent sign of life for a session: the later of its
// last state write and its log file's mtime (the log streams continuously
// while the agent is actually working).
func freshest(s store.Session) time.Time {
	t := s.UpdatedAt
	if fi, err := os.Stat(paths.LogFor(s.Ticket)); err == nil && fi.ModTime().After(t) {
		t = fi.ModTime()
	}
	return t
}

// isStopped: a running-state session whose process is gone. When we know the
// pid (recorded at run start) the check is deterministic; for older rows with
// no pid we fall back to the activity heuristic.
//
// "Running state" is store.IsActive, not a local copy of its switch. The two
// lists have to agree - cmd/run.go refuses a second run on exactly these states
// while the dashboard files exactly these rows under RUNNING - and a hand-copied
// switch here is a second list to forget when a state is added.
func isStopped(s store.Session) bool {
	if !store.IsActive(s.State) {
		return false
	}
	if s.PID > 0 {
		return !proc.Alive(s.PID)
	}
	return time.Since(freshest(s)) > stoppedAfter
}

type group struct {
	label string
	style lipgloss.Style
	match func(store.Session) bool
	// collapsible groups render their header as a cursor row that Enter
	// toggles; the tickets hide behind it until expanded. Cold groups only -
	// anything a user acts on regularly stays permanently open.
	collapsible bool
	sessions    []store.Session
}

func newGroups() []*group {
	red := lipgloss.NewStyle().Foreground(colorNeedsYou).Bold(true)
	orange := lipgloss.NewStyle().Foreground(colorAttn).Bold(true)
	cyan := lipgloss.NewStyle().Foreground(colorRunning).Bold(true)
	green := lipgloss.NewStyle().Foreground(colorReview).Bold(true)
	violet := lipgloss.NewStyle().Foreground(colorTracking).Bold(true)
	purple := lipgloss.NewStyle().Foreground(colorClosed).Bold(true)
	return []*group{
		{label: "NEEDS YOU", style: red, match: func(s store.Session) bool {
			return s.State == "awaiting-answer" || s.State == "needs-you" ||
				s.State == "failed" || s.State == store.StatePlanReview ||
				s.State == store.StateFixReview || s.State == store.StateChangeReview ||
				// An open PR whose reviewers have asked for changes is waiting on
				// you just as much as a failed run. Groups are first-match-wins and
				// this one is first, so the row leaves READY FOR REVIEW on its own.
				(s.State == store.StateReview && s.OpenComments > 0)
		}},
		{label: "RUNNING", style: cyan, match: func(s store.Session) bool {
			return store.IsActive(s.State) && !isStopped(s)
		}},
		// The no-PR group sits between the agents' work and the parked work: a hand
		// checkout is work in progress - yours - and burying it under finished
		// rows would hide the rows most likely to be touched next.
		{label: "NO PULL REQUEST YET", style: violet, match: func(s store.Session) bool {
			return s.State == store.StateCheckedOut
		}},
		{label: "PR READY FOR REVIEW", style: green, match: func(s store.Session) bool {
			return s.State == store.StateReview
		}},
		// Terminal rows: the PR left review on GitHub's side. Folded away by
		// default, but above STOPPED and in purple (GitHub's merged color) -
		// a closed PR is an outcome worth noticing, a stopped run is debris.
		{label: "CLOSED", style: purple, collapsible: true, match: func(s store.Session) bool {
			return s.State == store.StateMerged || s.State == store.StateClosed
		}},
		{label: "STOPPED", style: orange, collapsible: true, match: func(s store.Session) bool {
			return s.State == store.StateStopped || isStopped(s)
		}},
	}
}

func stateLabel(s store.Session) string {
	if isStopped(s) {
		return "stopped"
	}
	if s.State == store.StateCheckedOut {
		return "checked out"
	}
	if s.State == store.StateReview {
		if s.OpenComments > 0 {
			return plural(s.OpenComments, "comment")
		}
		return "under review"
	}
	if s.State == store.StatePlanReview {
		return "plan ready"
	}
	if s.State == store.StateFixReview {
		return "fixes ready"
	}
	if s.State == store.StateChangeReview {
		return "review before PR"
	}
	return s.State
}

// ---- model -----------------------------------------------------------------

// renderDashboardBody renders the header, the one list, and the detail pane.
//
// Everything is drawn at the content width and inset by the margin at the end,
// so no component has to know where on the terminal it sits - the same
// arrangement the review screen uses, which is what makes the two read as one
// application rather than two.
func (m monitorModel) renderDashboardBody(w int) string {
	if tooNarrow(w) {
		return narrowNotice(w)
	}
	margin, cw := layout(w)

	var b strings.Builder
	b.WriteString(m.renderDashboardHeader(cw))
	b.WriteString("\n")

	for i := range dashCommands {
		b.WriteString(m.renderCommandRow(i, cw, m.cursor == i) + "\n")
	}

	if len(m.flat) == 0 {
		b.WriteString("\n" + stChrome.Render(truncate(
			"no tickets yet - choose a row above and press enter to start one", cw)))
		return indent(b.String(), margin)
	}

	// Wide terminals put the detail - and LAST OUTPUT - beside the list, not
	// under it: stacked, ten agents starve the log to nothing, since its height
	// was whatever the list left over. Side by side it gets the full column.
	// The list is the protagonist: two thirds of the width, always. The detail
	// column is a glanceable ticker, not a reading surface - the menu's "Watch
	// the live output" is where the log gets a whole screen.
	if cw >= 132 && m.selected() != nil && !m.answer.open {
		listW := cw * 66 / 100
		detW := cw - listW - 2
		list, _ := m.renderTicketList(listW)
		det := m.renderDashboardDetail(detW, 0)
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, list, "  ", det))
		return indent(b.String(), margin)
	}
	list, listLines := m.renderTicketList(cw)
	b.WriteString(list)
	b.WriteString("\n")
	b.WriteString(m.renderDashboardDetail(cw, listLines))
	return indent(b.String(), margin)
}

// renderTicketList is the grouped sessions, at any width the caller affords.
// It walks m.rows - the same slice the cursor runs over - so idx == m.cursor
// stays exact whatever is collapsed. Plain groups still get their render-only
// header line before their first ticket; collapsible headers ARE rows.
func (m monitorModel) renderTicketList(cw int) (string, int) {
	var b strings.Builder
	idx := len(dashCommands) // the list continues the same cursor
	listLines := 0
	for ri, r := range m.rows {
		g := m.groups[r.group]
		switch r.kind {
		case rowHeader:
			b.WriteString(m.renderGroupToggle(*g, cw, m.expanded[g.label], idx == m.cursor) + "\n")
		case rowTicket:
			if !g.collapsible && (ri == 0 || m.rows[ri-1].group != r.group) {
				b.WriteString(m.renderSectionHeader(*g) + "\n")
				listLines++
			}
			b.WriteString(m.renderRow(m.flat[r.flat], cw, idx == m.cursor) + "\n")
		}
		idx++
		listLines++
	}
	return b.String(), listLines
}

// renderSectionHeader names a plain group and counts it. No ▾ here: these
// sections do not collapse, so a disclosure triangle would promise an
// interaction that isn't there. The collapsible groups get renderGroupToggle
// instead - a real cursor row whose triangle is honest.
func (m monitorModel) renderSectionHeader(g group) string {
	return "\n" + g.style.Render(letterSpace(g.label)) +
		stChrome.Render(" · "+fmt.Sprint(len(g.sessions)))
}

// renderGroupToggle is a collapsible group's header as a selectable row:
// ▸ folded, ▾ open, Enter toggles. The triangle sits at column 0 so the
// header starts level with the plain section headers - no cursor-mark column
// here (it pushed these two rows visibly right of every other header); the
// focused row is carried by the filled band instead.
func (m monitorModel) renderGroupToggle(g group, cw int, expanded, cursor bool) string {
	tri := "▸"
	if expanded {
		tri = "▾"
	}
	sty := g.style
	if cursor {
		sty = stFocus
	}
	label := tri + " " + letterSpace(g.label) + " · " + fmt.Sprint(len(g.sessions))
	return "\n" + band(cursor, cw, cell(sty, cursor, cw, lipgloss.Left, truncate(label, cw)))
}

// renderDashboardHeader is the brand, the counts, and a rule - the review
// screen's header, with this screen's numbers in it.
func (m monitorModel) renderDashboardHeader(cw int) string {
	needs, running := 0, 0
	for _, g := range m.groups {
		switch g.label {
		case "NEEDS YOU":
			needs += len(g.sessions)
		case "RUNNING":
			running += len(g.sessions)
		}
	}
	daemon := "○ daemon stopped"
	if pid, ok := daemonAlive(); ok {
		daemon = fmt.Sprintf("● daemon pid %d", pid)
	}
	left := stBrand.Render(letterSpace("APPLE PIE")) + "   " + stMeta.Render("dashboard")
	right := stChrome.Render(daemon + " · " + m.lastRefresh.Format("15:04"))
	if lipWidth(left)+lipWidth(right)+2 > cw {
		right = stChrome.Render(m.lastRefresh.Format("15:04"))
	}

	ctx := stTitle.Render(plural(len(m.flat), "ticket"))
	if needs > 0 {
		ctx += stMeta.Render(fmt.Sprintf(" · %d needs you", needs))
	}
	if running > 0 {
		ctx += stMeta.Render(fmt.Sprintf(" · %d running", running))
	}
	head := padBetween(left, right, cw) + "\n" + truncate(ctx, cw) + "\n" + rule(cw) + "\n"
	// A paused agent outranks everything else on this screen: it is live,
	// waiting, and one keystroke resumes it.
	if n := len(m.pendingApprovals); n > 0 {
		head += whyStyle.Render(truncate(
			fmt.Sprintf("⏸ %s waiting for your approval - press A to review", plural(n, approvalNoun(m.pendingApprovals))), cw)) + "\n"
	}
	return head
}

// renderDashboardDetail is the selected row's reason and its log tail.
func (m monitorModel) renderDashboardDetail(cw, listLines int) string {
	sel := m.selected()
	if sel == nil {
		// A command or header row has no detail; say what enter does rather
		// than going blank.
		if g := m.headerUnderCursor(); g != nil {
			verb := "expands"
			if m.expanded[g.label] {
				verb = "collapses"
			}
			return stChrome.Render(truncate("enter "+verb+" this section", cw)) + "\n"
		}
		return stChrome.Render(truncate("enter opens this command", cw)) + "\n"
	}
	var b strings.Builder

	head := stTitle.Render(sel.Ticket)
	if sel.Summary != "" {
		head += stMeta.Render(" · " + sel.Summary)
	}
	right := ""
	if sel.PID > 0 {
		state := "gone"
		if proc.Alive(sel.PID) {
			state = "alive"
		}
		right = stChrome.Render(fmt.Sprintf("pid %d %s", sel.PID, state))
	}
	b.WriteString(padBetween(truncate(head, cw-lipWidth(right)-2), right, cw) + "\n")
	if sel.BaseBranch != "" {
		b.WriteString(stChrome.Render(truncate("stacked on "+sel.BaseBranch, cw)) + "\n")
	}

	whyRows := whyRowsFor(*sel, cw-railW)
	for _, seg := range whyRows {
		b.WriteString(stChrome.Render(rail) + stAmber.Render(seg) + "\n")
	}

	if m.answer.open {
		b.WriteString("\n" + stMeta.Render("answer "+m.answer.ticket) + "\n")
		for i, seg := range wrapWords(m.answer.in.render(), cw-railW) {
			prefix := stAccent.Render("› ")
			if i > 0 {
				prefix = "  "
			}
			b.WriteString(prefix + seg + "\n")
		}
		return b.String()
	}

	b.WriteString("\n" + stChrome.Render(letterSpace("LAST OUTPUT")) + "\n")
	for _, seg := range m.logTail(*sel, cw-railW, listLines+len(whyRows)) {
		b.WriteString(stChrome.Render(rail) + styleLogLine(seg) + "\n")
	}
	return b.String()
}

// rail is the gutter down the left of the detail pane, and railW its cost. It
// stays where the row glyphs went: it marks a whole region rather than labelling
// one line, which is the difference between structure and decoration.
const (
	rail  = "┆ "
	railW = 2
)

// logTail is the session's last output, wrapped and deduped, cut to what is left
// of the screen. The log is the elastic region: it gives up lines so the ticket
// list and the footer never do.
func (m monitorModel) logTail(s store.Session, w, used int) []string {
	h := m.height - used - 14
	if h < 1 {
		h = 1
	}
	// Read more raw lines than rows needed: each may wrap into several, and the
	// dedupe below removes some entirely.
	raw := dedupeAdjacent(tailLines(paths.LogFor(s.Ticket), h*6))
	var segs []string
	for _, ln := range raw {
		segs = append(segs, wrapWords(stripEmphasis(stripPrefix(ln, s.Ticket)), w)...)
	}
	if len(segs) == 0 {
		return []string{stChrome.Render("(no log output yet)")}
	}
	if len(segs) > h {
		segs = segs[len(segs)-h:]
	}
	return segs
}

// ---- helpers ---------------------------------------------------------------
