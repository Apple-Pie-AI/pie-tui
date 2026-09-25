// The wizard's single-field prompts: the Jira key list, and the per-chip ticket
// id, branch name and image attachments - each shown over a scrollable preview
// of the ticket body being confirmed.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
	tea "github.com/charmbracelet/bubbletea"
)

// jira method: whitespace-separated Jira keys become chips directly.
func (m monitorModel) updateRunJira(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		return m.commitJira(string(msg.Runes)), nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		if strings.TrimSpace(m.run.text) != "" {
			m = m.commitJira(m.run.text)
			m.run.text = ""
		}
		return m.launchOrClose()
	case tea.KeyEsc:
		return m.cancelRunInput(), nil
	case tea.KeyBackspace:
		if r := []rune(m.run.text); len(r) > 0 {
			m.run.text = string(r[:len(r)-1])
		} else if n := len(m.run.tickets); n > 0 {
			m.run.tickets = m.run.tickets[:n-1]
		}
	case tea.KeySpace:
		if strings.TrimSpace(m.run.text) != "" {
			m = m.commitJira(m.run.text)
			m.run.text = ""
		}
	case tea.KeyRunes:
		m.run.text += string(msg.Runes)
	default:
		switch msg.String() {
		case "ctrl+s":
			// Open the stack picker for the last chip, if any.
			if n := len(m.run.tickets); n > 0 {
				return m.openStackPicker(n - 1)
			}
		}
	}
	return m, nil
}

// commitJira turns whitespace-separated Jira keys into chips.
func (m monitorModel) commitJira(s string) monitorModel {
	for _, tok := range strings.Fields(s) {
		m.run.tickets = append(m.run.tickets, pendingTicket{
			id: ticket.Normalize(tok), title: tok, kind: "jira",
		})
	}
	return m
}

// updateRunImgAttach: drag image files into the terminal (their paths arrive as
// text) to attach them to the content ticket. Enter on an empty line finishes.
func (m monitorModel) updateRunImgAttach(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.run.imgPrompt
	if i < 0 || i >= len(m.run.tickets) {
		m.run.imgPrompt, m.run.text = -1, ""
		return m, nil
	}
	if msg.Paste {
		return m.attachImages(i, string(msg.Runes)), nil // dragged/pasted path(s)
	}
	switch msg.Type {
	case tea.KeyEnter:
		if strings.TrimSpace(m.run.text) != "" {
			m = m.attachImages(i, m.run.text)
			m.run.text, m.run.promptCursor = "", 0
		} else {
			m.run.imgPrompt = -1
			// Content tickets: advance to the stack picker (or straight to the
			// review toggle for a from-branch chip - its branch already exists,
			// so there is nothing to stack).
			if m.run.tickets[i].kind == "content" {
				return m.openStackPicker(i)
			}
		}
	case tea.KeyEsc:
		m.run.imgPrompt, m.run.text = -1, ""
		if i < len(m.run.tickets) && m.run.tickets[i].kind == "content" {
			return m.openStackPicker(i)
		}
	default:
		// Caret editing (←→/Home/End/type/backspace/delete) via the shared
		// form-field editor - a typed path is long and a typo in the middle of it
		// used to mean retyping the tail.
		fld := formField{value: m.run.text, cursor: m.run.promptCursor}
		if fld.editKey(msg) {
			m.run.text, m.run.promptCursor = fld.value, fld.cursor
		}
	}
	return m, nil
}

func (m monitorModel) attachImages(i int, s string) monitorModel {
	for _, p := range ticket.ParseDroppedPaths(s) {
		if ticket.IsImageFile(p) {
			m.run.tickets[i].images = append(m.run.tickets[i].images, p)
		}
	}
	return m
}

