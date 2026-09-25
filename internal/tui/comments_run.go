// The screen while a fix is running, and after it finishes.
//
// One agent run handles the whole batch, so there is no honest per-comment
// progress to show mid-run: every selected comment is "in this run" until the
// run ends and reports which ones it actually did. Inventing a per-comment
// spinner would be a nicer-looking lie. What the user gets instead is the
// agent's own log, live, which is the real answer to "what is it doing".
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// The four phases of the screen.
const (
	phaseIdle = iota
	phaseRunning
	phaseApprove
	phaseDone
)

// phase reports which screen to draw. A run is in flight whenever the session
// is in an active state; fix-review means local fixes are waiting for the
// human's approval (checked before "done" - a fix run parking there is a
// preview, not a result); "done" is the window after this screen launched a
// ship run and the session went idle again.
func (m monitorModel) phase() int {
	s := m.sessionByTicket(m.comments.ticket)
	if s != nil && store.IsActive(s.State) {
		return phaseRunning
	}
	if s != nil && s.State == store.StateFixReview {
		return phaseApprove
	}
	if len(m.comments.ran) > 0 {
		return phaseDone
	}
	return phaseIdle
}

// outcome is what happened to one comment in the run that just finished.
type outcome struct {
	mark  string // "ok" | "--" | "!!"
	label string
	style func(string) string
}

// outcomeFor reads a comment's result from what the runner recorded: an
// addressed_at with a sha means fixed, an agent note means the agent declined
// and said why, and a comment that was never selected was never touched.
func (m monitorModel) outcomeFor(c store.Comment) outcome {
	dim := func(s string) string { return cmtDimSty.Render(s) }
	switch {
	case !m.comments.ran[c.ID]:
		return outcome{"--", "not included", dim}
	case !c.AddressedAt.IsZero():
		label := "fixed"
		if c.AddressedSHA != "" {
			label = "fixed · " + shortSHA(c.AddressedSHA)
		}
		return outcome{"ok", label, func(s string) string { return cmtGoSty.Render(s) }}
	case strings.TrimSpace(c.AgentNote) != "":
		return outcome{"!!", "could not fix", func(s string) string { return cmtRedSty.Render(s) }}
	default:
		return outcome{"!!", "not fixed", func(s string) string { return cmtRedSty.Render(s) }}
	}
}

