package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
)

func permModel(t *testing.T, cfg *config.Config) monitorModel {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	var m monitorModel
	m.openPermissions()
	return m
}

func permEnter() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func permDown() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyDown} }
func permEsc() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEsc} }

func permKeys(t *testing.T, m monitorModel, msgs ...tea.KeyMsg) monitorModel {
	t.Helper()
	for _, msg := range msgs {
		mm, _ := m.updatePermissions(msg)
		m = mm.(monitorModel)
	}
	return m
}

// openPermissions() opens the allowlist screen directly.
func TestOpenPermissionsOpensScreen(t *testing.T) {
	m := permModel(t, &config.Config{})
	if m.view != viewPermissions {
		t.Fatalf("view = %v, want viewPermissions", m.view)
	}
}

// Opening with 3 extra rules shows 3 rows plus the baseline count.
func TestOpenPermissionsListsExtraRulesAndBaseline(t *testing.T) {
	m := permModel(t, &config.Config{
		ExtraAllowedTools: "Bash(python3:*) Bash(git diff:*) Skill",
	})
	m.height = 60
	if len(m.permissions.rows) != 3 {
		t.Fatalf("rows = %v, want 3", m.permissions.rows)
	}
	if m.permissions.baselineSrc != "default" || len(m.permissions.baseline) == 0 {
		t.Errorf("baseline = %q (%d rules), want the default baseline", m.permissions.baselineSrc, len(m.permissions.baseline))
	}
	rendered := m.renderPermissions(100)
	for _, want := range []string{"Bash(python3:*)", "Bash(git diff:*)", "Skill"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Save changes") {
		t.Errorf("the staged-save row is gone from this design:\n%s", rendered)
	}
}

// Enter on a rule opens the remove/keep dialog showing the exact rule;
// choosing Remove deletes it from the config IMMEDIATELY and returns to the
// (still-open) list.
func TestRemoveRuleViaDialog(t *testing.T) {
	m := permModel(t, &config.Config{
		ExtraAllowedTools: "Bash(python3:*) Bash(git diff:*)",
	})
	m.height = 60
	m = permKeys(t, m, permEnter()) // dialog over row 0
	if m.permissions.confirm != 0 {
		t.Fatal("enter on a rule row did not open the dialog")
	}
	rendered := m.renderPermissions(100)
	for _, want := range []string{"Remove this rule?", "Bash(python3:*)", "Remove it", "Keep it"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("dialog missing %q:\n%s", want, rendered)
		}
	}
	m = permKeys(t, m, permEnter()) // cursor 0 = Remove it
	if m.view != viewPermissions {
		t.Fatalf("view = %v, want to stay on viewPermissions after a removal", m.view)
	}
	if len(m.permissions.rows) != 1 || m.permissions.rows[0] != "Bash(git diff:*)" {
		t.Fatalf("rows = %v, want only the kept rule", m.permissions.rows)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtraAllowedTools != "Bash(git diff:*)" {
		t.Fatalf("extra_allowed_tools = %q - the removal was not written immediately", cfg.ExtraAllowedTools)
	}
}

