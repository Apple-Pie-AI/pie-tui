// One list row, and the columns every row shares.
//
// Rows are assembled from fixed-width cells and joined, never hand-padded with
// spaces: a cell knows its own width and alignment, so the columns line up down
// the whole list at any terminal size and the author's right edge is a straight
// line rather than an accident of arithmetic.
//
// The selected row is a band filled across the full content width. A one-cell
// bar on the left is invisible on a wide screen; a filled row can be found from
// across the room.
package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// cell renders one column at a fixed width. On a selected row every cell has to
// carry the background itself - Lip Gloss resets colour at the end of each
// rendered segment, so a band painted only by the enclosing style comes out
// with holes where the cells are.
func cell(st lipgloss.Style, selected bool, w int, align lipgloss.Position, s string) string {
	if selected {
		st = st.Background(sel)
	}
	return st.Width(w).MaxWidth(w).Align(align).Render(s)
}

// band joins a row's cells and fills the rest of the content width.
func band(selected bool, cw int, cells ...string) string {
	row := lipgloss.JoinHorizontal(lipgloss.Top, cells...)
	if selected {
		return stRowSel.Width(cw).MaxWidth(cw).Render(row)
	}
	return lipgloss.NewStyle().Width(cw).MaxWidth(cw).Render(row)
}

// rowLabelStyle is the one place emphasis is decided: the selected row's text
// is bold and bright, and nothing else on the screen is.
func rowLabelStyle(selected bool) lipgloss.Style {
	if selected {
		return stFocus
	}
	return stTitle
}

// markFor is the row cursor glyph: "▸" on the selected row, a blank cell
// otherwise - the one selection cue that survives NO_COLOR.
func markFor(selected bool) string {
	if selected {
		return "▸"
	}
	return " "
}

// renderCommentRow is one list line, on the shared columns.
//
// Every row lays out identically - the cursor changes paint, never position -
// so the list does not shimmy as the cursor moves through it.
func (m monitorModel) renderCommentRow(c store.Comment, cw int, cursor bool) string {
	on := m.comments.sel[c.ID]
	locW, bodyW, authW := commentCols(cw)

	box := "[ ]"
	if on {
		box = "[x]"
	}
	_, body := severity(c.Gist())

	boxSty, locSty, bodySty := stMeta, stMeta, stTitle
	if on {
		boxSty = stAccent
	} else {
		// An unchecked row is a decision already made: mute it entirely - box,
		// location and body - but never to the point of hiding it.
		locSty, bodySty = stChrome, stChrome
	}
	if cursor {
		bodySty = rowLabelStyle(true)
	}
	// A bot's name is tinted and nothing else. That tint is the entire feature
	// that replaced hiding them.
	authSty := stMeta
	if c.Bot {
		authSty = stCyan
	}
	// While a menu is open the rest of the list steps back, so the box that took
	// over the screen is plainly the thing keys are going to.
	if m.comments.mode == modeMenu && !cursor {
		boxSty, locSty, bodySty, authSty = stChrome, stChrome, stChrome, stChrome
	}

	// The ✎ rides with the author, right-aligned as one group, so a row carrying
	// an instruction says so without spending a column on it.
	author := displayAuthor(c)
	if c.UserNote != "" {
		author = "✎ " + author
	}

	return band(cursor, cw,
		cell(boxSty, cursor, colBox, lipgloss.Left, box),
		cell(stChrome, cursor, colMark, lipgloss.Left, "▸"),
		cell(locSty, cursor, locW, lipgloss.Left, truncFront(locationOf(c), locW)),
		cell(bodySty, cursor, bodyW, lipgloss.Left, truncate(body, bodyW)),
		cell(authSty, cursor, colGap+authW, lipgloss.Right, truncate(author, authW)),
	)
}

// locationOf is a row's anchor: the file's own name and the line, never the
// directory. The path in full has its own line in the detail pane, where there
// is width for it; in the list it would eat thirty columns the comment needs.
func locationOf(c store.Comment) string {
	if c.Path == "" {
		return "summary"
	}
	name := filepath.Base(c.Path)
	if c.Line > 0 {
		return fmt.Sprintf("%s:%d", name, c.Line)
	}
	return name
}

// truncFront drops the head of a string rather than its tail, so a location too
// long for its column keeps the part that identifies it: "…ViewModel.kt:42"
// still says which line, where "app/src/main/jav…" says nothing at all.
func truncFront(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 1 && lipWidth("…"+string(r)) > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// displayAuthor drops a bot's "[bot]" suffix. The cyan tint already says what
// it is, and the suffix cost five columns to repeat it - enough to truncate the
// name it was labelling.
func displayAuthor(c store.Comment) string {
	return strings.TrimSuffix(c.Author, "[bot]")
}

// padRight pads to a column width, counting display cells not bytes.
func padRight(s string, w int) string {
	for lipWidth(s) < w {
		s += " "
	}
	return s
}

// padLeft right-aligns within a column width.
func padLeft(s string, w int) string {
	for lipWidth(s) < w {
		s = " " + s
	}
	return s
}
