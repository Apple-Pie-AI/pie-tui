// The markdown-content authoring path: a textarea for writing or pasting the
// ticket, its glamour preview, and the action bar under both.
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/runner"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
)

// content method: a real multi-line markdown EDITOR - the ticket content drives
// the whole run, so composing it well matters. Enter inserts a new line. Esc
// steps OUT of the editor onto an always-visible action bar (Continue / Preview
// / Discard) below the viewport - so confirming never needs scrolling and a
// stray Esc can't destroy the draft. ctrl+d (quick confirm) and ctrl+p
// (preview) remain as shortcuts. Pastes land in the editor for review instead
// of becoming a chip directly.
func (m monitorModel) updateRunContent(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.run.content == nil { // safety net; entry points arm the editor
		m = m.withContentEditor()
	}

	// Preview mode: read-only - scroll, flip back to editing, or confirm.
	if m.run.preview {
		switch msg.Type {
		case tea.KeyEsc:
			return m.backToEditing()
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
			case "ctrl+p":
				return m.backToEditing()
			case "ctrl+d":
				return m.confirmContent()
			}
		}
		return m, nil
	}

	// Action bar focused: ←→ choose, Enter activates, Esc back into the editor.
	if m.run.bar {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyTab:
			return m.backToEditing()
		case tea.KeyLeft:
			if m.run.btn > 0 {
				m.run.btn--
			}
		case tea.KeyRight:
			if m.run.btn < 2 {
				m.run.btn++
			}
		case tea.KeyEnter:
			switch m.run.btn {
			case 0: // Continue to next step
				return m.confirmContent()
			case 1: // Keep editing
				return m.backToEditing()
			case 2: // Discard
				return m.cancelRunInput(), nil
			}
		}
		return m, nil
	}

	if msg.Paste {
		m.run.content.InsertString(ticket.NormalizeNewlines(string(msg.Runes)))
		return m, nil
	}
	switch msg.String() {
	case "esc":
		// Step out to the action bar - never silently discard the draft.
		m.run.bar, m.run.btn = true, 0
		m.run.content.Blur()
		return m, nil
	case "ctrl+d":
		return m.confirmContent()
	case "ctrl+p":
		return m.openContentPreview()
	}
	// Backspace in an empty editor removes the last queued chip.
	if msg.Type == tea.KeyBackspace && m.run.content.Value() == "" {
		if n := len(m.run.tickets); n > 0 {
			m.run.tickets = m.run.tickets[:n-1]
			return m, nil
		}
	}
	var cmd tea.Cmd
	*m.run.content, cmd = m.run.content.Update(msg)
	return m, cmd
}

// backToEditing returns focus to the editor from the action bar or preview.
func (m monitorModel) backToEditing() (tea.Model, tea.Cmd) {
	m.run.preview, m.run.bar = false, false
	m.run.content.Focus()
	return m, textarea.Blink
}

// openContentPreview renders the draft to a scrollable glamour preview (no-op
// on an empty draft).
func (m monitorModel) openContentPreview() (tea.Model, tea.Cmd) {
	if strings.TrimSpace(m.run.content.Value()) == "" {
		return m, nil
	}
	m.run.ticketLines = renderMarkdownLines(m.run.content.Value(), m.paneWidth())
	m.run.ticketScroll = 0
	m.run.preview = true
	return m, nil
}

// confirmContent turns the editor's content into a pending ticket, entering the
// id/branch confirm chain; with an empty editor it launches the queued chips.
func (m monitorModel) confirmContent() (tea.Model, tea.Cmd) {
	body := m.run.content.Value()
	if strings.TrimSpace(body) == "" {
		return m.launchOrClose()
	}
	m.run.content.Reset()
	m.run.content.Focus() // clean editing state for when the chip chain returns here
	m.run.preview, m.run.bar, m.run.btn = false, false, 0
	return m.addContentTicket(body)
}

// withContentEditor arms content mode's multi-line markdown editor
// (bubbles/textarea: enter for new lines, full 2D cursor movement, wrapping).
func (m monitorModel) withContentEditor() monitorModel {
	ta := textarea.New()
	ta.Placeholder = "Type or paste the ticket here - markdown welcome…"
	ta.CharLimit = 0
	ta.MaxHeight = 0
	ta.ShowLineNumbers = false
	ta.Focus()
	m.run.content = &ta
	m.run.preview = false
	m.sizeContentEditor()
	return m
}

// sizeContentEditor fits the editor to the terminal, leaving room for the
// screen chrome and any queued chips.
func (m monitorModel) sizeContentEditor() {
	if m.run.content == nil {
		return
	}
	w := m.width - 6
	if w < 40 {
		w = 40
	}
	h := m.height - 12 - len(m.run.tickets) // chrome + the esc hint above and action bar below
	if h < 5 {
		h = 5
	}
	m.run.content.SetWidth(w)
	m.run.content.SetHeight(h)
}

func (m monitorModel) addContentTicket(blob string) (tea.Model, tea.Cmd) {
	blob = ticket.NormalizeNewlines(blob) // terminals paste newlines as \r
	if strings.TrimSpace(blob) == "" {
		return m, nil
	}
	title, _, err := ticket.ParseContent(blob)
	if err != nil || title == "" {
		title = "(untitled)"
	}
	guess, _ := ticket.DetectID(blob) // pre-fill only; the user always confirms
	pending := pendingTicket{
		id: guess, title: title, lines: ticket.LineCount(blob), kind: "content", body: blob,
	}
	i := len(m.run.tickets)
	m.run.tickets = append(m.run.tickets, pending)
	m.run.ticketLines = renderMarkdownLines(blob, m.paneWidth())
	m.run.ticketScroll = 0
	// Step 1: confirm the id. The branch pre-fill is computed on its Enter, from
	// the CONFIRMED id (see updateRunIDPrompt).
	m.run.idPrompt = i
	m.run.promptCursor = len([]rune(guess))
	return m, nil
}

// inferBranch pre-fills the creation-time branch field: the repo's branch
// pattern expanded with the detected ticket id and title. The user edits the
// result, and whatever they confirm is passed through verbatim as the final
// branch name (via `run --branch`).
func inferBranch(id, title string) string {
	pattern := "{ticket}-{slug}"
	if cfg, err := config.Load(); err == nil && len(cfg.Repos) > 0 && cfg.Repos[0].Branch != "" {
		pattern = cfg.Repos[0].Branch
	}
	if id == "" {
		id = "PASTED" // ticket.WritePasted's fallback id
	}
	return runner.ResolveBranch(pattern, id, ticket.StripIDPrefix(title, id))
}

// renderBodyPreview renders the first 12 lines of a pasted ticket body as a dim
// quoted block so the user can see what they pasted while confirming its id and
// attaching images (paste is blind otherwise - two corruption bugs slipped
// through invisibly). Returns "" for an empty body (jira chips never have one).
func renderBodyPreview(body string, w int) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	const maxLines = 12
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	var b strings.Builder
	for i, ln := range lines {
		if i >= maxLines {
			break
		}
		b.WriteString(dimStyle.Render("  │ "+truncate(ln, w-4)) + "\n")
	}
	if n := len(lines) - maxLines; n > 0 {
		b.WriteString(dimStyle.Render(fmt.Sprintf("  │ … +%d more lines", n)) + "\n")
	}
	b.WriteString("\n")
	return b.String()
}
