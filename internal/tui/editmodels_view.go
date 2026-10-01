// Rendering for the models screen. Split from editmodels.go.
package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// modelStageLabels are this screen's row names. The shared field labels
// explain what blank means, which this screen already shows as the value.
var modelStageLabels = map[string]string{
	fldModelPlan:       "Planning",
	fldModelImpl:       "Implementation",
	fldModelVerify:     "Verify",
	fldModelReview:     "Self-review",
	fldModelCommentFix: "Review-comment fixes",
}

// modelValueCol is where a row's value starts: marker (2) + label (22) + gap.
const modelValueCol = 25

func (m monitorModel) renderEditModels(w int) string {
	e := m.editModels
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Edit models per stage") + "\n")
	if e.loadErr != nil {
		b.WriteString(dimStyle.Render("  could not load config: "+e.loadErr.Error()) + "\n")
		return b.String()
	}
	source := "  aliases run the newest model in that family on your account; Add a model… takes any name --model accepts"
	if e.company.File != "" {
		file := e.company.File
		if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(file, home+string(os.PathSeparator)) {
			file = "~" + strings.TrimPrefix(file, home)
		}
		source = "  your Claude Code /model list leads (" + file + ")"
	}
	b.WriteString(dimStyle.Render(truncate(source, w)) + "\n")
	b.WriteString(dimStyle.Render("  a picked model is checked once in the background: seconds, and at most a few cents") + "\n\n")

	for i, fld := range e.fields {
		focused := i == e.cursor
		b.WriteString(m.renderModelRow(fld, focused, w))
		if !focused {
			continue
		}
		switch {
		case e.picking:
			b.WriteString(e.renderModelPicker(fld, w))
		case e.adding:
			b.WriteString(strings.Repeat(" ", modelValueCol) + stTitle.Render("Add a model: ") +
				formValFocus.Render(e.addField.caretView()) + "\n")
		case e.removing:
			b.WriteString(e.renderRemoveList())
		}
	}
	b.WriteString("\n")

	saveLabel := "Save changes"
	if e.cursor == e.saveIdx() {
		b.WriteString(selStyle.Render(" "+saveLabel) + "\n")
	} else {
		b.WriteString("  " + saveLabel + "\n")
	}

	hint := "  ↑↓ move   enter choose a model / save   esc discard and close"
	switch {
	case e.adding:
		hint = "  type a model name, e.g. claude-opus-4-6   enter add   esc cancel"
	case e.removing:
		hint = "  ↑↓ choose   enter remove   esc close"
	case e.picking:
		hint = "  ↑↓ choose   enter select   esc close"
	}
	b.WriteString("\n" + dimStyle.Render(hint))
	return b.String()
}

// renderModelRow is one stage: its name, the chosen model (a blank value
// spelled out as what it means for that stage), and that model's check.
func (m monitorModel) renderModelRow(fld formField, focused bool, w int) string {
	marker, labelSty, valSty := "  ", formLabelStyle, formValStyle
	if focused {
		marker, labelSty, valSty = formMarkFocus.Render("▸ "), formLabelFocus, formValFocus
	}
	label := padRight(modelStageLabels[fld.key]+":", modelValueCol-3)
	avail := w - modelValueCol
	if avail < 6 {
		avail = 6
	}
	if fld.value == "" {
		return marker + labelSty.Render(label) + " " + dimStyle.Render(truncate(blankModelLabel(fld.key), avail)) + "\n"
	}
	name := m.editModels.displayModel(fld.value)
	status := m.modelCheckText(fld.value)
	if room := avail - lipgloss.Width(name) - 2; room > 8 {
		status = truncate(status, room)
	} else {
		status = ""
	}
	line := marker + labelSty.Render(label) + " " + valSty.Render(truncate(name, avail))
	if status != "" {
		line += "  " + m.modelCheckStyle(fld.value).Render(status)
	}
	return line + "\n"
}

// modelCheckText is a model's check result in a few words, "" if unchecked.
func (m monitorModel) modelCheckText(model string) string {
	c, ok := m.modelChecks[model]
	switch {
	case !ok:
		return ""
	case c.pending:
		return "checking…"
	case c.result.Err != "":
		return "✗ " + c.result.Err
	case c.result.Mismatch(model):
		return "⚠ ran " + c.result.Resolved + " instead"
	case c.result.Resolved == model:
		return "✓"
	default:
		return "✓ " + c.result.Resolved
	}
}

func (m monitorModel) modelCheckStyle(model string) lipgloss.Style {
	c := m.modelChecks[model]
	switch {
	case c.pending:
		return dimStyle
	case c.result.Err != "" || c.result.Mismatch(model):
		return whyStyle
	default:
		return stAccent
	}
}

// renderModelPicker is the option list under the focused stage. The row
// holding the stage's value is tagged, so moving the cursor never hides it.
func (e *editModelsState) renderModelPicker(fld formField, w int) string {
	var b strings.Builder
	current := e.currentOption(fld)
	for i, o := range e.options(fld) {
		label := o.label
		if i == current {
			label += "  (current)"
		}
		note := ""
		if o.note != "" {
			if room := w - modelValueCol - 2 - lipgloss.Width(label) - 2; room > 8 {
				note = "  " + dimStyle.Render(truncate(o.note, room))
			}
		}
		if i == e.pickSel {
			b.WriteString(strings.Repeat(" ", modelValueCol-2) + stAccent.Render("▸") + " " + stFocus.Render(label) + note + "\n")
		} else {
			b.WriteString(strings.Repeat(" ", modelValueCol) + stTitle.Render(label) + note + "\n")
		}
	}
	return b.String()
}

// renderRemoveList is the saved models, one of them focused for removal.
func (e *editModelsState) renderRemoveList() string {
	var b strings.Builder
	for i, s := range e.saved {
		if i == e.removeSel {
			b.WriteString(strings.Repeat(" ", modelValueCol-2) + stAccent.Render("▸") + " " + stFocus.Render("remove "+s) + "\n")
		} else {
			b.WriteString(strings.Repeat(" ", modelValueCol) + stTitle.Render(s) + "\n")
		}
	}
	return b.String()
}
