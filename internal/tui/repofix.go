// The fix-a-broken-repo-path screen. When the run preflight (launchOrClose)
// finds the configured repo invalid, it bounces here instead of dead-ending on a
// failed run: pick a local checkout Pie found nearby, clone your repo from a URL,
// or drop into the config form to edit the path by hand.
package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
)

// openRepoFix builds the screen from the current config and the preflight error.
// reason is surfaced verbatim so the user sees exactly what was wrong; the
// suggestions are discovered fresh each time (the filesystem may have changed
// since the last bounce). "Create a new git repo here" is offered only when a
// git init at the target would actually make a new repo. The cursor lands on the
// first row, so enter does the most useful thing (top suggestion, else create).
func (m *monitorModel) openRepoFix(reason string) {
	target := ""
	if cfg, err := config.Load(); err == nil && len(cfg.Repos) > 0 {
		target = config.Expand(cfg.Repos[0].Path)
	}
	actions := []repoFixAction{}
	if canCreateRepo(target) {
		actions = append(actions, fixCreate)
	}
	actions = append(actions, fixClone, fixEdit)
	m.repofix = repoFixState{
		reason:      reason,
		target:      target,
		suggestions: git.SuggestRepos(target),
		actions:     actions,
	}
	m.view = viewRepoFix
}

// canCreateRepo reports whether `git init` at target would produce a new repo:
// the path is either absent (init creates it) or a plain directory that isn't a
// repo yet. A file sitting at the path, or a path that's already a repo, can't be
// created into - offering it there would only mislead.
func canCreateRepo(target string) bool {
	if target == "" {
		return false
	}
	info, err := os.Stat(target)
	if err != nil {
		return os.IsNotExist(err)
	}
	return info.IsDir() && !git.IsRepo(target)
}

func (m monitorModel) updateRepoFix(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A clone is in flight: ignore input until cloneDoneMsg lands, so a stray key
	// can't start a second clone or navigate away mid-checkout.
	if m.repofix.cloning {
		return m, nil
	}
	if m.repofix.urlMode {
		return m.updateRepoFixURL(msg)
	}
	switch msg.String() {
	case "esc", "q":
		m.repofix.reset()
		m.view = viewDashboard
		return m, nil
	case "up", "k":
		if m.repofix.cursor > 0 {
			m.repofix.cursor--
		}
	case "down", "j":
		if m.repofix.cursor < m.repofix.lastRow() {
			m.repofix.cursor++
		}
	case "n": // shortcut: new (local) repo, when offered
		if m.repofix.createRow() >= 0 {
			return m.createRepoHere(), nil
		}
	case "c": // shortcut straight to the clone-URL input
		m.repofix.urlMode = true
	case "e": // shortcut straight to manual editing
		m.openConfigFormFocused(fldRepoPath)
	case "enter":
		switch m.repofix.cursor {
		case m.repofix.createRow():
			return m.createRepoHere(), nil
		case m.repofix.cloneRow():
			m.repofix.urlMode = true
		case m.repofix.editRow():
			m.openConfigFormFocused(fldRepoPath)
		default:
			return m.applyRepoPath(m.repofix.suggestions[m.repofix.cursor].Path), nil
		}
	}
	return m, nil
}

// updateRepoFixURL handles the clone-URL sub-input.
func (m monitorModel) updateRepoFixURL(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Paste {
		m.repofix.url.insert(strings.ReplaceAll(string(msg.Runes), "\n", ""))
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.repofix.urlMode = false
		return m, nil
	case tea.KeyEnter:
		url := strings.TrimSpace(m.repofix.url.value)
		if url == "" || m.repofix.target == "" {
			return m, nil
		}
		m.repofix.cloning = true
		return m, m.cloneRepoCmd(url, m.repofix.target)
	}
	m.repofix.url.editKey(msg)
	return m, nil
}

// applyRepoPath points the first configured repo at path and saves, then returns
// to the dashboard. The path came from git.SuggestRepos, so it's already a real
// checkout - no re-validation needed.
func (m monitorModel) applyRepoPath(path string) monitorModel {
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}
	if len(cfg.Repos) == 0 {
		cfg.Repos = []config.Repo{{Path: path, Branch: "{ticket}-{slug}"}}
	} else {
		cfg.Repos[0].Path = path
	}
	if err := config.Save(cfg); err != nil {
		m.notice = "save failed: " + err.Error()
		return m
	}
	m.notice = "repo set to " + tildify(path) + " - launch the run again"
	m.repofix.reset()
	m.view = viewDashboard
	return m
}

