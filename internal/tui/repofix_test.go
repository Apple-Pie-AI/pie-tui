package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

func rfKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

// saveBadRepo saves a config whose repo path doesn't exist and returns that path.
func saveBadRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(t.TempDir(), "no-such-repo")
	if err := config.Save(&config.Config{
		Concurrency: 3,
		Repos:       []config.Repo{{Path: bad, Branch: "{ticket}-{slug}"}},
	}); err != nil {
		t.Fatal(err)
	}
	return bad
}

func TestOpenRepoFix(t *testing.T) {
	bad := saveBadRepo(t)
	m := monitorModel{}
	m.openRepoFix("repository path does not exist")

	if m.view != viewRepoFix {
		t.Fatalf("view = %d, want viewRepoFix", m.view)
	}
	if m.repofix.reason != "repository path does not exist" {
		t.Errorf("reason = %q", m.repofix.reason)
	}
	if m.repofix.target != bad {
		t.Errorf("target = %q, want %q", m.repofix.target, bad)
	}
	// The target doesn't exist, so a `git init` there is meaningful - the create
	// row must be offered, first among the actions, followed by clone then edit.
	n := len(m.repofix.suggestions)
	if m.repofix.createRow() != n {
		t.Errorf("createRow = %d, want %d (first action, after the suggestions)", m.repofix.createRow(), n)
	}
	if m.repofix.cloneRow() != n+1 || m.repofix.editRow() != n+2 {
		t.Errorf("clone/edit rows = %d/%d, want %d/%d", m.repofix.cloneRow(), m.repofix.editRow(), n+1, n+2)
	}
	if m.repofix.lastRow() != n+2 {
		t.Errorf("lastRow = %d, want %d", m.repofix.lastRow(), n+2)
	}
	if m.repofix.cursor != 0 {
		t.Errorf("cursor = %d, want 0", m.repofix.cursor)
	}
}

// The create row appears only when a `git init` at the target would make a new
// repo: hidden when the target is already a git repo.
func TestRepoFixCreateOfferedOnlyWhenUseful(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// An existing directory that is already a git repo but has no origin.
	existing := filepath.Join(t.TempDir(), "already-a-repo")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	c := exec.Command("git", "init", "-q")
	c.Dir = existing
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if err := config.Save(&config.Config{Repos: []config.Repo{{Path: existing, Branch: "b"}}}); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{}
	m.openRepoFix("no origin remote")
	if m.repofix.createRow() != -1 {
		t.Error("create must not be offered when the target is already a git repo")
	}
}

// Selecting "Create a new git repo here" runs git init at the target, points the
// config at it, and returns to the dashboard with a guiding notice.
func TestRepoFixCreate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "new-app") // does not exist yet
	if err := config.Save(&config.Config{Repos: []config.Repo{{Path: target, Branch: "b"}}}); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{}
	m.openRepoFix("repository path does not exist")
	m.repofix.cursor = m.repofix.createRow()
	mm, _ := m.updateRepoFix(tea.KeyMsg{Type: tea.KeyEnter})
	hub := mm.(monitorModel)

	if hub.view != viewDashboard {
		t.Errorf("create should return to the dashboard, view=%d", hub.view)
	}
	if !git.IsRepo(target) {
		t.Error("git init did not create a repo at the target")
	}
	if !strings.Contains(hub.notice, "created a git repo") || !strings.Contains(hub.notice, "add a remote") {
		t.Errorf("notice should confirm creation and mention adding a remote, got %q", hub.notice)
	}
}

