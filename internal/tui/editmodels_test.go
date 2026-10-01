package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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
	if out := stripAnsiStr(m.renderEditModels(120)); !strings.Contains(out, "▸ fable") {
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
	// "opus": options are [Same as implementation, fable, opus, ...].
	m = emPress(t, m, ecDown(), ecDown(), ecDown(), ecDown(), ecEnter(), ecDown(), ecDown(), ecEnter())
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
	opts := modelOptions(fldModelPlan)
	if !m.editModels.picking || opts[m.editModels.pickSel].value != "sonnet" {
		t.Fatalf("picker must open on the current value: picking=%v sel=%d", m.editModels.picking, m.editModels.pickSel)
	}
	if out := m.renderEditModels(120); !strings.Contains(out, "sonnet  (current)") || !strings.Contains(out, "Custom…") {
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
	out := m.renderEditModels(160)
	if !strings.Contains(out, "Claude Code default") {
		t.Errorf("blank plan must read as Claude Code's default:\n%s", out)
	}
	if blankModelLabel(fldModelVerify) != "Same as implementation" || blankModelLabel(fldModelCommentFix) != "Same as implementation" {
		t.Error("verify and comment-fix fall back to the implementation model, and must say so")
	}
}

// Custom… opens free text: typing sets any id, Enter keeps it, and a custom
// id reopens on Custom… with its text ready to edit. Esc restores the value.
func TestEditModelsCustomID(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelImpl: "haiku"})
	m.openEditModels()
	m = emPress(t, m, ecDown(), ecEnter()) // impl row, picker on haiku
	for i := 0; i < 10; i++ {
		m = emPress(t, m, ecDown())
	}
	m = emPress(t, m, ecEnter())
	if !m.editModels.editing || fieldValue(m.editModels.fields, fldModelImpl) != "" {
		t.Fatalf("Custom… must open an empty text field over an alias: editing=%v value=%q",
			m.editModels.editing, fieldValue(m.editModels.fields, fldModelImpl))
	}
	m = emType(t, m, "claude-opus-5-5")
	m = emPress(t, m, ecEnter())
	if m.editModels.editing || fieldValue(m.editModels.fields, fldModelImpl) != "claude-opus-5-5" {
		t.Fatalf("enter must keep the typed id: editing=%v value=%q", m.editModels.editing, fieldValue(m.editModels.fields, fldModelImpl))
	}

	m = emPress(t, m, ecEnter()) // reopen: lands on Custom…, showing the id
	if opts := modelOptions(fldModelImpl); m.editModels.pickSel != len(opts)-1 {
		t.Fatalf("a custom id must reopen on Custom…, sel=%d", m.editModels.pickSel)
	}
	if out := m.renderEditModels(120); !strings.Contains(out, "Custom… · claude-opus-5-5  (current)") {
		t.Fatalf("the picker must show the current custom id:\n%s", out)
	}
	m = emPress(t, m, ecEnter()) // edit it: text kept, not cleared
	m = emType(t, m, "-x")
	m = emPress(t, m, ecEsc())
	if got := fieldValue(m.editModels.fields, fldModelImpl); got != "claude-opus-5-5" {
		t.Fatalf("esc in the custom field must restore the previous id, got %q", got)
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