// createRepoHere runs `git init` at the target and points the config at it. The
// result is a LOCAL repo with no remote: it satisfies "there's a git repo here"
// but a Pie run still fetches/pushes origin, so the notice says to add a remote
// before launching. git init is fast and local, so this runs synchronously.
func (m monitorModel) createRepoHere() monitorModel {
	dir := m.repofix.target
	if err := git.Init(dir); err != nil {
		m.notice = "git init failed: " + err.Error()
		return m
	}
	cfg, err := config.Load()
	if err != nil {
		cfg = &config.Config{}
	}
	if len(cfg.Repos) == 0 {
		cfg.Repos = []config.Repo{{Path: dir, Branch: "{ticket}-{slug}"}}
	} else {
		cfg.Repos[0].Path = dir
	}
	if err := config.Save(cfg); err != nil {
		m.notice = "save failed: " + err.Error()
		return m
	}
	m.notice = "created a git repo at " + tildify(dir) + " - add a remote (git remote add origin <url>) and push before launching"
	m.repofix.reset()
	m.view = viewDashboard
	return m
}

// cloneRepoCmd clones url into dir and, on success, points the config at it. The
// clone is a network call, so this runs as a tea.Cmd and reports via cloneDoneMsg.
func (m monitorModel) cloneRepoCmd(url, dir string) tea.Cmd {
	return func() tea.Msg {
		if err := git.Clone(url, dir); err != nil {
			return cloneDoneMsg{dir: dir, err: err}
		}
		cfg, err := config.Load()
		if err != nil {
			cfg = &config.Config{}
		}
		if len(cfg.Repos) == 0 {
			cfg.Repos = []config.Repo{{Path: dir, Branch: "{ticket}-{slug}"}}
		} else {
			cfg.Repos[0].Path = dir
		}
		if err := config.Save(cfg); err != nil {
			return cloneDoneMsg{dir: dir, err: err}
		}
		return cloneDoneMsg{dir: dir}
	}
}

func (m monitorModel) renderRepoFix(w int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  Fix the repository path") + "\n")
	b.WriteString(whyStyle.Render(truncate("  "+m.repofix.reason, w)) + "\n\n")

	if m.repofix.urlMode {
		return m.renderRepoFixURL(&b, w)
	}

	if len(m.repofix.suggestions) > 0 {
		b.WriteString(dimStyle.Render("  Did you mean one of these?") + "\n")
		for i, s := range m.repofix.suggestions {
			flag := dimStyle.Render(" (no origin remote)")
			if s.HasOrigin {
				flag = okStyle.Render(" (origin ✓)")
			}
			b.WriteString(rowLine(i == m.repofix.cursor, tildify(s.Path)+flag) + "\n")
		}
		b.WriteString("\n")
	}
	for i, a := range m.repofix.actions {
		row := len(m.repofix.suggestions) + i
		b.WriteString(rowLine(m.repofix.cursor == row, repoFixActionLabel(a)) + "\n")
	}

	hint := "↑↓ move · enter select · c: clone · e: edit · esc cancel"
	if m.repofix.createRow() >= 0 {
		hint = "↑↓ move · enter select · n: new · c: clone · e: edit · esc cancel"
	}
	b.WriteString("\n  " + dimStyle.Render(hint))
	return b.String()
}

// repoFixActionLabel is the row text for each always-present action.
func repoFixActionLabel(a repoFixAction) string {
	switch a {
	case fixCreate:
		return "Create a new git repo here (git init)"
	case fixClone:
		return "Clone from a URL instead"
	default:
		return "Edit the path manually"
	}
}

func (m monitorModel) renderRepoFixURL(b *strings.Builder, w int) string {
	b.WriteString(dimStyle.Render("  Clone into "+tildify(m.repofix.target)) + "\n\n")
	if m.repofix.cloning {
		b.WriteString(formMarkFocus.Render("  ▸ ") + "cloning " + m.repofix.url.value + " …\n")
		b.WriteString("\n  " + dimStyle.Render("please wait"))
		return b.String()
	}
	prefix := "  Git URL: "
	val := formValFocus.Render(truncate(m.repofix.url.caretView(), w-lipgloss.Width(prefix)))
	b.WriteString(formMarkFocus.Render("▸ ") + formLabelFocus.Render("Git URL:") + " " + val + "\n")
	b.WriteString("\n  " + dimStyle.Render("enter: clone · esc: back"))
	return b.String()
}

// rowLine renders one pickable row with the form's focus marker/styles, so the
// repo-fix screen reads like the config form it sits in front of.
func rowLine(focused bool, text string) string {
	if focused {
		return formMarkFocus.Render("▸ ") + formValFocus.Render(text)
	}
	return "  " + formValStyle.Render(text)
}

// tildify collapses the user's home dir back to ~ for display only. The stored
// path stays absolute; this just keeps the screen readable.
func tildify(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if p == home {
			return "~"
		}
		if strings.HasPrefix(p, home+string(filepath.Separator)) {
			return "~" + p[len(home):]
		}
	}
	return p
}
