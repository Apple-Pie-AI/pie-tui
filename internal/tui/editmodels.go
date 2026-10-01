// The "Edit models per stage" screen: the five per-stage model fields, opened
// from the dashboard's "Edit models" row (and the command palette). Its own
// Save/Esc, returning to the dashboard directly. Enter on a stage opens its
// picker (editmodels_options.go); rendering lives in editmodels_view.go.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// editModelsState is the screen's state: model fields lead, Save is last.
// A field row opens its picker (picking). From the picker, "Add a model…"
// opens a text field (adding) and "Remove a saved model…" a second list
// (removing). saved is the add/remove list, written on Save like the fields.
type editModelsState struct {
	fields  []formField
	cursor  int
	loadErr error

	company   agent.ModelPicker // Claude Code's curated /model list, if any
	saved     []string
	picking   bool
	pickSel   int
	adding    bool
	addField  formField
	removing  bool
	removeSel int
}

// loadModelPicker reads Claude Code's curated /model list; a variable so tests
// don't depend on the settings of the machine running them.
var loadModelPicker = agent.ClaudeModelPicker

func (e *editModelsState) saveIdx() int  { return len(e.fields) }
func (e *editModelsState) rowCount() int { return e.saveIdx() + 1 }

// openEditModels loads the config and Claude Code's model list and opens the
// screen (mutates via pointer receiver - the caller falls through to its own
// return).
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
	e.saved = append([]string(nil), cfg.SavedModels...)
	e.company, _ = loadModelPicker()
	m.editModels = e
	m.view = viewEditModels
}

func (m monitorModel) updateEditModels(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := &m.editModels
	var picked string
	switch {
	case e.adding:
		picked = e.updateAddModel(msg)
	case e.removing:
		e.updateRemoveModel(msg)
	case e.picking:
		picked = e.updateModelPicker(msg)
	default:
		return m.updateModelRows(msg)
	}
	return m, m.startModelCheck(picked)
}

// updateModelRows is the stage list itself: ↑↓ move, Enter opens the
// focused stage's picker (or applies Save), Esc discards and closes.
func (m monitorModel) updateModelRows(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := &m.editModels
	switch msg.Type {
	case tea.KeyEsc:
		m.view = viewDashboard
		m.notice = "models unchanged"
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
			e.picking, e.pickSel = true, e.currentOption(e.fields[e.cursor])
		}
	}
	return m, nil
}

// updateModelPicker is the focused stage's option list: ↑↓ choose, Enter
// applies a model or opens Add/Remove, Esc closes with nothing changed. It
// returns the model just picked, so the caller can check it.
func (e *editModelsState) updateModelPicker(msg tea.KeyMsg) string {
	f := &e.fields[e.cursor]
	opts := e.options(*f)
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
		switch o := opts[e.pickSel]; o.kind {
		case optAdd:
			e.adding, e.addField = true, formField{}
		case optRemove:
			e.removing, e.removeSel = true, 0
		default:
			f.value = o.value
			return o.value
		}
	}
	return ""
}

// updateAddModel is the text field "Add a model…" opens: typing edits, Enter
// saves the name to every stage's list and picks it for this stage, Esc
// cancels. It returns the added model, so the caller can check it.
func (e *editModelsState) updateAddModel(msg tea.KeyMsg) string {
	switch msg.Type {
	case tea.KeyEsc:
		e.adding = false
	case tea.KeyEnter:
		e.adding = false
		name := strings.TrimSpace(e.addField.value)
		if name == "" {
			return ""
		}
		if !e.isListed(name) {
			e.saved = append(e.saved, name)
		}
		e.fields[e.cursor].value = name
		return name
	default:
		e.addField.editKey(msg)
	}
	return ""
}

// updateRemoveModel is the saved-model list "Remove a saved model…" opens:
// Enter removes the focused name, Esc closes. A stage already set to a
// removed name keeps it - the picker still shows it as its current setting.
func (e *editModelsState) updateRemoveModel(msg tea.KeyMsg) {
	switch msg.Type {
	case tea.KeyUp:
		if e.removeSel > 0 {
			e.removeSel--
		}
	case tea.KeyDown:
		if e.removeSel < len(e.saved)-1 {
			e.removeSel++
		}
	case tea.KeyEsc:
		e.removing = false
	case tea.KeyEnter:
		if e.removeSel < len(e.saved) {
			e.saved = append(e.saved[:e.removeSel:e.removeSel], e.saved[e.removeSel+1:]...)
		}
		if e.removeSel >= len(e.saved) {
			e.removeSel = len(e.saved) - 1
		}
		if len(e.saved) == 0 {
			e.removing = false
		}
	}
}

// applyEditModels writes the model edits and the saved list onto a freshly
// loaded config and returns to the dashboard. applyConfigFields matches by
// key, so passing only the model fields leaves every other setting untouched.
func (m monitorModel) applyEditModels() (tea.Model, tea.Cmd) {
	cfg, err := config.Load()
	if err != nil {
		m.notice = "config: load failed: " + err.Error()
		m.view = viewDashboard
		return m, nil
	}
	applyConfigFields(cfg, m.editModels.fields)
	cfg.SavedModels = m.editModels.saved
	if err := config.Save(cfg); err != nil {
		m.notice = "config: save failed: " + err.Error()
		m.view = viewDashboard
		return m, nil
	}
	m.notice = "models saved"
	m.view = viewDashboard
	return m, nil
}
