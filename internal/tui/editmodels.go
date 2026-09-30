// The "Edit models per stage" screen: the five per-stage model fields, linked
// from "Edit config" (editconfig.go). Split out of that screen's inline list
// because five model rows buried the repo fields most visits are for - the
// same reason the allowlist got its own screen. Same nesting contract as the
// allowlist: its own Save/Esc, returning to the dashboard directly.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// modelAliases are Claude Code's own --model aliases. Each resolves to the
// newest model in its family, so this list changes when a family is added,
// not on every model release.
var modelAliases = []string{"fable", "opus", "sonnet", "haiku"}

// modelOption is one row of a model field's picker. The last option of every
// list is "Custom…", which opens the field as free text instead of setting it.
type modelOption struct{ label, value string }

const customModelLabel = "Custom… (type a full model id)"

func modelOptions(key string) []modelOption {
	opts := []modelOption{{label: blankModelLabel(key)}}
	for _, a := range modelAliases {
		opts = append(opts, modelOption{label: a, value: a})
	}
	return append(opts, modelOption{label: customModelLabel})
}

// blankModelLabel names what an unset model means for that stage.
func blankModelLabel(key string) string {
	if key == fldModelVerify || key == fldModelCommentFix {
		return "Same as implementation"
	}
	return "Claude Code default"
}

// currentOption is the picker row matching a field's value: an alias, the
// blank default, or Custom… for any other id.
func currentOption(f formField) int {
	opts := modelOptions(f.key)
	for i, o := range opts[:len(opts)-1] {
		if o.value == f.value {
			return i
		}
	}
	return len(opts) - 1
}

// editModelsState is the screen's state: model fields lead, Save is last.
// A field row opens its picker (picking); the picker's Custom… opens the row
// as free text (editing), with editPrev restored if that edit is cancelled.
type editModelsState struct {
	fields   []formField
	cursor   int
	loadErr  error
	picking  bool
	pickSel  int
	editing  bool
	editPrev string
}

func (e *editModelsState) saveIdx() int  { return len(e.fields) }
func (e *editModelsState) rowCount() int { return e.saveIdx() + 1 }

// openEditModels loads the config and opens the screen (mutates via pointer
// receiver - the caller falls through to its own return).
func (m *monitorModel) openEditModels() {
	e := editModelsState{}
	cfg, err := config.Load()
	if err != nil {
		e.loadErr = err
		m.editModels = e
		m.view = viewEditModels
		return
	}
	e.fields = modelFields(cfg)
	m.editModels = e
	m.view = viewEditModels
}

func (m monitorModel) updateEditModels(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := &m.editModels
	switch {
	case e.editing:
		e.updateCustomModel(msg)
		return m, nil
	case e.picking:
		e.updateModelPicker(msg)
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.view = viewDashboard
		m.notice = "models unchanged"
		return m, nil
	case tea.KeyUp:
		if e.cursor > 0 {
			e.cursor--
		}
	case tea.KeyDown:
		if e.cursor < e.rowCount()-1 {
			e.cursor++
		}
	case tea.KeyEnter:
		if e.cursor == e.saveIdx() {
			return m.applyEditModels()
		}
		if e.cursor < len(e.fields) {
			e.picking, e.pickSel = true, currentOption(e.fields[e.cursor])
		}
	}
	return m, nil
}

// updateModelPicker is the focused row's option list: ↑↓ choose, Enter
// applies (or opens Custom… as free text), Esc closes with nothing changed.
func (e *editModelsState) updateModelPicker(msg tea.KeyMsg) {
	f := &e.fields[e.cursor]
	opts := modelOptions(f.key)
	switch msg.Type {
	case tea.KeyUp:
		if e.pickSel > 0 {
			e.pickSel--
		}
	case tea.KeyDown:
		if e.pickSel < len(opts)-1 {
			e.pickSel++
		}
	case tea.KeyEsc:
		e.picking = false
	case tea.KeyEnter:
		e.picking = false
		if e.pickSel < len(opts)-1 {
			f.value = opts[e.pickSel].value
			return
		}
		e.editing, e.editPrev = true, f.value
		if currentOption(*f) != len(opts)-1 {
			f.value = "" // an alias or the default isn't a starting point for a full id
		}
		f.end()
	}
}