// updateRunIDPrompt: confirm/fix the detected ticket id for a NEW-branch
// content ticket (it names the dashboard row, worktree, and logs) - only that
// path asks for one; an existing-branch ticket's id is the branch itself (see
// updateRunBranchPick). On accept it computes the branch pre-fill from the
// CONFIRMED id and advances to the branch prompt. The chip's id doubles as
// the edit buffer.
func (m monitorModel) updateRunIDPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.run.idPrompt
	if i < 0 || i >= len(m.run.tickets) {
		m.run.idPrompt = -1
		return m, nil
	}
	if msg.Paste {
		// Insert at the caret, whitespace stripped - an id is one token, and a
		// pasted "PLEX-123\n" must not smuggle a newline into the field.
		fld := formField{value: m.run.tickets[i].id, cursor: m.run.promptCursor}
		fld.insert(strings.Join(strings.Fields(string(msg.Runes)), ""))
		m.run.tickets[i].id, m.run.promptCursor = fld.value, fld.cursor
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		if id := ticket.Normalize(m.run.tickets[i].id); id != "" {
			m.run.tickets[i].id = id
			m.run.idPrompt = -1
			t := m.run.tickets[i]
			m.run.tickets[i].branch = inferBranch(t.id, t.title)
			m.run.branchPrompt = i
			m.run.promptCursor = len([]rune(m.run.tickets[i].branch))
		}
	case tea.KeyEsc:
		m.run.tickets = append(m.run.tickets[:i], m.run.tickets[i+1:]...) // drop this chip
		m.run.idPrompt = -1
		m.run.ticketLines, m.run.ticketScroll = nil, 0
	case tea.KeyUp:
		m.run.ticketScroll--
		m.clampTicketScroll()
	case tea.KeyDown:
		m.run.ticketScroll++
		m.clampTicketScroll()
	case tea.KeyPgUp:
		m.run.ticketScroll -= m.ticketViewportHeight()
		m.clampTicketScroll()
	case tea.KeyPgDown:
		m.run.ticketScroll += m.ticketViewportHeight()
		m.clampTicketScroll()
	default:
		switch msg.String() {
		case "ctrl+e":
			return m.openTicketEditor(i)
		}
		// Caret editing (←→/Home/End/type/backspace/delete) via the shared
		// form-field editor.
		fld := formField{value: m.run.tickets[i].id, cursor: m.run.promptCursor}
		if fld.editKey(msg) {
			m.run.tickets[i].id, m.run.promptCursor = fld.value, fld.cursor
		}
	}
	return m, nil
}

// updateRunBranchPrompt: step 2 - confirm/edit the pre-filled branch name for a
// pasted content ticket. On accept it advances to the image-attach step. The
// chip's branch doubles as the edit buffer; the confirmed value is the FINAL
// branch name, passed verbatim to `run --branch`.
func (m monitorModel) updateRunBranchPrompt(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	i := m.run.branchPrompt
	if i < 0 || i >= len(m.run.tickets) {
		m.run.branchPrompt = -1
		return m, nil
	}
	if msg.Paste {
		// Insert at the caret, whitespace collapsed to "-" (same rule Enter
		// applies: git refuses spaces in branch names).
		fld := formField{value: m.run.tickets[i].branch, cursor: m.run.promptCursor}
		fld.insert(strings.Join(strings.Fields(string(msg.Runes)), "-"))
		m.run.tickets[i].branch, m.run.promptCursor = fld.value, fld.cursor
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		// Collapse whitespace to "-" (git refuses spaces in branch names).
		if br := strings.Join(strings.Fields(m.run.tickets[i].branch), "-"); br != "" {
			m.run.tickets[i].branch = br
			m.run.branchPrompt = -1
			m.run.imgPrompt = i // next: attach images
			m.run.ticketLines, m.run.ticketScroll = nil, 0
		}
	case tea.KeyEsc:
		m.run.tickets = append(m.run.tickets[:i], m.run.tickets[i+1:]...) // drop this chip
		m.run.branchPrompt = -1
		m.run.ticketLines, m.run.ticketScroll = nil, 0
	case tea.KeyUp:
		m.run.ticketScroll--
		m.clampTicketScroll()
	case tea.KeyDown:
		m.run.ticketScroll++
		m.clampTicketScroll()
	case tea.KeyPgUp:
		m.run.ticketScroll -= m.ticketViewportHeight()
		m.clampTicketScroll()
	case tea.KeyPgDown:
		m.run.ticketScroll += m.ticketViewportHeight()
		m.clampTicketScroll()
	default:
		switch msg.String() {
		case "ctrl+e":
			return m.openTicketEditor(i)
		}
		// Caret editing (←→/Home/End/type/backspace/delete) via the shared
		// form-field editor.
		fld := formField{value: m.run.tickets[i].branch, cursor: m.run.promptCursor}
		if fld.editKey(msg) {
			m.run.tickets[i].branch, m.run.promptCursor = fld.value, fld.cursor
		}
	}
	return m, nil
}

// openTicketEditor suspends the TUI and opens the pasted ticket body in the
// user's $VISUAL/$EDITOR (vim fallback) via a temp .md file, so the full
// content can be reviewed and edited before the run launches.
func (m monitorModel) openTicketEditor(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.run.tickets) {
		return m, nil
	}
	f, err := os.CreateTemp("", "pie-ticket-*.md")
	if err != nil {
		m.notice = "edit ticket: " + err.Error()
		return m, nil
	}
	path := f.Name()
	if _, err := f.WriteString(m.run.tickets[i].body); err != nil {
		f.Close()
		os.Remove(path)
		m.notice = "edit ticket: " + err.Error()
		return m, nil
	}
	f.Close()
	parts := strings.Fields(editorCmd())
	c := exec.Command(parts[0], append(parts[1:], path)...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg {
		return ticketEditedMsg{i: i, path: path, err: err}
	})
}

