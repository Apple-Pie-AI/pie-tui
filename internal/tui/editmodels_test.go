package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// "Edit models" is a dashboard command row, right after "Edit config": Enter
// on it opens the models screen with exactly the five per-stage fields,
// values loaded from the config. The command palette offers it too.
func TestDashboardEditModelsRowOpensModelsScreen(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelImpl: "haiku", ModelCommentFix: "opus"})
	m.view = viewDashboard
	m.cursor = 3
	if dashCommands[2].label != "Edit config" || dashCommands[3].label != "Edit models" {
		t.Fatalf("command rows = %+v, want Edit models right after Edit config", dashCommands)
	}
	mm, _ := m.Update(ecEnter())
	hub := mm.(monitorModel)
	if hub.view != viewEditModels {
		t.Fatalf("view = %v, want viewEditModels", hub.view)
	}
	if len(hub.editModels.fields) != 5 {
		t.Fatalf("fields = %v, want the 5 per-stage models", hub.editModels.fields)
	}
	if got := fieldValue(hub.editModels.fields, fldModelImpl); got != "haiku" {
		t.Errorf("impl field = %q, want the configured value", got)
	}
	rendered := hub.renderEditModels(100)
	for _, want := range []string{"Edit models per stage", "Planning:", "Review-comment fixes:", "Save changes"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
	var inPalette bool
	for _, it := range m.paletteItems() {
		inPalette = inPalette || it.key == actModels
	}
	if !inPalette {
		t.Error("the command palette must offer Edit models")
	}
}

// The picker's selected option carries the ▸ marker, so it reads as selected
// even with no color at all.
func TestEditModelsPickerMarksSelection(t *testing.T) {
	m := editConfigModel(t, &config.Config{})
	m.openEditModels()
	m = emPress(t, m, ecEnter(), ecDown())
	if out := stripAnsiStr(m.renderEditModels(120)); !strings.Contains(out, "▸ opus") {
		t.Fatalf("the selected option must carry the ▸ marker:\n%s", out)
	}
}

// Editing a model and saving persists exactly that change: the other models,
// the repo, and the allowlist stay untouched (applyConfigFields is key-matched,
// so a models-only field list is a safe partial apply).
func TestEditModelsSavesOnlyModels(t *testing.T) {
	m := editConfigModel(t, &config.Config{
		ModelImpl:         "haiku",
		ExtraAllowedTools: "Bash(python3:*)",
		Repos:             []config.Repo{{Path: "/repo", Branch: "b"}},
	})
	m.openEditModels()
	// Cursor starts on field 0 (plan); move to comment-fix (field 4) and pick
	// "opus": options are [Same as implementation, opus, sonnet, haiku, ...].
	m = emPress(t, m, ecDown(), ecDown(), ecDown(), ecDown(), ecEnter(), ecDown(), ecEnter())
	if got := fieldValue(m.editModels.fields, fldModelCommentFix); got != "opus" {
		t.Fatalf("comment-fix field = %q, want the picked alias", got)
	}
	m = emPress(t, m, ecDown(), ecEnter()) // Save row
	if m.view != viewDashboard {
		t.Fatalf("view after save = %v, want viewDashboard", m.view)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelCommentFix != "opus" || cfg.ModelImpl != "haiku" {
		t.Errorf("models = commentfix:%q impl:%q, want the edit and the untouched sibling", cfg.ModelCommentFix, cfg.ModelImpl)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != "/repo" || cfg.ExtraAllowedTools != "Bash(python3:*)" {
		t.Errorf("a models-only save must not touch repo or allowlist: %+v %q", cfg.Repos, cfg.ExtraAllowedTools)
	}
}

// Esc discards model edits: nothing is written to disk.
func TestEditModelsEscDiscards(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelPlan: "haiku"})
	m.openEditModels()
	m = emPress(t, m, ecEnter(), ecUp(), ecEnter()) // haiku (last alias) → sonnet
	if got := fieldValue(m.editModels.fields, fldModelPlan); got != "sonnet" {
		t.Fatalf("field edit did not register before esc: %q", got)
	}
	m = emPress(t, m, ecEsc())
	if m.view != viewDashboard {
		t.Fatalf("view after esc = %v, want viewDashboard", m.view)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelPlan != "haiku" {
		t.Fatalf("esc wrote a change: model_plan = %q, want the original untouched", cfg.ModelPlan)
	}
}

func ecUp() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyUp} }

func emPress(t *testing.T, m monitorModel, keys ...tea.KeyMsg) monitorModel {
	t.Helper()
	for _, k := range keys {
		mm, _ := m.updateEditModels(k)
		m = mm.(monitorModel)
	}
	return m
}