// updateCustomModel is the free-text field Custom… opens: typing edits,
// Enter keeps the text, Esc restores the value from before Custom….
func (e *editModelsState) updateCustomModel(msg tea.KeyMsg) {
	f := &e.fields[e.cursor]
	switch msg.Type {
	case tea.KeyEnter:
		f.value = strings.TrimSpace(f.value)
		e.editing = false
	case tea.KeyEsc:
		f.value = e.editPrev
		e.editing = false
	default:
		f.editKey(msg)
	}
}

// applyEditModels writes the model edits onto a freshly loaded config and
// returns to the dashboard. applyConfigFields matches by key, so passing only
// the model fields leaves every other setting untouched.
func (m monitorModel) applyEditModels() (tea.Model, tea.Cmd) {
	cfg, err := config.Load()
	if err != nil {
		m.notice = "config: load failed: " + err.Error()
		m.view = viewDashboard
		return m, nil
	}
	applyConfigFields(cfg, m.editModels.fields)
	if err := config.Save(cfg); err != nil {
		m.notice = "config: save failed: " + err.Error()
		m.view = viewDashboard
		return m, nil
	}
	m.notice = "models saved"
	m.view = viewDashboard
	return m, nil
}

func (m monitorModel) renderEditModels(w int) string {
	e := m.editModels
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Edit models per stage") + "\n")
	if e.loadErr != nil {
		b.WriteString(dimStyle.Render("  could not load config: "+e.loadErr.Error()) + "\n")
		return b.String()
	}
	b.WriteString(dimStyle.Render("  an alias always runs the newest model in that family; Custom… takes any full id your account allows") + "\n\n")

	for i, fld := range e.fields {
		focused := i == e.cursor
		b.WriteString(renderModelRow(fld, focused, focused && e.editing, w))
		if focused && e.picking {
			b.WriteString(renderModelPicker(fld, e.pickSel))
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
	case e.editing:
		hint = "  type a full model id   enter done   esc cancel"
	case e.picking:
		hint = "  ↑↓ choose   enter select   esc close"
	}
	b.WriteString("\n" + dimStyle.Render(hint))
	return b.String()
}

// modelStageLabels are this screen's row names. The shared field labels
// explain what blank means, which this screen already shows as the value.
var modelStageLabels = map[string]string{
	fldModelPlan:       "Planning",
	fldModelImpl:       "Implementation",
	fldModelVerify:     "Verify",
	fldModelReview:     "Self-review",
	fldModelCommentFix: "Review-comment fixes",
}

// renderModelRow is a model field: its stage and the chosen model, with a
// blank value spelled out as what it means for that stage - or, while
// Custom… is open (editing), the typed id with its caret.
func renderModelRow(fld formField, focused, editing bool, w int) string {
	marker, labelSty, valSty := "  ", formLabelStyle, formValStyle
	if focused {
		marker, labelSty, valSty = formMarkFocus.Render("▸ "), formLabelFocus, formValFocus
	}
	label := padRight(modelStageLabels[fld.key]+":", 22)
	avail := w - lipgloss.Width("  "+label+" ")
	if avail < 6 {
		avail = 6
	}
	shown := valSty.Render(truncate(fld.value, avail))
	switch {
	case editing:
		shown = valSty.Render(truncate(fld.caretView(), avail))
	case fld.value == "":
		shown = dimStyle.Render(truncate(blankModelLabel(fld.key), avail))
	}
	return marker + labelSty.Render(label) + " " + shown + "\n"
}

// renderModelPicker is the option list under the focused row. The option
// matching the saved value is tagged, so moving the cursor never hides it.
func renderModelPicker(fld formField, sel int) string {
	var b strings.Builder
	opts := modelOptions(fld.key)
	current := currentOption(fld)
	for i, o := range opts {
		label := o.label
		switch {
		case i == current && i == len(opts)-1:
			label = "Custom… · " + fld.value + "  (current)"
		case i == current:
			label += "  (current)"
		}
		// Indented to the value column renderModelRow draws (2 + 22 + 1).
		if i == sel {
			b.WriteString(strings.Repeat(" ", 24) + selStyle.Render(" "+label+" ") + "\n")
		} else {
			b.WriteString(strings.Repeat(" ", 25) + label + "\n")
		}
	}
	return b.String()
}