// editorCmd picks the user's editor: $VISUAL, then $EDITOR, then vim. The value
// may carry flags (e.g. "code -w"), so callers split it into fields.
func editorCmd() string {
	if v := os.Getenv("VISUAL"); v != "" {
		return v
	}
	if v := os.Getenv("EDITOR"); v != "" {
		return v
	}
	return "vim"
}

// applyTicketEdit folds the editor result back into the chip: body, title, line
// count, and detected id are re-derived. The branch field is re-inferred only if
// the user hadn't already customized it away from the previous inferred value.
func (m monitorModel) applyTicketEdit(msg ticketEditedMsg) (tea.Model, tea.Cmd) {
	defer os.Remove(msg.path)
	i := msg.i
	if msg.err != nil {
		m.notice = "edit ticket: " + msg.err.Error()
		return m, nil
	}
	if i < 0 || i >= len(m.run.tickets) {
		return m, nil
	}
	raw, err := os.ReadFile(msg.path)
	if err != nil {
		m.notice = "edit ticket: " + err.Error()
		return m, nil
	}
	body := ticket.NormalizeNewlines(string(raw))
	if strings.TrimSpace(body) == "" {
		m.notice = "edit discarded - the ticket can't be empty"
		return m, nil
	}
	t := &m.run.tickets[i]
	prevInferred := inferBranch(t.id, t.title)
	title, _, err := ticket.ParseContent(body)
	if err != nil || title == "" {
		title = "(untitled)"
	}
	// Re-detect the id, but keep the current one (possibly hand-typed in the id
	// step) when the new content has none.
	if id, ok := ticket.DetectID(body); ok {
		t.id = id
	}
	t.body, t.title, t.lines = body, title, ticket.LineCount(body)
	if t.branch != "" && t.branch == prevInferred {
		t.branch = inferBranch(t.id, title)
	}
	if m.run.idPrompt == i || m.run.branchPrompt == i {
		m.run.ticketLines = renderMarkdownLines(body, m.paneWidth())
		m.run.ticketScroll = 0
		// The active field's value may have been re-derived - park the caret at
		// its end.
		if m.run.idPrompt == i {
			m.run.promptCursor = len([]rune(t.id))
		} else {
			m.run.promptCursor = len([]rune(t.branch))
		}
	}
	return m, nil
}

// renderTicketPrompt is the shared layout for the id and branch confirmation
// steps: the full ticket content, markdown-rendered (same glamour pipeline as
// the plan viewer) in a scrollable window, with the step's header and input
// field directly below it.
func (m monitorModel) renderTicketPrompt(body, header, field string, w int) string {
	var b strings.Builder
	lines := m.run.ticketLines
	if lines == nil && strings.TrimSpace(body) != "" {
		lines = renderMarkdownLines(body, m.paneWidth())
	}
	viewH := m.ticketViewportHeight()
	start, end := scrollWindow(len(lines), m.run.ticketScroll, viewH)
	for _, ln := range lines[start:end] {
		b.WriteString(ln + "\n")
	}
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  "+header) + "\n")
	b.WriteString("  " + field + "\n")
	hint := "←→ move · ctrl+e edit ticket · enter next · esc drop this ticket"
	if len(lines) > viewH {
		hint = fmt.Sprintf("%d–%d of %d · ↑↓ scroll · %s", start+1, end, len(lines), hint)
	}
	b.WriteString("\n  " + dimStyle.Render(hint))
	return b.String()
}

// scrollWindow returns the visible [start, end) bounds of an n-line buffer
// scrolled to offset with viewH visible rows.
func scrollWindow(n, offset, viewH int) (start, end int) {
	start = offset
	if start > n-viewH {
		start = n - viewH
	}
	if start < 0 {
		start = 0
	}
	end = start + viewH
	if end > n {
		end = n
	}
	return start, end
}

// ticketViewportHeight is how many rows of the pasted ticket are visible in the
// id/branch prompts: the screen minus their chrome (app header, blank, title,
// input line, and the blank + hint footer).
func (m monitorModel) ticketViewportHeight() int {
	h := m.height - 7
	if h < 3 {
		h = 3
	}
	return h
}

// clampTicketScroll keeps the branch prompt's scroll offset within [0, max].
func (m *monitorModel) clampTicketScroll() {
	max := len(m.run.ticketLines) - m.ticketViewportHeight()
	if max < 0 {
		max = 0
	}
	if m.run.ticketScroll > max {
		m.run.ticketScroll = max
	}
	if m.run.ticketScroll < 0 {
		m.run.ticketScroll = 0
	}
}