func TestRepoFixCloneAndEditRows(t *testing.T) {
	saveBadRepo(t)
	m := monitorModel{}
	m.openRepoFix("boom")

	// enter on the clone row → URL input mode.
	m.repofix.cursor = m.repofix.cloneRow()
	mm, _ := m.updateRepoFix(tea.KeyMsg{Type: tea.KeyEnter})
	hub := mm.(monitorModel)
	if !hub.repofix.urlMode {
		t.Fatal("enter on the clone row should switch to URL input")
	}
	// esc backs out of URL mode to the picker.
	mm, _ = hub.updateRepoFix(tea.KeyMsg{Type: tea.KeyEsc})
	hub = mm.(monitorModel)
	if hub.repofix.urlMode {
		t.Error("esc should leave URL mode")
	}

	// 'e' jumps to manual editing: the config form focused on the repo path.
	mm, _ = hub.updateRepoFix(rfKey('e'))
	hub = mm.(monitorModel)
	if hub.view != viewForm || hub.form == nil {
		t.Fatalf("'e' should open the config form; view=%d", hub.view)
	}
	if hub.form.focused().key != fldRepoPath {
		t.Errorf("form should focus the repo path field, got %q", hub.form.focused().key)
	}
}

func TestRepoFixURLEnterStartsClone(t *testing.T) {
	saveBadRepo(t)
	m := monitorModel{}
	m.openRepoFix("boom")
	m.repofix.urlMode = true

	// Empty URL: enter does nothing.
	mm, cmd := m.updateRepoFix(tea.KeyMsg{Type: tea.KeyEnter})
	hub := mm.(monitorModel)
	if cmd != nil || hub.repofix.cloning {
		t.Error("enter with an empty URL must not start a clone")
	}

	// Typed URL: enter kicks off the clone.
	hub.repofix.url.value = "https://example.com/app.git"
	mm, cmd = hub.updateRepoFix(tea.KeyMsg{Type: tea.KeyEnter})
	hub = mm.(monitorModel)
	if cmd == nil || !hub.repofix.cloning {
		t.Error("enter with a URL should start a clone (cloning=true, cmd set)")
	}
}

func TestRepoFixApplySuggestion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	// A real checkout sitting next to a bad configured path.
	parent := t.TempDir()
	real := filepath.Join(parent, "real-app")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", "https://example.com/x.git"}} {
		c := exec.Command("git", args...)
		c.Dir = real
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := config.Save(&config.Config{
		Repos: []config.Repo{{Path: filepath.Join(parent, "wrong-name"), Branch: "{ticket}-{slug}"}},
	}); err != nil {
		t.Fatal(err)
	}

	m := monitorModel{}
	m.openRepoFix("boom")
	// The real checkout must be among the suggestions; select and apply it.
	idx := -1
	for i, s := range m.repofix.suggestions {
		if s.Path == real {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("real checkout not suggested; got %+v", m.repofix.suggestions)
	}
	m.repofix.cursor = idx
	mm, _ := m.updateRepoFix(tea.KeyMsg{Type: tea.KeyEnter})
	hub := mm.(monitorModel)

	if hub.view != viewDashboard {
		t.Errorf("applying a suggestion should return to the dashboard, view=%d", hub.view)
	}
	if !strings.Contains(hub.notice, "repo set to") {
		t.Errorf("notice should confirm the repo was set, got %q", hub.notice)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Repos) == 0 || cfg.Repos[0].Path != real {
		t.Errorf("config repo path = %+v, want %q", cfg.Repos, real)
	}
}

func TestCloneDoneMsg(t *testing.T) {
	// Failure keeps the user on the repo-fix screen to retry.
	m := monitorModel{view: viewRepoFix}
	m.repofix.cloning = true
	mm, _ := m.Update(cloneDoneMsg{dir: "/x", err: os.ErrPermission})
	hub := mm.(monitorModel)
	if hub.view != viewRepoFix || hub.repofix.cloning {
		t.Errorf("failed clone should stay on repo-fix and clear cloning; view=%d cloning=%v", hub.view, hub.repofix.cloning)
	}
	if !strings.Contains(hub.notice, "clone failed") {
		t.Errorf("notice = %q, want a clone-failed message", hub.notice)
	}
}