// renderRunning is the screen while the agent works: the batch, frozen, over a
// live tail of the agent's log.
func (m monitorModel) renderRunning(w int) string {
	var b strings.Builder
	s := m.sessionByTicket(m.comments.ticket)

	// The raw state, not stateLabel: that one folds in a PID-liveness check and
	// reports "stopped" for a run whose pid has not been recorded yet, which is
	// exactly the first seconds of the run this screen is watching.
	stage := "working"
	if s != nil && s.State != "" {
		stage = s.State
	}
	elapsed := ""
	if !m.comments.startedAt.IsZero() {
		d := time.Since(m.comments.startedAt).Truncate(time.Second)
		elapsed = fmt.Sprintf(" · %d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	}
	left := fmt.Sprintf("  apple pie · %s · fixing %s",
		m.comments.prLabel, plural(len(m.comments.ran), "comment"))
	right := stage + elapsed + "   "
	b.WriteString(headerStyle.Render(left) + padTo(w, left, right) + dimStyle.Render(right) + "\n")

	for _, c := range m.visible() {
		mark, label := "··", "in this run"
		if !m.comments.ran[c.ID] {
			mark, label = "--", "not included"
		}
		b.WriteString(m.renderStatusRow(c, w, mark, label, func(s string) string { return dimStyle.Render(s) }) + "\n")
	}

	b.WriteString(sepStyle.Render(strings.Repeat("─", w)) + "\n")
	for _, ln := range m.agentLog(w) {
		b.WriteString(ln + "\n")
	}
	return b.String()
}

// renderDone is the screen after the run: what was fixed, what was not, and why.
func (m monitorModel) renderDone(w int) string {
	var b strings.Builder
	fixed, failed := 0, 0
	for _, c := range m.visible() {
		switch m.outcomeFor(c).mark {
		case "ok":
			fixed++
		case "!!":
			failed++
		}
	}
	left := "  apple pie · " + m.comments.prLabel + " · done"
	right := fmt.Sprintf("%d fixed · %d not fixed   ", fixed, failed)
	b.WriteString(headerStyle.Render(left) + padTo(w, left, right) + dimStyle.Render(right) + "\n")

	for i, c := range m.visible() {
		o := m.outcomeFor(c)
		b.WriteString(m.renderStatusRow(c, w, o.mark, o.label, o.style) + "\n")
		_ = i
	}
	margin, cw := layout(w)
	b.WriteString("\n" + indent(m.renderRunButton(cw), margin))
	b.WriteString(sepStyle.Render(strings.Repeat("─", w)) + "\n")
	b.WriteString(m.renderDoneDetail(w))
	return b.String()
}

// renderDoneDetail explains the highlighted row: the agent's reason when it
// declined, the PR link when it succeeded.
func (m monitorModel) renderDoneDetail(w int) string {
	c := m.focused()
	if c == nil {
		s := m.sessionByTicket(m.comments.ticket)
		if s != nil && s.PRURL != "" {
			return "  " + dimStyle.Render("the fixes were pushed to "+s.PRURL) + "\n"
		}
		return ""
	}
	var b strings.Builder
	b.WriteString(dimStyle.Render(truncate("  "+c.Path, w)) + "\n\n")
	if note := strings.TrimSpace(c.AgentNote); note != "" {
		b.WriteString(whyStyle.Render(truncate("  apple pie did not fix this:", w)) + "\n")
		for _, ln := range wrapLine(note, w-4) {
			b.WriteString("  " + ln + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString(headerStyle.Render(truncate("  "+c.Author, w)) + "\n")
	for _, ln := range m.comments.lines {
		b.WriteString(ln + "\n")
	}
	return b.String()
}

// renderStatusRow is a list row in the running/done phases: the same shape as
// the idle list, with the status where the gist would be.
func (m monitorModel) renderStatusRow(c store.Comment, w int, mark, label string,
	paint func(string) string) string {
	box := "[ ]"
	if m.comments.ran[c.ID] {
		box = "[x]"
	}
	const locW, authW = 24, 12
	statusW := w - 2 - 4 - locW - authW - 8
	if statusW < 10 {
		statusW = 10
	}
	body := box + " " + dimStyle.Render(padRight(truncate(locOf(c), locW), locW)) + " " +
		paint(padRight(truncate(mark+"  "+label, statusW), statusW)) + " " +
		cmtMidSty.Render(truncate(c.Author, authW))
	if m.comments.cursor == m.indexOf(c) {
		return "  " + selStyle.Render(truncate("▸ "+body, w-4))
	}
	return "    " + truncate(body, w-4)
}

// indexOf is a comment's position in the visible list.
func (m monitorModel) indexOf(c store.Comment) int {
	for i, v := range m.visible() {
		if v.ID == c.ID {
			return i
		}
	}
	return -1
}

// agentLog is the tail of the ticket's session log, which is what the agent is
// actually doing right now.
func (m monitorModel) agentLog(w int) []string {
	h := m.height - len(m.visible()) - 8
	if h < 3 {
		h = 3
	}
	raw := tailLines(paths.LogFor(m.comments.ticket), h*4)
	var out []string
	for _, ln := range raw {
		out = append(out, cmtDimSty.Render(truncate("  "+ln, w)))
	}
	if len(out) > h {
		out = out[len(out)-h:]
	}
	if len(out) == 0 {
		return []string{cmtDimSty.Render("  waiting for the agent to start…")}
	}
	return out
}

// padTo returns the spaces that push a right-aligned segment to the edge.
func padTo(w int, left, right string) string {
	n := w - lipWidth(left) - lipWidth(right)
	if n < 1 {
		n = 1
	}
	return strings.Repeat(" ", n)
}
