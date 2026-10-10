// Rendering for the "Edit config" screen. Split from editconfig.go
// (the comments.go/comments_view.go split already used in this package).
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m monitorModel) renderEditConfig(w int) string {
	e := m.editConfig
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Edit config") + "\n")
	if e.loadErr != nil {
		b.WriteString(dimStyle.Render("  could not load config: "+e.loadErr.Error()) + "\n")
		return b.String()
	}
	b.WriteString("\n")

	links := []struct{ label, desc string }{
		{"Edit command allowlist", "review, add, or remove the commands agents can run without asking"},
	}
	for i, l := range links {
		if e.cursor == i {
			b.WriteString(selStyle.Render(" "+truncate(l.label, w-2)) + "\n")
			b.WriteString(dimStyle.Render("    "+truncate(l.desc, w-4)) + "\n")
		} else {
			b.WriteString("  " + truncate(l.label, w-2) + "\n")
		}
	}
	b.WriteString("\n")

	for i, fld := range withBudgetPlaceholders(e.cfgFields) {
		b.WriteString(renderFormFieldRow(fld, e.fieldStart()+i == e.cursor, w))
	}
	b.WriteString("\n")

	saveLabel := "Save changes"
	if e.cursor == e.saveIdx() {
		b.WriteString(selStyle.Render(" "+saveLabel) + "\n")
	} else {
		b.WriteString("  " + saveLabel + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("  ↑↓ move   enter open a link / apply Save   type to edit a field   esc discard and close"))
	return b.String()
}

// renderFormFieldRow draws one inline-editable config field, shared by the
// edit-config and edit-models screens.
func renderFormFieldRow(fld formField, focused bool, w int) string {
	val := fld.value
	marker, labelSty, valSty := "  ", formLabelStyle, formValStyle
	if focused {
		marker, labelSty, valSty = formMarkFocus.Render("▸ "), formLabelFocus, formValFocus
		val = formField{value: val, cursor: fld.cursor}.caretView()
	}
	prefix := "  " + fld.label + ": "
	avail := w - lipgloss.Width(prefix)
	if avail < 6 {
		avail = 6
	}
	shown := valSty.Render(truncate(val, avail))
	switch {
	case fld.value == "" && fld.placeholder != "" && focused:
		shown += " " + dimStyle.Render(truncate(fld.placeholder, avail))
	case fld.value == "" && fld.placeholder != "":
		shown = dimStyle.Render(truncate(fld.placeholder, avail))
	case val == "" && !focused:
		shown = dimStyle.Render("(empty)")
	}
	return marker + labelSty.Render(fld.label+":") + " " + shown + "\n"
}
