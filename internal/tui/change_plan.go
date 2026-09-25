// Plan mode: the change screen's feedback box has two send behaviors - a
// discuss round that stays on this screen and replies read-only, or a
// rework that implements what was discussed. Split from change_box.go (the
// box's own widget/picker plumbing) to keep both files near the repo's
// ~400-line convention; this one owns the send-mode machinery and its
// rendering (the transcript, the Auto-mode confirm).
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// updateReworkConfirm is Auto mode's inline "start implementing?" prompt,
// armed by Enter in the box - the same shape as updateChangeApprove.
func (m monitorModel) updateReworkConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	switch msg.Type {
	case tea.KeyUp:
		if c.reworkConfirmSel > 0 {
			c.reworkConfirmSel--
		}
	case tea.KeyDown:
		if c.reworkConfirmSel < 1 {
			c.reworkConfirmSel++
		}
	case tea.KeyEsc:
		c.reworkConfirmOpen = false
	case tea.KeyEnter:
		c.reworkConfirmOpen = false
		if c.reworkConfirmSel == 0 {
			return m.sendReworkMessage()
		}
	}
	return m, nil
}

// sendDiscussMessage is Enter in the box while in Plan mode: persists the
// message as the queued turn (ChangeNotes doubles as the discuss inbox - see
// internal/runner/change_discuss.go), clears the box for the next line, and
// stays on THIS screen - a discuss round never navigates away. A round
// already in flight picks it up within its live poll; otherwise this spawns
// one, exactly like every other verb on this screen.
func (m monitorModel) sendDiscussMessage(s store.Session, text string) (tea.Model, tea.Cmd) {
	c := &m.change
	m.persistDraft()
	c.draft.SetValue("")
	if c.discussing {
		m.notice = "sent - the agent will pick it up shortly"
		return m, nil
	}
	m.notice = "asking " + c.ticket + "…"
	c.discussing = true // optimistic; the next tick confirms it from the store
	return m, m.spawnRun(runArg(s), "--discuss")
}

// cancelDiscuss interrupts a live Plan-mode round: Esc while c.discussing is
// true, the same mid-turn cancel Claude Code's own CLI offers. Kills the
// driving process and parks the ticket back at change-review, exactly where
// it sat before the round started - a cancelled discuss leaves "you: ..." in
// the transcript with no reply, same as an interrupted Claude Code turn, and
// the human can just try again. State is set before the kill, not after, the
// same ordering pauseAgent/cancelAgent use (modals.go) and for the same
// reason: writing it afterward would leave a window where the row still
// claims to be working.
func (m monitorModel) cancelDiscuss(s store.Session) tea.Cmd {
	st := m.store
	return func() tea.Msg {
		_ = st.SetState(s.Ticket, store.StateChangeReview, s.Retries)
		stopRun(s)
		return discussCancelledMsg{ticket: s.Ticket}
	}
}

// sendReworkMessage is Auto mode's confirmed "start implementing": persists
// the message, spawns the rework run - resuming whatever session a prior
// Plan-mode conversation left, so nothing discussed needs repeating - and
// hands off to the dashboard exactly like every other spawn on this screen.
func (m monitorModel) sendReworkMessage() (tea.Model, tea.Cmd) {
	c := &m.change
	s := m.sessionByTicket(c.ticket)
	if s == nil {
		m.notice = "no session for " + c.ticket
		return m, nil
	}
	m.persistDraft()
	c.draft.SetValue("")
	c.draft.Blur()
	m.view = viewDashboard
	m.notice = fmt.Sprintf("reworking %s - round %d feedback sent", c.ticket, c.round+1)
	return m, m.spawnRun(runArg(*s), "--rework")
}

// ---- rendering --------------------------------------------------------------

// changeTranscriptPreviewLines is how many transcript lines show in the
// split view's box preview (renderChangeBox) - just enough to tell a
// conversation is happening. The full-screen chat view (renderChangeDiscussFull)
// passes its own, much larger budget instead of this constant.
const changeTranscriptPreviewLines = 8