func emType(t *testing.T, m monitorModel, s string) monitorModel {
	t.Helper()
	for _, r := range s {
		m = emPress(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

// Enter opens the picker on the saved value; Esc closes it unchanged; rows
// don't take typing directly, so a stray letter changes nothing.
func TestEditModelsPickerOpensOnCurrentAndEscKeepsValue(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelPlan: "sonnet"})
	m.openEditModels()
	m = emType(t, m, "q")
	if m.view != viewEditModels || fieldValue(m.editModels.fields, fldModelPlan) != "sonnet" {
		t.Fatalf("a letter on a model row must do nothing: view=%v plan=%q", m.view, fieldValue(m.editModels.fields, fldModelPlan))
	}
	m = emPress(t, m, ecEnter())
	e := m.editModels
	if !e.picking || e.options(e.fields[0])[e.pickSel].value != "sonnet" {
		t.Fatalf("picker must open on the current value: picking=%v sel=%d", e.picking, e.pickSel)
	}
	if out := stripAnsiStr(m.renderEditModels(140)); !strings.Contains(out, "sonnet  (current)") || !strings.Contains(out, "Add a model…") {
		t.Fatalf("picker must list the options and tag the current one:\n%s", out)
	}
	m = emPress(t, m, ecDown(), ecEsc())
	if m.editModels.picking || m.view != viewEditModels || fieldValue(m.editModels.fields, fldModelPlan) != "sonnet" {
		t.Fatalf("esc must close only the picker, value unchanged: picking=%v view=%v plan=%q",
			m.editModels.picking, m.view, fieldValue(m.editModels.fields, fldModelPlan))
	}
}

// The first option clears the field back to blank, labelled for what blank
// means on that stage.
func TestEditModelsBlankOptionIsPerStage(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelPlan: "opus", ModelVerify: "opus"})
	m.openEditModels()
	m = emPress(t, m, ecEnter())
	for i := 0; i < 10; i++ {
		m = emPress(t, m, ecUp())
	}
	m = emPress(t, m, ecEnter())
	if got := fieldValue(m.editModels.fields, fldModelPlan); got != "" {
		t.Fatalf("the first option must clear the field, got %q", got)
	}
	if out := m.renderEditModels(160); !strings.Contains(out, "Claude Code default") {
		t.Errorf("blank plan must read as Claude Code's default:\n%s", out)
	}
	if blankModelLabel(fldModelVerify) != "Same as implementation" || blankModelLabel(fldModelCommentFix) != "Same as implementation" {
		t.Error("verify and comment-fix fall back to the implementation model, and must say so")
	}
}

// emPickLast opens the focused stage's picker and moves to its last option.
func emPickLast(t *testing.T, m monitorModel) monitorModel {
	t.Helper()
	m = emPress(t, m, ecEnter())
	for i := 0; i < 20; i++ {
		m = emPress(t, m, ecDown())
	}
	return m
}

// "Add a model…" saves any name to every stage's list and sets it on this
// stage; Save persists the list. Esc while typing cancels the add.
func TestEditModelsAddSavedModel(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelImpl: "haiku"})
	m.openEditModels()
	m = emPress(t, m, ecDown()) // impl
	m = emPickLast(t, m)        // no saved models yet: Add is last
	m = emPress(t, m, ecEnter())
	if !m.editModels.adding {
		t.Fatal("Add a model… must open the text field")
	}
	m = emType(t, m, "claude-nope")
	m = emPress(t, m, ecEsc())
	if m.editModels.adding || len(m.editModels.saved) != 0 || fieldValue(m.editModels.fields, fldModelImpl) != "haiku" {
		t.Fatalf("esc must cancel the add: adding=%v saved=%v", m.editModels.adding, m.editModels.saved)
	}

	m = emPickLast(t, m)
	m = emPress(t, m, ecEnter())
	m = emType(t, m, " claude-opus-4-6 ")
	m = emPress(t, m, ecEnter())
	if got := fieldValue(m.editModels.fields, fldModelImpl); got != "claude-opus-4-6" {
		t.Fatalf("the added model must be set on this stage, got %q", got)
	}
	m = emPress(t, m, ecUp(), ecEnter()) // the plan stage's picker offers it too
	if out := stripAnsiStr(m.renderEditModels(160)); !strings.Contains(out, "claude-opus-4-6  saved") {
		t.Fatalf("a saved model must be offered on every stage:\n%s", out)
	}
	m = emPress(t, m, ecEsc())
	for m.editModels.cursor < m.editModels.saveIdx() {
		m = emPress(t, m, ecDown())
	}
	m = emPress(t, m, ecEnter())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ModelImpl != "claude-opus-4-6" || len(cfg.SavedModels) != 1 || cfg.SavedModels[0] != "claude-opus-4-6" {
		t.Fatalf("saved: impl=%q list=%v", cfg.ModelImpl, cfg.SavedModels)
	}
}

