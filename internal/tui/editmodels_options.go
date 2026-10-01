// The models screen's picker: which options a stage offers, in order, and
// the background check that shows what a picked model actually runs.
package tui

import (
	"context"
	"slices"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/secrets"
)

// The kinds of picker row: a model to set, or one of the two list actions.
const (
	optModel = iota
	optAdd
	optRemove
)

// modelOption is one picker row. label is what the row says; value is what
// gets passed to `claude --model` ("" = the stage's default); note is a dim
// aside, e.g. the model id under a company label.
type modelOption struct {
	label, value, note string
	kind               int
}

// options is the picker for one stage, in order: the stage's default, the
// company's curated /model list, Claude Code's aliases (unless that list
// replaces the built-in lineup), the user's saved models, the stage's current
// value if none of those has it, then Add and Remove.
func (e *editModelsState) options(f formField) []modelOption {
	opts := []modelOption{{label: blankModelLabel(f.key)}}
	seen := map[string]bool{"": true}
	add := func(o modelOption) {
		if !seen[o.value] {
			seen[o.value] = true
			opts = append(opts, o)
		}
	}
	for _, r := range e.company.Rows {
		o := modelOption{label: r.Model, value: r.Model, note: "from Claude Code settings"}
		if r.Label != "" {
			o.label, o.note = r.Label, r.Model
		}
		add(o)
	}
	if !e.company.ReplaceBuiltIns {
		for _, a := range agent.ModelAliases {
			add(modelOption{label: a, value: a, note: "newest " + a + " on your account"})
		}
	}
	for _, s := range e.saved {
		add(modelOption{label: s, value: s, note: "saved"})
	}
	add(modelOption{label: f.value, value: f.value, note: "current setting"})
	opts = append(opts, modelOption{label: "Add a model… (type any name --model accepts)", kind: optAdd})
	if len(e.saved) > 0 {
		opts = append(opts, modelOption{label: "Remove a saved model…", kind: optRemove})
	}
	return opts
}

// currentOption is the picker row holding a field's value.
func (e *editModelsState) currentOption(f formField) int {
	for i, o := range e.options(f) {
		if o.kind == optModel && o.value == f.value {
			return i
		}
	}
	return 0
}

// isListed reports whether a name is already offered without being saved.
func (e *editModelsState) isListed(name string) bool {
	if slices.Contains(e.saved, name) {
		return true
	}
	for _, r := range e.company.Rows {
		if r.Model == name {
			return true
		}
	}
	return !e.company.ReplaceBuiltIns && slices.Contains(agent.ModelAliases, name)
}

// displayModel is how a stage row names its value: the company's label when
// the curated list has one, else the name itself.
func (e *editModelsState) displayModel(value string) string {
	for _, r := range e.company.Rows {
		if r.Model == value && r.Label != "" {
			return r.Label + " · " + value
		}
	}
	return value
}

// blankModelLabel names what an unset model means for that stage.
func blankModelLabel(key string) string {
	if key == fldModelVerify || key == fldModelCommentFix {
		return "Same as implementation"
	}
	return "Claude Code default"
}

// modelCheckState is one model's check, kept for the whole hub session so
// moving between stages never re-runs (or re-bills) one already done.
type modelCheckState struct {
	pending bool
	result  agent.ModelCheck
}

type modelCheckMsg struct {
	model  string
	result agent.ModelCheck
}

// startModelCheck checks a picked model in the background, once per session.
func (m *monitorModel) startModelCheck(model string) tea.Cmd {
	if model == "" {
		return nil
	}
	if _, done := m.modelChecks[model]; done {
		return nil
	}
	if m.modelChecks == nil {
		m.modelChecks = map[string]modelCheckState{}
	}
	m.modelChecks[model] = modelCheckState{pending: true}
	return func() tea.Msg {
		return modelCheckMsg{model: model, result: agent.CheckModel(context.Background(), model, secrets.Get(secrets.Anthropic))}
	}
}
