package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

func editConfigModel(t *testing.T, cfg *config.Config) monitorModel {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var m monitorModel
	m.openEditConfig()
	return m
}

func ecEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func ecDown() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }
func ecEsc() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

// actConfig opens the merged screen directly - one hop from the dashboard,
// with the allowlist as a link (not inline) and the fields right below it.
func TestActConfigOpensEditConfigScreen(t *testing.T) {
	m := editConfigModel(t, &config.Config{Concurrency: 3})
	mm, _ := m.doAction(actConfig)
	hub := mm.(monitorModel)
	if hub.view != viewEditConfig {
		t.Fatalf("view = %v, want viewEditConfig", hub.view)
	}
	if hub.editConfig.cursor != 0 {
		t.Errorf("cursor = %d, want 0 (the allowlist link leads)", hub.editConfig.cursor)
	}
	// 5 inline fields: repo path, branch, base, the plan-review toggle, and
	// the review-before-PR toggle (no Jira). The five per-stage models have
	// their own dashboard row.
	if len(hub.editConfig.cfgFields) != 5 {
		t.Errorf("cfgFields = %v, want 5", hub.editConfig.cfgFields)
	}
	for _, f := range hub.editConfig.cfgFields {
		if strings.Contains(f.label, "Jira") {
			t.Errorf("config fields must not contain a Jira field: %q", f.label)
		}
		if strings.Contains(f.label, "Model") {
			t.Errorf("model fields live on their own screen now: %q", f.label)
		}
	}
	rendered := hub.renderEditConfig(80)
	if !strings.Contains(rendered, "Edit command allowlist") {
		t.Errorf("render missing the allowlist link:\n%s", rendered)
	}
	if strings.Contains(rendered, "Edit models") {
		t.Errorf("models moved to the dashboard; Edit config must not link them:\n%s", rendered)
	}
}

// The allowlist link is row 0, the default cursor.
func TestEditConfigLinkRowOpensAllowlist(t *testing.T) {
	m := editConfigModel(t, &config.Config{})
	mm, _ := m.updateEditConfig(ecEnter())
	hub := mm.(monitorModel)
	if hub.view != viewPermissions {
		t.Fatalf("view = %v, want viewPermissions", hub.view)
	}
}

// A field row edits directly (typing, no separate "enter edit mode"); Save
// persists it and never touches extra_allowed_tools - that's the allowlist
// screen's job, applied independently.
func TestEditConfigFieldEditsAndSaves(t *testing.T) {
	m := editConfigModel(t, &config.Config{ExtraAllowedTools: "Bash(python3:*)"})
	mm, _ := m.updateEditConfig(ecDown()) // past the allowlist link, onto field 0
	m = mm.(monitorModel)
	for _, r := range "/tmp/newrepo" {
		mm, _ = m.updateEditConfig(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = mm.(monitorModel)
	}
	if m.editConfig.cfgFields[0].value != "/tmp/newrepo" {
		t.Fatalf("field value = %q, want the typed text", m.editConfig.cfgFields[0].value)
	}
	for m.editConfig.cursor < m.editConfig.saveIdx() {
		mm, _ = m.updateEditConfig(ecDown())
		m = mm.(monitorModel)
	}
	mm, _ = m.updateEditConfig(ecEnter())
	m = mm.(monitorModel)
	if m.view != viewDashboard {
		t.Fatalf("view after save = %v, want viewDashboard", m.view)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].Path != "/tmp/newrepo" {
		t.Fatalf("repos = %v, want the edited path saved", cfg.Repos)
	}
	if cfg.ExtraAllowedTools != "Bash(python3:*)" {
		t.Fatalf("extra_allowed_tools = %q, the fields screen must never touch it", cfg.ExtraAllowedTools)
	}
}

// Esc discards field edits: nothing is written to disk.
func TestEditConfigEscDiscards(t *testing.T) {
	m := editConfigModel(t, &config.Config{ModelPlan: "haiku"})
	mm, _ := m.updateEditConfig(ecDown()) // past the allowlist link, onto field 0 (repo path)
	m = mm.(monitorModel)
	mm, _ = m.updateEditConfig(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Z")})
	m = mm.(monitorModel)
	if !strings.Contains(m.editConfig.cfgFields[0].value, "Z") {
		t.Fatal("field edit did not register before esc")
	}
	mm, _ = m.updateEditConfig(ecEsc())
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
