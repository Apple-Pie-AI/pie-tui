package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

// The models link leads the edit-config screen (row 0, the default cursor):
// one Enter opens the models screen with exactly the five per-stage fields,
// values loaded from the config.
func TestEditConfigModelsLinkOpensModelsScreen(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelImpl: "haiku", ModelCommentFix: "opus"})
	mm, _ := m.updateEditConfig(ecEnter())
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
	for _, want := range []string{"Edit models per stage", "planning stage", "review-comment fixes", "Save changes"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
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
	// Cursor starts on field 0 (plan); move to comment-fix (field 4) and type.
	for i := 0; i < 4; i++ {
		mm, _ := m.updateEditModels(ecDown())
		m = mm.(monitorModel)
	}
	for _, r := range "opus" {
		mm, _ := m.updateEditModels(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(monitorModel)
	}
	if got := fieldValue(m.editModels.fields, fldModelCommentFix); got != "opus" {
		t.Fatalf("comment-fix field = %q, want the typed text", got)
	}
	mm, _ := m.updateEditModels(ecDown()) // Save row
	m = mm.(monitorModel)
	mm, _ = m.updateEditModels(ecEnter())
	m = mm.(monitorModel)
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
	mm, _ := m.updateEditModels(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	m = mm.(monitorModel)
	if !strings.Contains(fieldValue(m.editModels.fields, fldModelPlan), "Z") {
		t.Fatal("field edit did not register before esc")
	}
	mm, _ = m.updateEditModels(ecEsc())
	m = mm.(monitorModel)
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
