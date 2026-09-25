// The command allowlist review screen: the extra rules "Allow & remember"
// accumulates in extra_allowed_tools over time, with no way to see or undo
// them short of hand-editing config.toml. Reached from "Edit config"'s first
// row (editconfig.go); Esc returns straight to the dashboard, same as every
// other screen in this hub - there is no back-to-the-previous-screen stack.
//
// Edits apply IMMEDIATELY: Enter on a rule opens a remove/keep dialog (the
// stop-confirmation pattern), and choosing Remove writes the config right
// there - no staged toggles, no separate Save row to find. The dialog is the
// safety net a save button used to be.
//
// The allowlist BASELINE (allowed_tools / the built-in default) is shown for
// context but not edited here: it is the documented "expert knob" for
// wholesale replacement, and editing it inline risks silently losing default
// upgrades (the exact failure NormalizeAllowedTools exists to prevent). Edit
// it directly in ~/.pie/config.toml if you mean to replace it wholesale.
package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// permissionsState is the screen's state: the baseline (display-only), the
// extra rules, the add-rule input when open, and the remove dialog when open.
type permissionsState struct {
	baseline    []string
	baselineSrc string // "default" or "custom (allowed_tools set)"
	rows        []string
	cursor      int // [0, addIdx) = rule rows, addIdx = the add row
	adding      bool
	addField    formField
	err         string
	loadErr     error
	// confirm is the index of the rule the remove dialog is asking about;
	// -1 = no dialog. confirmCursor: 0 = Remove, 1 = Keep (mirrors the stop
	// dialog's destructive-first order).
	confirm       int
	confirmCursor int
}

func (p *permissionsState) addIdx() int   { return len(p.rows) }
func (p *permissionsState) rowCount() int { return p.addIdx() + 1 }

// openPermissions loads the config and opens the allowlist screen (mutates
// via pointer receiver - the caller falls through to doAction's own return).
// A load failure still opens the screen (so Esc works) with the error shown
// instead of rows.
func (m *monitorModel) openPermissions() {
	p := permissionsState{confirm: -1}
	cfg, err := config.Load()
	if err != nil {
		p.loadErr = err
		m.permissions = p
		m.view = viewPermissions
		return
	}
	if cfg.AllowedTools == "" {
		p.baseline = config.SplitRules(config.DefaultAllowedTools)
		p.baselineSrc = "default"
	} else {
		p.baseline = config.SplitRules(cfg.AllowedTools)
		p.baselineSrc = "custom"
	}
	p.rows = config.SplitRules(cfg.ExtraAllowedTools)
	m.permissions = p
	m.view = viewPermissions
}

func (m monitorModel) updatePermissions(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.permissions
	if p.adding {
		return m.updatePermissionsAdding(msg)
	}
	if p.confirm >= 0 {
		return m.updatePermissionsConfirm(msg)
	}
	switch msg.String() {
	case "esc", "q":
		// Every edit already hit the disk when it was made; Esc just leaves.
		m.view = viewDashboard
		return m, nil
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < p.rowCount()-1 {
			p.cursor++
		}
	case "enter":
		switch {
		case p.cursor == p.addIdx():
			p.adding = true
			p.addField = formField{}
			p.err = ""
		case p.cursor < len(p.rows):
			p.confirm = p.cursor
			p.confirmCursor = 0
			p.err = ""
		}
	}
	return m, nil
}

// updatePermissionsConfirm is the remove/keep dialog over one rule.
func (m monitorModel) updatePermissionsConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.permissions
	switch msg.String() {
	case "esc", "q":
		p.confirm = -1
	case "up", "k":
		p.confirmCursor = 0
	case "down", "j":
		p.confirmCursor = 1
	case "enter":
		idx := p.confirm
		p.confirm = -1
		if p.confirmCursor != 0 || idx >= len(p.rows) {
			return m, nil // Keep: the dialog closes, nothing changes
		}
		kept := append(append([]string{}, p.rows[:idx]...), p.rows[idx+1:]...)
		if err := saveExtraRules(kept); err != nil {
			p.err = "save failed: " + err.Error()
			return m, nil // the rule stays listed - it is still in the config
		}
		p.rows = kept
		if p.cursor > 0 && p.cursor >= len(p.rows) {
			p.cursor = len(p.rows) // at most the add row
		}
	}
	return m, nil
}

// updatePermissionsAdding handles the single-line add-rule input, reusing
// formField's caret editor rather than a second one. A valid rule is written
// to the config immediately - same contract as removal.
func (m monitorModel) updatePermissionsAdding(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := &m.permissions
	switch msg.Type {
	case tea.KeyEsc:
		p.adding = false
		p.err = ""
		return m, nil
	case tea.KeyEnter:
		tok := strings.TrimSpace(p.addField.value)
		if tok == "" {
			p.adding = false
			return m, nil
		}
		if !config.ValidRule(tok) {
			p.err = "not a rule Claude Code can act on: " + tok
			return m, nil
		}
		for _, r := range p.rows {
			if r == tok {
				p.err = "already in the list: " + tok
				return m, nil
			}
		}
		next := append(append([]string{}, p.rows...), tok)
		if err := saveExtraRules(next); err != nil {
			p.err = "save failed: " + err.Error()
			return m, nil
		}
		p.rows = next
		p.adding = false
		p.err = ""
		// Land on the just-added row - a typo'd rule should be one Enter away
		// from its remove dialog, not require navigating up.
		p.cursor = len(p.rows) - 1
		return m, nil
	}
	p.addField.editKey(msg)
	return m, nil
}

// saveExtraRules writes the rule list back to extra_allowed_tools. Load-then-
// save so concurrent edits to other config fields survive.
func saveExtraRules(rules []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cfg.ExtraAllowedTools = strings.Join(rules, " ")
	return config.Save(cfg)
}
