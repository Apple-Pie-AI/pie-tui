// The feedback box: state, keys, and rendering for the change screen's one
// input surface - a wrapping textarea plus an @ file picker. Enter is
// intercepted before it ever reaches the textarea (it sends, never inserts a
// newline - a bubbles/textarea consumer pattern already used once in this
// package, run_content.go:83-107). ←/→ always move the caret - the one place
// they don't navigate the screen; ↑ moves the caret up within wrapped lines
// unless already on the textarea's first visual row (LineInfo().RowOffset==0,
// meaningful because Enter never lets a real newline in, so the whole draft
// stays one wrapping paragraph), in which case it exits to the Approve row
// instead.
package tui

import (
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// newChangeBox builds the feedback textarea, prefilled with any draft the
// screen was left with. It starts blurred; openChangeView focuses it.
func newChangeBox(initial string) *textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Comment on a file or a line, or write here. @ mentions any file in the repo."
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.ShowLineNumbers = false
	// The focused view is rendered with its ANSI intact so the caret shows;
	// every other textarea style is cleared so the caret is the only styling
	// that gets through, and it stays solid so no blink messages are needed.
	plain := textarea.Style{Placeholder: stMeta}
	ta.FocusedStyle, ta.BlurredStyle = plain, plain
	ta.Cursor.Style = lipgloss.NewStyle().Reverse(true)
	ta.Cursor.SetMode(cursor.CursorStatic)
	ta.SetValue(initial)
	ta.Blur()
	return &ta
}

// sizeChangeBox fits the box to the terminal - full content width, 3 lines
// tall, as specified. Called on open and on every resize. The width leaves 2
// columns the border math (renderChangeBox's inner) doesn't wrap text into -
// room for the "❯ " prompt glyph prefixed onto the first rendered line.
func (m *monitorModel) sizeChangeBox() {
	if m.change.draft == nil {
		return
	}
	_, cw := layout(m.width)
	w := cw - 4 - 2
	if w < 20 {
		w = 20
	}
	m.change.draft.SetWidth(w)
	m.change.draft.SetHeight(3)
}

// openChangeBox focuses the box, inserting a mention (a leading "@path" or
// "@path:line", "" for plain general feedback) at the end of the existing
// draft - never replacing it, so a file mention can be added to text already
// there.
func (m monitorModel) openChangeBox(mention string, origin changeBoxOrigin) (tea.Model, tea.Cmd) {
	c := &m.change
	c.boxOrigin = origin
	c.mode = chBox
	c.chatCompact = false
	if mention != "" {
		insertMention(c.draft, mention)
	}
	c.boxModelDiscuss, c.boxModelImpl, c.boxModelVerify = effectiveChangeBoxModels()
	cmd := c.draft.Focus()
	m.persistDraft()
	// The wheel scrolls the full-screen chat's transcript the moment it can
	// show one (update.go's tea.MouseMsg case) - enabled here, unconditionally
	// on every box open rather than only once discussFullScreen() is already
	// true, so the single symmetric close point below (updateChangeBox's two
	// mode-changing exits) never has to reason about which of several ways
	// the box got here also turned mouse capture on.
	return m, tea.Batch(cmd, tea.EnableMouseCellMotion)
}

// effectiveChangeBoxModels resolves the model backing each stage Enter in the
// box can reach:
//   - discuss: Plan mode's conversation (change_discuss.go). Runs on
//     ModelPlan, the same field the pipeline's own upfront planning stage
//     uses (runner.go) - both are read-only reasoning turns, just at
//     different points in the ticket's life. discuss also resumes the
//     ticket's implementation session (ResumeID) when one exists, but that
//     does not pin it to ModelImpl: Claude Code's CLI takes --model
//     alongside --resume fine, switching models mid-session for one turn -
//     see change_discuss.go.
//   - impl: Auto mode's rework, the first of its two runs (change_rework.go).
//   - verify: Auto mode's re-verify, its second run (verify.go) - genuinely a
//     different model from impl whenever ModelVerify is set, so a single
//     "model:" line for Auto mode would silently hide half of what Enter is
//     about to do.
//
// "default" stands in for an unset field - Claude Code then picks its own
// model, which pie has no way to name without asking the CLI itself.
func effectiveChangeBoxModels() (discuss, impl, verify string) {
	cfg, err := config.Load()
	if err != nil {
		return "default", "default", "default"
	}
	named := func(m string) string {
		if m == "" {
			return "default"
		}
		return m
	}
	return named(cfg.ModelPlan), named(cfg.ModelImpl), named(cfg.EffectiveModelVerify())
}

