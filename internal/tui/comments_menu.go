// The comment menu, and the three modes the screen can be in.
//
// A menu exists where there is a choice to make. A comment row opens one,
// because there are several things you might do to a comment. The aggregate row
// does not - there is exactly one thing to do there, and a menu would put a box
// in front of it. Neither does the run row: enter runs it.
//
// Mode is checked at the top of the key router and nowhere else. Every menu bug
// worth having is a key reaching the list while a menu is open, so the list's
// handler must be unreachable from the other two modes rather than merely
// guarded inside them.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
)

// commentsMode is which of the screen's three input modes is live.
type commentsMode int

const (
	modeBrowsing commentsMode = iota // the cursor walks the one list
	modeMenu                         // a comment's menu is open
	modeEditing                      // typing an instruction for the agent
	modeReplying                     // typing a reply to post and resolve by hand (issue #16)
)

// menuAction is what an entry does when it is picked.
type menuAction int

const (
	menuToggle menuAction = iota
	menuReply             // answer the thread yourself and resolve it - no agent
	menuInstruct
	menuOpen
	menuDiff
	menuSep
	menuClose
)

type menuEntry struct {
	action menuAction
	label  string
	hint   string
}

// commentMenu is the focused comment's menu.
//
// The toggle leads and is pre-selected, so the common case is enter, enter. Its
// label states the outcome rather than the state - "Skip this comment", not
// "Checked" - because the label is what the keystroke is about to do.
func (m monitorModel) commentMenu() []menuEntry {
	c := m.focused()
	if c == nil {
		return nil
	}
	toggle := menuEntry{menuToggle, "Fix this comment", "[ ] → [x]"}
	if m.comments.sel[c.ID] {
		toggle = menuEntry{menuToggle, "Skip this comment", "[x] → [ ]"}
	}
	instruct := menuEntry{menuInstruct, "Write an instruction for the agent", ""}
	if c.UserNote != "" {
		instruct = menuEntry{menuInstruct, "Edit your instruction", ""}
	}
	out := []menuEntry{toggle, instruct}
	// The human's own answer leads (issue #16): what GitHub lets you do with one
	// reply, without leaving the hub or involving the agent. Only a thread has a
	// reply target - a review body cannot be resolved on GitHub at all.
	if c.Kind == review.KindThread {
		out = append([]menuEntry{{menuReply, m.replyResolveLabel(), "answer it yourself, no agent"}}, out...)
	}
	if c.URL != "" {
		out = append(out, menuEntry{menuOpen, "Open on GitHub", shortURL(c.URL)})
	}
	out = append(out, menuEntry{menuDiff, "See the whole diff", "full screen · esc returns"})
	return append(out, menuEntry{menuSep, "", ""}, menuEntry{menuClose, "Close", "esc"})
}

// openMenu opens the focused comment's menu on its first item.
func (m *monitorModel) openMenu() {
	if m.focused() == nil {
		return
	}
	m.comments.mode = modeMenu
	m.comments.menuItem = 0
}

// menuMove walks the menu, stepping over the separator and stopping at both
// ends - a held key must not wrap past the item you meant.
func (m *monitorModel) menuMove(delta int) {
	items := m.commentMenu()
	next := m.comments.menuItem
	for {
		next += delta
		if next < 0 || next >= len(items) {
			return // clamped: leave the selection where it was
		}
		if items[next].action != menuSep {
			m.comments.menuItem = next
			return
		}
	}
}

// updateMenu routes keys while the menu is open. The list's handler is not
// reachable from here, which is the point of the mode.
func (m monitorModel) updateMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.comments.mode = modeBrowsing
	case "up":
		m.menuMove(-1)
	case "down":
		m.menuMove(1)
	case "enter":
		return m.activateMenu()
	}
	return m, nil
}

// activateMenu runs the picked entry. Picking acts immediately and closes the
// menu; there is no confirm step, because every entry here is reversible by
// pressing enter again.
func (m monitorModel) activateMenu() (tea.Model, tea.Cmd) {
	items := m.commentMenu()
	c := m.focused()
	if c == nil || m.comments.menuItem < 0 || m.comments.menuItem >= len(items) {
		m.comments.mode = modeBrowsing
		return m, nil
	}
	switch items[m.comments.menuItem].action {
	case menuReply:
		m.comments.mode = modeReplying
		m.comments.nb.clear()
	case menuToggle:
		m.comments.sel[c.ID] = !m.comments.sel[c.ID]
		m.comments.mode = modeBrowsing
	case menuInstruct:
		// The field lives in the detail pane, on the line that already displays
		// it, so the menu gets out of the way to uncover it.
		m.comments.mode = modeEditing
		m.comments.nb.clear()
		if c.UserNote != "" {
			m.comments.nb.paste(c.UserNote)
		}
	case menuOpen:
		m.comments.mode = modeBrowsing
		m.notice = "opening " + c.URL
		return m, openCommentURL(c.URL)
	case menuDiff:
		m.comments.mode = modeBrowsing
		m.comments.cardMode = cardDiff // the triage screen borrows the viewer
		m.comments.diffScroll = 0
	default:
		m.comments.mode = modeBrowsing
	}
	return m, nil
}