// renderChangeTranscript draws the Plan-mode conversation above the box:
// each line already prefixed "you: "/"claude: ", wrapped to the box's inner
// width and capped to the tail of maxLines - the start of a long
// conversation scrolls off, same tradeoff as a chat view defaulting to its
// most recent messages.
func (m monitorModel) renderChangeTranscript(cw, maxLines int) []string {
	c := m.change
	inner := cw - 4
	if inner < 10 {
		inner = 10
	}
	var out []string
	for _, ln := range c.transcript {
		sty := stTitle
		if strings.HasPrefix(ln, "you: ") {
			sty = stAccent
		}
		for _, w := range wrapWords(ln, inner) {
			out = append(out, sty.Render(w))
		}
	}
	if c.discussing {
		out = append(out, stChrome.Render("claude is thinking…"))
	}
	if len(out) > maxLines {
		hidden := len(out) - maxLines
		out = append([]string{stChrome.Render(fmt.Sprintf("↑ %d earlier line(s)", hidden))}, out[len(out)-maxLines:]...)
	}
	return out
}

// discussFullScreen reports whether the box should take over the whole
// screen as a chat view instead of sharing it with the split file list: once
// a Plan-mode conversation has actually started (a message sent, or one
// streaming back), the diff stops being the point of the screen and the chat
// should read like one - see renderChangeDiscussFull. Composing the first
// message stays in the split view, where the file list and diff are still
// useful for picking what to ask about.
func (c *changeState) discussFullScreen() bool {
	return c.planMode && (len(c.transcript) > 0 || c.discussing)
}

// changeDiscussBudget is the full-screen chat view's transcript line budget -
// shared between rendering (renderChangeDiscussFull) and the PgUp/PgDown
// scroll handlers (scrollChangeTranscript), which must clamp against the
// exact same number a key press would otherwise scroll past what's actually
// visible.
func (m monitorModel) changeDiscussBudget(cw int) int {
	boxLines := m.renderChangeBoxInput(cw)
	// Reserve: total body minus the header renderChange already drew (2, or
	// 3 with an approve error), our own rule before the box, and the box
	// itself - the same shape renderChangeSplit uses for its own reserve.
	reserved := 4 + len(boxLines)
	if m.change.changeErr != "" {
		reserved++
	}
	budget := m.changeBodyHeight() - reserved
	if budget < 4 {
		budget = 4
	}
	return budget
}

// changeTranscriptLines builds the full-screen chat's complete, unwrapped-
// to-a-budget line list: every committed transcript line, then whatever's
// currently live (change_live) - a growing answer styled like a normal
// "claude: " line, or, while ChangeLive still carries the "thinking: "
// prefix a discuss round writes, dim ephemeral reasoning text - falling back
// to the static "claude is thinking…" placeholder when a round is running
// but nothing has streamed yet (an older claude build, or a turn with
// nothing to stream). No truncation here - renderChangeDiscussFull windows
// this with scrollWindow; the split view's own preview keeps using
// renderChangeTranscript, which stays tail-capped and live-content-free by
// design (see changeTranscriptPreviewLines).
func (m monitorModel) changeTranscriptLines(cw int) []string {
	c := m.change
	inner := cw - 4
	if inner < 10 {
		inner = 10
	}
	var out []string
	for _, ln := range c.transcript {
		sty := stTitle
		if strings.HasPrefix(ln, "you: ") {
			sty = stAccent
		}
		for _, w := range wrapWords(ln, inner) {
			out = append(out, sty.Render(w))
		}
	}
	switch {
	case strings.HasPrefix(c.live, "thinking: "):
		for _, w := range wrapWords(strings.TrimPrefix(c.live, "thinking: "), inner) {
			out = append(out, stMeta.Render(w))
		}
	case c.live != "":
		for _, w := range wrapWords(c.live, inner) {
			out = append(out, stTitle.Render(w))
		}
	case c.discussing:
		out = append(out, stChrome.Render("claude is thinking…"))
	}
	return out
}