// updateChangeBox owns the keyboard while the box is focused.
func (m monitorModel) updateChangeBox(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	if c.picker.open {
		return m.updateChangePicker(msg)
	}
	if c.reworkConfirmOpen {
		return m.updateReworkConfirm(msg)
	}
	if msg.Paste {
		var cmd tea.Cmd
		*c.draft, cmd = c.draft.Update(msg)
		m.persistDraft()
		return m, cmd
	}
	switch msg.Type {
	case tea.KeyEsc:
		// A live discuss round takes Esc first - cancel, not navigate, the
		// same mid-turn interrupt Claude Code's own CLI offers. Auto mode
		// never reaches here "working": sending it leaves for the dashboard
		// immediately (sendReworkMessage).
		if c.discussing {
			if s := m.sessionByTicket(c.ticket); s != nil {
				m.notice = "cancelling " + c.ticket + "…"
				return m, m.cancelDiscuss(*s)
			}
		}
		c.draft.Blur()
		c.mode = c.boxOrigin.mode
		c.cursor = c.boxOrigin.cursor
		c.fileIdx = c.boxOrigin.fileIdx
		c.lineCur = c.boxOrigin.lineCur
		c.yOff = c.boxOrigin.yOff
		// Give the terminal its mouse back - unless we're returning to the
		// full-screen file view (chFull/chScroll), which owns the wheel for
		// its own diff scroll (update.go's tea.MouseMsg case) and enabled
		// mouse capture itself on ITS OWN entry (change.go's "right" key) -
		// disabling it here would cut that scroll off from under it.
		if c.mode != chFull && c.mode != chScroll {
			return m, tea.DisableMouse
		}
		return m, nil
	case tea.KeyEnter:
		return m.sendFeedback()
	case tea.KeyShiftTab:
		// The same binding and status line Claude Code's own CLI uses for
		// its permission-mode cycle - here it's just the two modes this box
		// has (Plan/Auto), toggled rather than cycled through more.
		c.planMode = !c.planMode
		return m, nil
	case tea.KeyUp:
		if c.draft.LineInfo().RowOffset == 0 {
			c.draft.Blur()
			c.mode = chSplit
			c.cursor = c.ctaAt()
			return m, tea.DisableMouse
		}
	case tea.KeyPgUp, tea.KeyCtrlB:
		// PgUp is free today in both this switch and the textarea's own
		// DefaultKeyMap, but a lot of terminals intercept PageUp/PageDown for
		// their OWN scrollback before the keystroke ever reaches a foreground
		// program - confirmed live: the transcript scrolls correctly when the
		// real key code reaches pie (driven via a pty), but some terminals
		// never forward it at all. Ctrl+B is the same "page back" binding
		// less/vim use for exactly this reason - it's a plain control byte,
		// not a terminal-chrome shortcut, so it's passed through raw
		// virtually everywhere. It shadows the textarea's redundant
		// character-backward alias (arrow keys remain the primary way to
		// move the caret one character) - only meaningful once the
		// full-screen chat is actually showing.
		if c.discussFullScreen() {
			m.scrollChangeTranscript(-1)
			return m, nil
		}
	case tea.KeyPgDown, tea.KeyCtrlF:
		if c.discussFullScreen() {
			m.scrollChangeTranscript(1)
			return m, nil
		}
	case tea.KeyRunes:
		if string(msg.Runes) == "@" {
			var cmd tea.Cmd
			*c.draft, cmd = c.draft.Update(msg)
			c.picker = mentionPicker{open: true}
			c.picker.items = m.filterChangeMentions("")
			m.persistDraft()
			return m, cmd
		}
	}
	var cmd tea.Cmd
	*c.draft, cmd = c.draft.Update(msg)
	m.persistDraft()
	return m, cmd
}