// updateInstruction edits the guidance the agent reads before fixing this one
// comment. It is not a reply to the reviewer, which is why it is never called a
// note: the two would be indistinguishable on screen.
func (m monitorModel) updateInstruction(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		m.comments.nb.paste(string(msg.Runes))
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEnter:
		c := m.focused()
		if c == nil {
			m.comments.mode = modeBrowsing
			return m, nil
		}
		text := strings.TrimSpace(m.comments.nb.text())
		m.comments.mode = modeBrowsing
		m.comments.nb.clear()
		if m.store != nil {
			if err := m.store.SetCommentNote(c.ID, text); err != nil {
				m.notice = "saving your instruction: " + err.Error()
				return m, nil
			}
		}
		// Writing instructions for a fix implies wanting the fix.
		if text != "" {
			m.comments.sel[c.ID] = true
		}
		m.loadComments()
		return m, nil
	case tea.KeyEsc:
		m.comments.mode = modeBrowsing
		m.comments.nb.clear()
	default:
		m.comments.nb.key(msg)
	}
	return m, nil
}

// shortURL is a comment's link, short enough for a column: the host, an
// ellipsis, and the fragment that identifies the thread.
func shortURL(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	host, rest, ok := strings.Cut(s, "/")
	if !ok {
		return s
	}
	last := rest
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		last = rest[i+1:]
	}
	return host + "/…/" + last
}

// osc8 wraps text in a terminal hyperlink so the URL is clickable where the
// terminal supports it, and plain text where it does not. The escape carries no
// display width, so callers must measure the label, never this.
func osc8(url, label string) string {
	if url == "" {
		return label
	}
	return "\x1b]8;;" + url + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

// renderMenu draws the bordered box.
//
// It is written into the top of the detail pane rather than composited over the
// list: the list is above it and never moves, so anchoring it here costs no
// layering and nothing reflows. Rounded accent border on the selection
// background, which together with the run button are the only two things on the
// screen that are bordered or filled.
func (m monitorModel) renderMenu(cw int) string {
	items := m.commentMenu()
	c := m.focused()
	if len(items) == 0 || c == nil {
		return ""
	}
	boxW := 56
	if boxW > cw {
		boxW = cw
	}
	inner := boxW - 2 - 2*GutterX // the border and its padding

	var b strings.Builder
	b.WriteString(stMenuTitle.Width(inner).Render(truncate(locationOf(*c), inner)) + "\n")
	for i, e := range items {
		if e.action == menuSep {
			b.WriteString(stMenuHint.Width(inner).Render(repeat("─", inner)) + "\n")
			continue
		}
		b.WriteString(m.menuRow(e, i == m.comments.menuItem, inner) + "\n")
	}
	return stMenu.Render(strings.TrimRight(b.String(), "\n")) + "\n"
}

// menuRow lays one entry out to exactly inner cells: mark, label, then the hint
// pushed to the right edge.
func (m monitorModel) menuRow(e menuEntry, on bool, inner int) string {
	mark := "  "
	if on {
		mark = "▸ "
	}
	label, hint := e.label, e.hint
	gap := inner - 2 - lipWidth(label) - lipWidth(hint)
	if gap < 1 {
		hint = truncate(hint, max(0, inner-2-lipWidth(label)-1))
		gap = inner - 2 - lipWidth(label) - lipWidth(hint)
	}
	if gap < 1 {
		label = truncate(label, max(1, inner-2-lipWidth(hint)-1))
		gap = inner - 2 - lipWidth(label) - lipWidth(hint)
	}
	if gap < 0 {
		gap = 0
	}
	if on {
		// The one inversion inside the menu: dark on accent, so "this is what
		// enter will do" needs no legend.
		return stMenuOn.Width(inner).Render(mark + label + strings.Repeat(" ", gap) + hint)
	}
	labelSty := stMenuItem
	if e.action == menuClose {
		labelSty = stMenuQuiet
	}
	return stMenuHint.Render(mark) + labelSty.Render(label) +
		stMenuItem.Render(strings.Repeat(" ", gap)) + stMenuHint.Render(hint)
}