// Choosing Keep (or pressing Esc) closes the dialog and changes nothing,
// on screen or on disk.
func TestDialogKeepAndEscChangeNothing(t *testing.T) {
	orig := "Bash(python3:*) Bash(git diff:*)"
	m := permModel(t, &config.Config{ExtraAllowedTools: orig})

	m = permKeys(t, m, permEnter(), permDown(), permEnter()) // dialog → Keep it
	if m.permissions.confirm != -1 || len(m.permissions.rows) != 2 {
		t.Fatalf("keep: dialog=%d rows=%v, want closed dialog and 2 rows", m.permissions.confirm, m.permissions.rows)
	}

	m = permKeys(t, m, permEnter(), permEsc()) // dialog → esc
	if m.permissions.confirm != -1 || len(m.permissions.rows) != 2 {
		t.Fatalf("esc: dialog=%d rows=%v, want closed dialog and 2 rows", m.permissions.confirm, m.permissions.rows)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtraAllowedTools != orig {
		t.Fatalf("extra_allowed_tools = %q, want untouched %q", cfg.ExtraAllowedTools, orig)
	}
}

// Arrow-only navigation must visit every rule row before landing on Add -
// not skip from the first row to the end.
func TestArrowsAloneVisitEveryRuleRow(t *testing.T) {
	m := permModel(t, &config.Config{ExtraAllowedTools: "Bash(a:*) Bash(b:*) Bash(c:*)"})
	var visited []int
	for i := 0; i < m.permissions.rowCount(); i++ {
		visited = append(visited, m.permissions.cursor)
		m = permKeys(t, m, permDown())
	}
	want := []int{0, 1, 2, 3} // 3 rule rows, then Add (3)
	if len(visited) != len(want) {
		t.Fatalf("visited = %v, want %v", visited, want)
	}
	for i := range want {
		if visited[i] != want[i] {
			t.Errorf("visited[%d] = %d, want %d (full: %v)", i, visited[i], want[i], visited)
		}
	}
}

// Adding a valid rule appends a row AND writes the config immediately - the
// same no-separate-save contract as removal.
func TestAddValidRuleSavesImmediately(t *testing.T) {
	m := permModel(t, &config.Config{})
	m = permKeys(t, m, permEnter()) // cursor 0 = Add row (no rules yet)
	if !m.permissions.adding {
		t.Fatal("enter on Add row did not open the input")
	}
	for _, r := range "Bash(python3:*)" {
		m = permKeys(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = permKeys(t, m, permEnter())
	if m.permissions.adding {
		t.Fatal("add input still open after submit")
	}
	if len(m.permissions.rows) != 1 || m.permissions.rows[0] != "Bash(python3:*)" {
		t.Fatalf("rows = %v, want one Bash(python3:*)", m.permissions.rows)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtraAllowedTools != "Bash(python3:*)" {
		t.Fatalf("extra_allowed_tools = %q - the add was not written immediately", cfg.ExtraAllowedTools)
	}
}

// An invalid rule shows an inline error, adds nothing, writes nothing, and
// keeps the input open for correction.
func TestAddInvalidRuleShowsError(t *testing.T) {
	m := permModel(t, &config.Config{})
	m = permKeys(t, m, permEnter())
	for _, r := range "Unable to run this" {
		m = permKeys(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = permKeys(t, m, permEnter())
	if len(m.permissions.rows) != 0 {
		t.Fatalf("rows = %v, want none added", m.permissions.rows)
	}
	if m.permissions.err == "" {
		t.Fatal("no inline error shown for an invalid rule")
	}
	if cfg, _ := config.Load(); cfg.ExtraAllowedTools != "" {
		t.Fatalf("invalid rule reached the config: %q", cfg.ExtraAllowedTools)
	}
}

// Adding a rule already present is rejected with an inline error, not a
// silent duplicate row.
func TestAddDuplicateRuleRejected(t *testing.T) {
	m := permModel(t, &config.Config{ExtraAllowedTools: "Bash(python3:*)"})
	for m.permissions.cursor < m.permissions.addIdx() {
		m = permKeys(t, m, permDown())
	}
	m = permKeys(t, m, permEnter())
	for _, r := range "Bash(python3:*)" {
		m = permKeys(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	m = permKeys(t, m, permEnter())
	if len(m.permissions.rows) != 1 {
		t.Fatalf("rows = %v, want the duplicate rejected (still just 1)", m.permissions.rows)
	}
	if m.permissions.err == "" {
		t.Fatal("no inline error shown for a duplicate rule")
	}
}

// Esc with no dialog open leaves the screen; browsing alone writes nothing.
func TestEscClosesWithoutWriting(t *testing.T) {
	orig := "Bash(python3:*) Bash(git diff:*)"
	m := permModel(t, &config.Config{ExtraAllowedTools: orig})
	m = permKeys(t, m, permDown(), permEsc())
	if m.view != viewDashboard {
		t.Fatalf("view after esc = %v, want viewDashboard", m.view)
	}
	if cfg, _ := config.Load(); cfg.ExtraAllowedTools != orig {
		t.Fatalf("browsing wrote a change: extra_allowed_tools = %q", cfg.ExtraAllowedTools)
	}
}

// The row-index helpers are what every other test navigates by - pin their
// arithmetic directly.
func TestPermissionsRowIndexHelpers(t *testing.T) {
	m := permModel(t, &config.Config{ExtraAllowedTools: "Bash(a:*) Bash(b:*)"})
	p := &m.permissions
	if got := len(p.rows); got != 2 {
		t.Fatalf("rows = %d, want 2", got)
	}
	if got := p.addIdx(); got != 2 {
		t.Errorf("addIdx() = %d, want 2", got)
	}
	if got := p.rowCount(); got != 3 {
		t.Errorf("rowCount() = %d, want 3", got)
	}
}

// The screen explains itself: section titles say WHICH baseline is active
// and where to change it, and an empty extras section says so plainly -
// a first-time viewer should not need the docs to parse what they see.
func TestRenderExplainsBaselineAndEmptyExtras(t *testing.T) {
	// Default baseline, no extras.
	m := permModel(t, &config.Config{})
	m.height = 60
	rendered := m.renderPermissions(120)
	for _, want := range []string{
		"Default allowlist",
		"to change it, edit allowed_tools in ~/.pie/config.toml",
		"Your extra rules (0)",
		"(you haven't added any rules yet)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("default render missing %q:\n%s", want, rendered)
		}
	}

	// Custom baseline: the title must say so and explain the way back.
	m = permModel(t, &config.Config{AllowedTools: "Edit Bash(ls:*)"})
	m.height = 60
	rendered = m.renderPermissions(120)
	for _, want := range []string{
		"Custom allowlist (2 rules)",
		"delete that line to restore the default",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("custom render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "Default allowlist") {
		t.Errorf("custom baseline must not be titled Default:\n%s", rendered)
	}

	// With extras present, the empty-state line disappears.
	m = permModel(t, &config.Config{ExtraAllowedTools: "Bash(python3:*)"})
	m.height = 60
	rendered = m.renderPermissions(120)
	if strings.Contains(rendered, "haven't added any rules") {
		t.Errorf("non-empty extras still shows the empty-state line:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Your extra rules (1)") {
		t.Errorf("extras title lost its count:\n%s", rendered)
	}
}

// The field bug this windowing exists for: a short terminal with a long list
// used to render rows straight off the bottom edge - the cursor reached them,
// the eye never did. The window must keep the CURSOR's row visible and mark
// what is cut off in both directions.
func TestLongListScrollsToKeepCursorVisible(t *testing.T) {
	rules := make([]string, 30)
	for i := range rules {
		rules[i] = "Bash(tool" + string(rune('a'+i%26)) + ":*)"
	}
	m := permModel(t, &config.Config{ExtraAllowedTools: strings.Join(rules, " ")})
	m.height = 16 // far too short for baseline + 30 extras

	// Walk the cursor to the last rule row; its rendering must be visible.
	for m.permissions.cursor < len(m.permissions.rows)-1 {
		m = permKeys(t, m, permDown())
	}
	rendered := m.renderPermissions(100)
	if !strings.Contains(rendered, "more above") {
		t.Errorf("scrolled-down view lacks the above marker:\n%s", rendered)
	}
	sel := selStyle.Render(" " + m.permissions.rows[len(m.permissions.rows)-1])
	if !strings.Contains(rendered, sel) {
		t.Errorf("the focused last rule is not visible in the window:\n%s", rendered)
	}
	if got := strings.Count(rendered, "\n"); got > m.height {
		t.Errorf("rendered %d lines into a %d-line terminal", got, m.height)
	}

	// On the FIRST rule row, the long baseline above is (correctly) cut, the
	// tail of the rules is cut below, and the focused row itself is visible.
	m.permissions.cursor = 0
	rendered = m.renderPermissions(100)
	if !strings.Contains(rendered, "more below") {
		t.Errorf("first-rule view lacks the below marker:\n%s", rendered)
	}
	if !strings.Contains(rendered, selStyle.Render(" "+m.permissions.rows[0])) {
		t.Errorf("the focused first rule is not visible in the window:\n%s", rendered)
	}
}

// An unmeasured terminal (height 0, e.g. before the first WindowSizeMsg)
// windows nothing - never hide rows on a guess.
func TestUnmeasuredTerminalShowsEverything(t *testing.T) {
	m := permModel(t, &config.Config{ExtraAllowedTools: "Bash(a:*) Bash(b:*)"})
	m.height = 0
	rendered := m.renderPermissions(100)
	if strings.Contains(rendered, "more above") || strings.Contains(rendered, "more below") {
		t.Errorf("height 0 must not window:\n%s", rendered)
	}
	for _, r := range m.permissions.baseline {
		if !strings.Contains(rendered, r) {
			t.Errorf("baseline rule %q hidden at height 0", r)
		}
	}
}
