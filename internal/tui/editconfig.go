// The "Edit config" screen: links into the per-stage models screen
// (editmodels.go, leading - the most-visited setting) and the command
// allowlist review screen (permissions.go), then the repo/branch/plan-review
// fields the standalone field form (settings.go) has always edited. One hop
// to touch a field; one more hop, from a link, for models or the allowlist -
// both used to be flattened inline here, and each buried the fields under a
// screenful of rows.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// editConfigState is the screen's state: two link rows lead (the per-stage
// models at 0 - the most-visited setting leads - the command allowlist at 1),
// the general config fields follow, Save is last.
type editConfigState struct {
	cfgFields []formField
	cursor    int
	loadErr   error
}

const editConfigLinks = 2 // allowlist, models

func (e *editConfigState) fieldStart() int { return editConfigLinks }
func (e *editConfigState) fieldEnd() int   { return e.fieldStart() + len(e.cfgFields) }
func (e *editConfigState) saveIdx() int    { return e.fieldEnd() }
func (e *editConfigState) rowCount() int   { return e.saveIdx() + 1 }

// openEditConfig loads the config and opens the screen (mutates via pointer
// receiver - the caller falls through to doAction's own return).
func (m *monitorModel) openEditConfig() {
	e := editConfigState{}
	cfg, err := config.Load()
	if err != nil {
		e.loadErr = err
		m.editConfig = e
		m.view = viewEditConfig
		return
	}
	e.cfgFields = generalConfigFields(cfg)
	m.editConfig = e
	m.view = viewEditConfig
}

func (m monitorModel) updateEditConfig(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := &m.editConfig
	// A field row edits directly, same as the plain form always has: typing
	// and caret movement apply with no separate "enter edit mode" step.
	// Up/Down still move the row cursor (editKey doesn't claim those keys).
	if e.cursor >= e.fieldStart() && e.cursor < e.fieldEnd() {
		if e.cfgFields[e.cursor-e.fieldStart()].editKey(msg) {
			return m, nil
		}
	}
	last := e.rowCount() - 1
	switch msg.String() {
	case "esc", "q":
		m.view = viewDashboard
		m.notice = "config unchanged"
		return m, nil
	case "up", "k":
		if e.cursor > 0 {
			e.cursor--
		}
	case "down", "j":
		if e.cursor < last {
			e.cursor++
		}
	case "enter":
		switch e.cursor {
		case 0:
			m.openEditModels() // the per-stage models screen; own Save/Esc, returns to the dashboard directly
		case 1:
			m.openPermissions() // the nested allowlist screen; same nesting contract
		case e.saveIdx():
			return m.applyEditConfig()
		}
		// A field row already consumed Enter above if editKey wanted it (it
		// doesn't); otherwise Enter there is a no-op - applying lives on the
		// Save row so a stray Enter can't submit early.
	}
	return m, nil
}

// applyEditConfig writes the field edits and returns to the dashboard. It
// never touches extra_allowed_tools - that's the allowlist screen's job,
// applied independently when leaving it.
func (m monitorModel) applyEditConfig() (tea.Model, tea.Cmd) {
	cfg, err := config.Load()
	if err != nil {
		m.notice = "config: load failed: " + err.Error()
		m.view = viewDashboard
		return m, nil
	}
	applyConfigFields(cfg, m.editConfig.cfgFields)
	if err := config.Save(cfg); err != nil {
		m.notice = "config: save failed: " + err.Error()
		m.view = viewDashboard
		return m, nil
	}
	m.notice = "config saved"
	m.view = viewDashboard
	return m, nil
}