// updateChangePicker owns the keyboard while the @ picker is open, layered
// over the box: ↑↓ choose, Enter inserts, Esc closes the picker (the box
// keeps whatever was typed). Every other key still reaches the textarea, so
// typing keeps refining the query and backspace/space can close the picker
// as a side effect of the text itself changing. The picker's own key
// handling and filtering are generic (mention_picker.go); this is just the
// change screen's wiring - which fields feed it and when to persist.
func (m monitorModel) updateChangePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := &m.change
	stop, changed := updateMentionPicker(&c.picker, c.draft, msg, m.filterChangeMentions)
	if stop {
		if changed {
			m.persistDraft()
		}
		return m, nil
	}
	var cmd tea.Cmd
	*c.draft, cmd = c.draft.Update(msg)
	m.persistDraft()
	return m, cmd
}

// filterChangeMentions is the change screen's refilter callback for the
// generic picker: the reviewed files lead, then the whole repo.
func (m monitorModel) filterChangeMentions(query string) []string {
	c := m.change
	priority := make([]string, len(c.files))
	for i, f := range c.files {
		priority[i] = f.path
	}
	return filterMentions(query, priority, c.repoFiles, c.repoFilesLower, mentionPickerCap)
}

// isChangedPath reports whether p is one of the reviewed files - the
// picker's "changed" tag.
func (c *changeState) isChangedPath(p string) bool {
	for _, f := range c.files {
		if f.path == p {
			return true
		}
	}
	return false
}

// sendFeedback is Enter in the box with the picker and confirm both closed:
// a no-op on an empty draft; in Plan mode it starts (or continues) a
// conversation without leaving the screen; in Auto mode it arms the explicit
// "start implementing?" confirm rather than spawning on a bare Enter - the
// same two-step caution the Approve CTA already uses.
func (m monitorModel) sendFeedback() (tea.Model, tea.Cmd) {
	c := &m.change
	text := strings.TrimSpace(c.draft.Value())
	if text == "" {
		m.notice = "nothing to send"
		return m, nil
	}
	s := m.sessionByTicket(c.ticket)
	if s == nil {
		m.notice = "no session for " + c.ticket
		return m, nil
	}
	if !c.planMode {
		c.reworkConfirmOpen, c.reworkConfirmSel = true, 0
		return m, nil
	}
	return m.sendDiscussMessage(*s, text)
}

// confirmShipChange is the approve confirm's "Create pull request" - the
// gated ship run, exactly as the CTA used to spawn directly.
func (m monitorModel) confirmShipChange() (tea.Model, tea.Cmd) {
	c := &m.change
	s := m.sessionByTicket(c.ticket)
	if s == nil {
		m.notice = "no session for " + c.ticket
		return m, nil
	}
	m.view = viewDashboard
	action := "re-verify if moved, commit, push & PR"
	if c.hasPR() {
		action = "re-verify if moved, commit & push to the open PR"
	}
	m.notice = "shipping " + c.ticket + " - " + action
	return m, m.spawnRun(runArg(*s), "--ship-change")
}

// persistDraft mirrors the box's current text into the store on every
// keystroke, so leaving the screen never loses it.
func (m *monitorModel) persistDraft() {
	if m.store == nil || m.change.ticket == "" || m.change.draft == nil {
		return
	}
	_ = m.store.SetChangeNotes(m.change.ticket, m.change.draft.Value())
}

// changeRepoFilesMsg carries a worktree's full file list for the @ picker's
// second tier. Loaded once per worktree per session and cached on
// monitorModel (repoFileCache) - update.go's handler both feeds the current
// screen and populates the cache for any later screen against the same
// worktree.
type changeRepoFilesMsg struct {
	ticket, worktree string
	files            []string
}

// loadRepoFilesCmd runs one `git ls-files` off the render path.
func loadRepoFilesCmd(worktree, ticket string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", worktree, "ls-files").Output()
		if err != nil {
			return changeRepoFilesMsg{ticket: ticket, worktree: worktree}
		}
		files := strings.Split(strings.TrimSpace(string(out)), "\n")
		return changeRepoFilesMsg{ticket: ticket, worktree: worktree, files: files}
	}
}

// ---- rendering --------------------------------------------------------------

// renderChangeBox draws the transcript preview (when there's one, or a round
// is running - capped to changeTranscriptPreviewLines, just enough to show a
// conversation is happening) above the always-visible input box. Used by the
// split view, where the diff pane still owns most of the screen. Once a
// Plan-mode conversation is actually under way, renderChange switches to
// renderChangeDiscussFull instead - see changeState.discussFullScreen -
// which uses renderChangeBoxInput directly with its own, much larger
// transcript budget rather than this preview.
func (m monitorModel) renderChangeBox(cw int) []string {
	c := m.change
	var out []string
	if len(c.transcript) > 0 || c.discussing {
		out = append(out, m.renderChangeTranscript(cw, changeTranscriptPreviewLines)...)
	}
	return append(out, m.renderChangeBoxInput(cw)...)
}