// scrollChangeTranscript moves the full-screen chat's scroll position by one
// page (dir -1 up, +1 down) - ctrl+b/ctrl+f (and PgUp/PgDown, where a
// terminal actually forwards them) while the box is focused, free keys
// today (see updateChangeBox). See scrollChangeTranscriptBy for the shared
// mechanics; this is its "move a whole page" caller. The mouse wheel
// (update.go's tea.MouseMsg case) is the other caller, moving a small step
// per notch instead of a full page - the same wheel/keyboard step split the
// diff viewer already uses (changeScrollStep).
func (m *monitorModel) scrollChangeTranscript(dir int) {
	_, cw := layout(m.width)
	viewH := m.changeDiscussBudget(cw)
	m.scrollChangeTranscriptBy(dir * viewH)
}

// scrollChangeTranscriptBy moves the scroll position by an explicit line
// delta (negative up, positive down) - the mechanics both the page-at-a-time
// keyboard scroll and the notch-at-a-time mouse wheel share. -1 (stick to
// the tail, the default) is resolved to the current bottom before moving, so
// the first scroll from a live-following view starts exactly where the eye
// already is instead of jumping; moving back to (or past) the tail
// re-enters stick-to-tail mode so new messages keep pushing the view down
// again without further scrolling.
func (m *monitorModel) scrollChangeTranscriptBy(delta int) {
	_, cw := layout(m.width)
	n := len(m.changeTranscriptLines(cw))
	viewH := m.changeDiscussBudget(cw)
	max := n - viewH
	if max < 0 {
		max = 0
	}
	pos := m.change.transcriptScroll
	if pos < 0 {
		pos = max
	}
	pos += delta
	if pos >= max {
		m.change.transcriptScroll = -1
		return
	}
	if pos < 0 {
		pos = 0
	}
	m.change.transcriptScroll = pos
}

// renderChangeDiscussFull is the full-screen chat view for a Plan-mode
// conversation that's actually under way (discussFullScreen) - the file list
// and diff step aside so the transcript and the input box get the whole
// screen, like a chat client rather than a sidebar squeezed under a diff.
// The transcript is bottom-anchored (padded above, when there's room to
// spare) so the latest messages sit just above the box, and it's scrollable
// (transcriptScroll, PgUp/PgDown) rather than hard-capped to the tail.
func (m monitorModel) renderChangeDiscussFull(cw int) string {
	boxLines := m.renderChangeBoxInput(cw)
	budget := m.changeDiscussBudget(cw)
	lines := m.changeTranscriptLines(cw)
	n := len(lines)
	offset := m.change.transcriptScroll
	if offset < 0 {
		offset = n // scrollWindow clamps this down to the tail automatically
	}

	viewH := budget
	start, end := scrollWindow(n, offset, viewH)
	if start > 0 {
		// Reserve one row for the hint instead of showing budget+1 lines -
		// this view's budget is sized to the terminal exactly, unlike the
		// split preview's small fixed cap, where being a line over is fine.
		viewH = budget - 1
		if viewH < 1 {
			viewH = 1
		}
		start, end = scrollWindow(n, offset, viewH)
	}
	shown := lines[start:end]
	if start > 0 {
		hint := stChrome.Render(fmt.Sprintf("↑ %d earlier line(s) · ctrl+b", start))
		shown = append([]string{hint}, shown...)
	}

	var b strings.Builder
	if pad := budget - len(shown); pad > 0 {
		b.WriteString(strings.Repeat("\n", pad))
	}
	for _, ln := range shown {
		b.WriteString(ln + "\n")
	}
	b.WriteString(rule(cw) + "\n")
	for _, ln := range boxLines {
		b.WriteString(ln + "\n")
	}
	return b.String()
}

// renderReworkConfirm is Auto mode's explicit "start implementing?" prompt,
// armed by Enter in the box - the same inline 2-item shape as the Approve
// CTA's own confirm.
func renderReworkConfirm(sel, cw int) []string {
	w := cw - 4
	if w < 20 {
		w = 20
	}
	out := []string{stAmber.Render("Start implementing what was discussed?")}
	for i, label := range []string{"Confirm", "Cancel"} {
		on := i == sel
		mark := " "
		if on {
			mark = "▸"
		}
		out = append(out, band(on, w,
			cell(stAccent, on, 2, lipgloss.Left, mark),
			cell(rowLabelStyle(on), on, w-2, lipgloss.Left, label),
		))
	}
	return out
}