// "Remove a saved model…" drops a name from the list; a stage already set to
// it keeps the value, shown as its current setting.
func TestEditModelsRemoveSavedModel(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelImpl: "claude-a", SavedModels: []string{"claude-a", "claude-b"}})
	m.openEditModels()
	m = emPickLast(t, m) // plan stage; Remove is last once something is saved
	m = emPress(t, m, ecEnter())
	if !m.editModels.removing {
		t.Fatal("Remove a saved model… must open the saved list")
	}
	m = emPress(t, m, ecEnter(), ecEsc()) // remove claude-a
	if len(m.editModels.saved) != 1 || m.editModels.saved[0] != "claude-b" {
		t.Fatalf("saved = %v, want only claude-b", m.editModels.saved)
	}
	m = emPress(t, m, ecDown(), ecEnter()) // impl picker
	if out := stripAnsiStr(m.renderEditModels(160)); !strings.Contains(out, "claude-a  (current)  current setting") {
		t.Fatalf("a stage set to a removed model keeps it as its current setting:\n%s", out)
	}
}

// A company's curated /model list (Claude Code's modelPicker) leads, with its
// labels, ahead of the aliases; replacing the built-in lineup drops them.
func TestEditModelsCompanyListLeads(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelImpl: "claude-haiku-4-5"})
	stubModelPicker(t, agent.ModelPicker{File: "/managed-settings.json", Rows: []agent.PickerModel{
		{Model: "claude-haiku-4-5", Label: "Haiku 4.5"}, {Model: "claude-opus-4-6"}}})
	m.openEditModels()
	m = emPress(t, m, ecDown())
	out := stripAnsiStr(m.renderEditModels(160))
	if !strings.Contains(out, "Haiku 4.5 · claude-haiku-4-5") || !strings.Contains(out, "/managed-settings.json") {
		t.Fatalf("the stage must show the company label and the list's source:\n%s", out)
	}
	opts := m.editModels.options(m.editModels.fields[1])
	var got []string
	for _, o := range opts {
		got = append(got, o.label)
	}
	want := []string{"Claude Code default", "Haiku 4.5", "claude-opus-4-6", "opus", "sonnet", "haiku"}
	if len(got) < len(want) || strings.Join(got[:len(want)], "|") != strings.Join(want, "|") {
		t.Fatalf("options = %v, want %v first", got, want)
	}

	stubModelPicker(t, agent.ModelPicker{ReplaceBuiltIns: true, Rows: []agent.PickerModel{{Model: "claude-haiku-4-5", Label: "Haiku 4.5"}}})
	m.openEditModels()
	for _, o := range m.editModels.options(m.editModels.fields[0]) {
		if o.value == "opus" || o.value == "sonnet" || o.value == "haiku" {
			t.Fatalf("a list that replaces the built-in lineup must not add aliases: %+v", o)
		}
	}
}

// Picking a model starts one background check per session; its result shows
// on the stage row: what ran, a refusal, or a different model than asked.
func TestEditModelsCheckShowsOnRow(t *testing.T) {
	m := editConfigModel(t, &config.Config{})
	m.openEditModels()
	m = emPress(t, m, ecEnter(), ecDown()) // plan picker on "opus"
	mm, cmd := m.updateEditModels(ecEnter())
	m = mm.(monitorModel)
	if cmd == nil || !m.modelChecks["opus"].pending {
		t.Fatalf("picking a model must start its check: cmd=%v state=%+v", cmd != nil, m.modelChecks["opus"])
	}
	if !strings.Contains(stripAnsiStr(m.renderEditModels(140)), "opus  checking…") {
		t.Fatalf("a running check must show:\n%s", stripAnsiStr(m.renderEditModels(140)))
	}
	m = emPress(t, m, ecEnter())
	if _, cmd := m.updateEditModels(ecEnter()); cmd != nil {
		t.Fatal("a model already checked this session must not be checked again")
	}

	for _, tc := range []struct {
		res  agent.ModelCheck
		want string
	}{
		{agent.ModelCheck{Resolved: "claude-opus-4-6"}, "opus  ✓ claude-opus-4-6"},
		{agent.ModelCheck{Resolved: "claude-sonnet-5"}, "opus  ⚠ ran claude-sonnet-5 instead"},
		{agent.ModelCheck{Err: "There's an issue with the selected model"}, "opus  ✗ There's an issue"},
	} {
		got, _ := m.Update(modelCheckMsg{model: "opus", result: tc.res})
		if out := stripAnsiStr(got.(monitorModel).renderEditModels(160)); !strings.Contains(out, tc.want) {
			t.Errorf("result %+v: want %q on the row:\n%s", tc.res, tc.want, out)
		}
	}
}

// The wizard and deep-link form still edit everything in one list: the split
// must compose back to the full field set, models included.
func TestConfigFieldsStillComposeTheFullSet(t *testing.T) {
	cfg := &config.Config{ModelPlan: "a", ModelImpl: "b", ModelVerify: "c", ModelReview: "d", ModelCommentFix: "e"}
	f := configFields(cfg)
	if len(f) != 10 {
		t.Fatalf("configFields = %d rows, want 10 (5 general + 5 models)", len(f))
	}
	for key, want := range map[string]string{
		fldModelPlan: "a", fldModelImpl: "b", fldModelVerify: "c",
		fldModelReview: "d", fldModelCommentFix: "e",
	} {
		if got := fieldValue(f, key); got != want {
			t.Errorf("composed field %s = %q, want %q", key, got, want)
		}
	}
}