// renderChangeBoxInput draws just the input surface - the always-visible
// 3-line box: dim border idle, amber focused, the persisted draft shown
// either way. Appends the @ picker, the rework confirm, or a
// mode-appropriate hint line while focused, so callers need no extra footer
// of their own when the box owns the keyboard. No transcript - callers that
// want one prepend it themselves (renderChangeBox for the split preview,
// renderChangeDiscussFull for the full-screen chat).
func (m monitorModel) renderChangeBoxInput(cw int) []string {
	c := m.change
	focused := c.mode == chBox
	var out []string

	sty := stChrome
	title := " Feedback · Plan "
	if !c.planMode {
		title = " Feedback · Auto "
	}
	if focused {
		sty = stAmber
	} else if strings.TrimSpace(c.draft.Value()) != "" {
		title = strings.TrimSuffix(title, " ") + " · draft "
	}
	inner := cw - 4
	if inner < 10 {
		inner = 10
	}
	fill := inner - len(title) - 1
	if fill < 0 {
		fill = 0
	}
	out = append(out, sty.Render("─"+title+strings.Repeat("─", fill)+"─"))
	// "❯ " on the first line only, like Claude Code's own prompt - continuation
	// lines (this is still one wrapping paragraph; Enter never inserts a real
	// newline) get two blank columns instead, so the text stays aligned under it.
	// c.draft.View() carries its own ANSI (the textarea's placeholder/cursor
	// styling), which stripAnsiStr throws away - so the placeholder must be
	// re-styled here, dim, or it renders in the same plain color as real
	// typed text and reads as something the user already wrote.
	// No side borders - like Claude Code's own input, the box is just the
	// horizontal rules above and below, not a fully-boxed rectangle.
	empty := strings.TrimSpace(c.draft.Value()) == ""
	for i, ln := range strings.Split(c.draft.View(), "\n") {
		prefix := "  "
		if i == 0 {
			prefix = "❯ "
		}
		text := stripAnsiStr(ln)
		switch {
		case focused:
			text = ln // keeps the caret; see newChangeBox's styles
		case empty:
			text = stMeta.Render(text)
		}
		content := prefix + text
		out = append(out, "  "+padRight(content, inner)+"  ")
	}
	out = append(out, sty.Render(strings.Repeat("─", inner+4)))
	switch {
	case focused && c.reworkConfirmOpen:
		out = append(out, renderReworkConfirm(c.reworkConfirmSel, cw)...)
	case focused && c.picker.open:
		tag := func(p string) string {
			if c.isChangedPath(p) {
				return "changed"
			}
			return ""
		}
		out = append(out, renderMentionPicker(c.picker.items, c.picker.sel, c.picker.query, tag, cw)...)
		out = append(out, stChrome.Render("↑↓ choose   enter insert   esc close"))
	case focused:
		// The same status line Claude Code's own CLI shows for its
		// permission-mode cycle - shift+tab really does cycle it here too.
		// Plan mode is the active/selected state, so it gets the dashboard's
		// own selected-row treatment (stFocus: bold, the same white a
		// selected ticket's title takes) rather than a dim hint color.
		modeLine := stFocus.Render("⏸ plan mode on (shift+tab to cycle)")
		if !c.planMode {
			modeLine = stAmber.Render("⏵⏵ auto mode on (shift+tab to cycle)")
		}
		out = append(out, modeLine)
		// Named by stage, not just "model:" - Auto mode is two runs that can
		// land on two different models (see effectiveChangeBoxModels), so a
		// single unlabeled line would hide one of them.
		modelLine := "Planning model: " + c.boxModelDiscuss
		if !c.planMode {
			modelLine = "Implementation model: " + c.boxModelImpl + "   Verify model: " + c.boxModelVerify
		}
		out = append(out, stAccent.Render(modelLine))
		if c.discussFullScreen() {
			out = append(out, stMeta.Render("ctrl+b/ctrl+f scroll the conversation"))
		}
		out = append(out, stMeta.Render("esc back, keep draft   ↑ on first line: back to Approve"))
	}
	return out
}
