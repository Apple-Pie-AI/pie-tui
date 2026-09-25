// The "Edit models per stage" screen: the five per-stage model fields, linked
// from "Edit config" (editconfig.go). Split out of that screen's inline list
// because five model rows buried the repo fields most visits are for - the
// same reason the allowlist got its own screen. Same nesting contract as the
// allowlist: its own Save/Esc, returning to the dashboard directly.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// editModelsState is the screen's state: model fields lead, Save is last.
type editModelsState struct {
	fields  []formField
	cursor  int
	loadErr error
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
	// A field row edits directly, exactly as on the edit-config screen: typing
	// and caret movement apply with no separate "enter edit mode" step.
	if e.cursor < len(e.fields) {
		if e.fields[e.cursor].editKey(msg) {
			return m, nil
		}
	}
	switch msg.String() {
	case "esc", "q":
		m.view = viewDashboard
		m.notice = "models unchanged"
		return m, nil
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < e.rowCount()-1 {
			e.cursor++
		}
	case "enter":
		if e.cursor == e.saveIdx() {
			return m.applyEditModels()
		}
		// Enter on a field row is a no-op - applying lives on the Save row so
		// a stray Enter can't submit early.
	}
	return m, nil
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
	b.WriteString(dimStyle.Render("  blank inherits Claude Code's default; type any identifier your account allows") + "\n\n")

	for i, fld := range e.fields {
		b.WriteString(renderFormFieldRow(fld, i == e.cursor, w))
	}
	b.WriteString("\n")

	saveLabel := "Save changes"
	if e.cursor == e.saveIdx() {
		b.WriteString(selStyle.Render(" "+saveLabel) + "\n")
	} else {
		b.WriteString("  " + saveLabel + "\n")
	}

	b.WriteString("\n" + dimStyle.Render("  ↑↓ move   enter apply Save   type to edit a field   esc discard and close"))
	return b.String()
}
