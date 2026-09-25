package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/jira"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// These two tests are the net under the TUI file split. Every sub-handler
// (updatePalette, updateRunStack, updatePlan, …) is covered only by tests that
// call it DIRECTLY, so deleting a case from dispatchKey's `switch m.view` or a
// case from View's parallel switch leaves the whole suite green and silently
// breaks a screen. These exercise the routers themselves.

// baseModel mirrors what launchHub builds: the five prompt indices are -1
// sentinels ("no chip is prompting"), and their zero value 0 means "chip 0 is
// confirming its ticket id" - so a bare monitorModel{} literal silently routes
// into updateRunIDPrompt instead of the handler under test.
func baseModel() monitorModel {
	return monitorModel{
		width:  100,
		height: 40,
		run: newRun(func(r *runState) {
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = -1
			r.stackPrompt = -1
			r.reviewPrompt = -1
		}),
	}
}

func TestDispatchKeyRouting(t *testing.T) {
	key := func(s string) tea.KeyMsg {
		if s == " " {
			return tea.KeyMsg{Type: tea.KeySpace}
		}
		if len(s) == 1 {
			return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
		}
		switch s {
		case "down":
			return tea.KeyMsg{Type: tea.KeyDown}
		case "up":
			return tea.KeyMsg{Type: tea.KeyUp}
		case "enter":
			return tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			return tea.KeyMsg{Type: tea.KeyEsc}
		}
		t.Fatalf("unmapped key %q", s)
		return tea.KeyMsg{}
	}

	tests := []struct {
		name  string
		model func() monitorModel
		key   string
		want  func(*testing.T, monitorModel, tea.Cmd)
	}{
		{
			name:  "dashboard down moves the cursor",
			model: func() monitorModel { m := baseModel(); m.flat = []store.Session{{Ticket: "A-1"}}; return m },
			key:   "down",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.cursor != 1 {
					t.Fatalf("cursor = %d, want 1", m.cursor)
				}
				if m.view != viewDashboard {
					t.Fatalf("view = %v, want dashboard", m.view)
				}
			},
		},
		{
			name:  "dashboard colon opens the command palette",
			model: baseModel,
			key:   ":",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.view != viewPalette {
					t.Fatalf("view = %v, want palette", m.view)
				}
				if m.paletteAgentOnly {
					t.Fatal("the : menu must include the global commands")
				}
			},
		},
		{
			name:  "palette down moves its own cursor, not the dashboard's",
			model: func() monitorModel { m := baseModel(); m.view = viewPalette; return m },
			key:   "down",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.paletteCursor != 1 {
					t.Fatalf("paletteCursor = %d, want 1", m.paletteCursor)
				}
				if m.cursor != 0 {
					t.Fatalf("dashboard cursor moved to %d; the palette swallowed the wrong key", m.cursor)
				}
			},
		},
		{
			name: "run-method down moves when Jira makes a second method exist",
			// runMethods() returns one entry unless m.jira != nil, and the guard is
			// `cursor < len(ms)-1`, so without a client this key is a no-op.
			model: func() monitorModel {
				m := baseModel()
				m.view = viewRunMethod
				m.jira = &jira.Client{}
				return m
			},
			key: "down",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.run.methodCursor != 1 {
					t.Fatalf("runMethodCursor = %d, want 1", m.run.methodCursor)
				}
			},
		},
		{
			name: "run-input in jira mode types into runText",
			model: func() monitorModel {
				m := baseModel()
				m.view, m.run.mode = viewRunInput, "jira"
				return m
			},
			key: "P",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.run.text != "P" {
					t.Fatalf("runText = %q, want %q - the key reached the wrong handler", m.run.text, "P")
				}
			},
		},
		{
			name: "output pane closes on esc",
			model: func() monitorModel {
				m := baseModel()
				m.view, m.outputText = viewOutput, "doctor says hi"
				return m
			},
			key: "esc",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.view != viewDashboard {
					t.Fatalf("view = %v, want dashboard", m.view)
				}
			},
		},
		{
			name: "plan viewer scrolls only when the plan is taller than the viewport",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewPlan
				// clampPlanScroll snaps back to 0 unless len(planLines) exceeds
				// planViewportHeight (which derives from m.height).
				m.plan.lines = make([]string, 200)
				return m
			},
			key: "down",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.plan.scroll != 1 {
					t.Fatalf("planScroll = %d, want 1", m.plan.scroll)
				}
			},
		},
		{
			name: "plan viewer left toggles the menu selection",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewPlan
				return m
			},
			key: "h",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.plan.menu != 1 {
					t.Fatalf("planMenu = %d, want 1", m.plan.menu)
				}
			},
		},
		{
			name: "form tab advances focus",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewForm
				m.form = &formModel{fields: []formField{{label: "one"}, {label: "two"}}}
				return m
			},
			key: "down",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.form == nil || m.form.focus != 1 {
					t.Fatalf("form focus = %v, want 1", m.form)
				}
			},
		},
		{
			// A key in the repo-fix screen must reach updateRepoFix: 'c' switches
			// to the clone-URL input. Guards the `case viewRepoFix` in dispatchKey.
			name: "repo fix c enters clone mode",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewRepoFix
				return m
			},
			key: "c",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if !m.repofix.urlMode {
					t.Fatal("'c' never reached updateRepoFix (urlMode still false)")
				}
			},
		},
		{
			// updateConsent has no cursor field at all - it switches on y/n/esc/q
			// only. Assert it returns a command rather than executing it, because
			// applyConsent writes ~/.pie/config.toml.
			name:  "consent n returns a save command",
			model: func() monitorModel { m := baseModel(); m.view = viewConsent; return m },
			key:   "n",
			want: func(t *testing.T, _ monitorModel, c tea.Cmd) {
				if c == nil {
					t.Fatal("consent returned no command; the key never reached updateConsent")
				}
			},
		},
		{
			// The answering overlay pre-empts the view switch entirely.
			name: "answering overlay pre-empts the active view",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewPalette // would otherwise move paletteCursor
				m.answer.open, m.answer.ticket = true, "A-1"
				return m
			},
			key: "z",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if len(m.answer.in.atoms) != 1 || m.answer.in.atoms[0].r != 'z' {
					t.Fatalf("answerAtoms = %v, want one 'z' atom", m.answer.in.atoms)
				}
				if m.paletteCursor != 0 {
					t.Fatal("the palette handled a key the answer overlay owns")
				}
			},
		},
		{
			// Guards the `case viewComments` arm in dispatchKey. enter opens the
			// focused row's menu, so it must reach updateComments.
			name: "comments enter opens the row's menu",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewComments
				m.comments.reset("A-1", "PR #1", "")
				m.comments.rows = []store.Comment{{ID: "T1", Author: "alice", Path: "a.kt", Line: 1}}
				m.comments.sel["T1"] = true
				return m
			},
			key: "enter",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.comments.mode != modeMenu {
					t.Fatal("enter never reached updateComments (no menu opened)")
				}
			},
		},
		{
			// So does the stop-confirmation dialog.
			name: "confirm dialog pre-empts the active view",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewPalette
				m.confirm.ticket = "A-1"
				return m
			},
			key: "down",
			want: func(t *testing.T, m monitorModel, _ tea.Cmd) {
				if m.confirm.cursor != 1 {
					t.Fatalf("confirmCursor = %d, want 1", m.confirm.cursor)
				}
				if m.paletteCursor != 0 {
					t.Fatal("the palette handled a key the confirm dialog owns")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, cmd := tc.model().dispatchKey(key(tc.key))
			m, ok := got.(monitorModel)
			if !ok {
				t.Fatalf("dispatchKey returned %T, want monitorModel", got)
			}
			tc.want(t, m, cmd)
		})
	}
}

// TestViewRendersEveryScreen pins View's own switch over m.view - the second,
// independent view dispatcher. Steps that move render functions into per-screen
// files must keep every arm wired; without this, a dropped case silently falls
// through to `default` and renders the dashboard for every screen.
func TestViewRendersEveryScreen(t *testing.T) {
	tests := []struct {
		name   string
		model  func() monitorModel
		expect string // a substring only this screen produces
	}{
		{
			name:   "dashboard",
			model:  baseModel,
			expect: "Start new ticket",
		},
		{
			name:   "consent",
			model:  func() monitorModel { m := baseModel(); m.view = viewConsent; return m },
			expect: "Help improve Apple Pie?",
		},
		{
			name:   "palette",
			model:  func() monitorModel { m := baseModel(); m.view = viewPalette; return m },
			expect: "actions",
		},
		{
			name:   "run method picker",
			model:  func() monitorModel { m := baseModel(); m.view = viewRunMethod; return m },
			expect: "how do you want to add tickets?",
		},
		{
			name: "output pane",
			model: func() monitorModel {
				m := baseModel()
				m.view, m.outputTitle, m.outputText = viewOutput, "doctor", "all good"
				return m
			},
			expect: "esc/enter close",
		},
		{
			name: "form",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewForm
				m.form = &formModel{title: "Edit config", fields: []formField{{label: "repo path"}}}
				return m
			},
			expect: "repo path",
		},
		{
			name: "plan viewer",
			model: func() monitorModel {
				m := baseModel()
				m.view, m.plan.ticket = viewPlan, "A-1"
				m.plan.lines = []string{"a plan line"}
				return m
			},
			expect: "a plan line",
		},
		{
			name: "confirm dialog replaces the body",
			model: func() monitorModel {
				m := baseModel()
				m.confirm.ticket = "A-1"
				return m
			},
			expect: "A-1",
		},
		{
			name: "repo fix screen",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewRepoFix
				m.repofix.reason = "repository path does not exist"
				return m
			},
			expect: "Fix the repository path",
		},
		{
			name: "review comments screen",
			model: func() monitorModel {
				m := baseModel()
				m.view = viewComments
				m.comments.reset("A-1", "PR #1", "")
				m.comments.rows = []store.Comment{{
					ID: "T1", Author: "alice", Path: "app/Login.kt", Line: 42,
					Body: "don't swallow the exception",
				}}
				m.renderFocused()
				return m
			},
			expect: "PR #1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := ansiRe.ReplaceAllString(tc.model().View(), "")
			if !strings.Contains(out, tc.expect) {
				t.Fatalf("View() for %s is missing %q.\n--- got ---\n%s", tc.name, tc.expect, out)
			}
		})
	}
}

// Enter on a collapsible section header folds/unfolds the group in place: no
// palette, no view change, and the cursor stays on the header it toggled.
func TestEnterOnSectionHeaderToggles(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := baseModel()
	m.groups = rowGroups(store.Session{Ticket: "M-1", State: store.StateMerged,
		PRURL: "https://x/pull/2"})
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}
	m.rows = buildRows(m.groups, m.expanded)
	m.cursor = len(dashCommands) // the CLOSED header is the only list row

	got, _ := m.dispatchKey(tea.KeyMsg{Type: tea.KeyEnter})
	hub := got.(monitorModel)
	if hub.view != viewDashboard {
		t.Fatalf("view = %v, want the dashboard (no palette on a header)", hub.view)
	}
	if !hub.expanded["CLOSED"] {
		t.Fatal("enter should have expanded the CLOSED section")
	}
	if g := hub.headerUnderCursor(); g == nil || g.label != "CLOSED" {
		t.Fatal("cursor should still be on the CLOSED header after the toggle")
	}
	below := hub // one ↓ from the header lands on the unfolded ticket
	below.cursor++
	if sel := below.selected(); sel == nil || sel.Ticket != "M-1" {
		t.Fatal("the unfolded ticket should be selectable below its header")
	}

	got, _ = hub.dispatchKey(tea.KeyMsg{Type: tea.KeyEnter})
	hub = got.(monitorModel)
	if hub.expanded["CLOSED"] {
		t.Fatal("enter again should have collapsed the CLOSED section")
	}
}

// Enter on a commented-PR row opens the MENU, not the triage screen: the old
// straight-to-comments jump blocked Android Studio, the PR link and Claude
// Code on exactly the rows that need them. The comments screen is one more
// enter away, as the menu's leading item.
func TestEnterOnCommentedPROpensTheMenu(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := baseModel()
	m.flat = []store.Session{{Ticket: "K-1", State: store.StateReview,
		PRURL: "https://x/pull/1", OpenComments: 3}}
	m.groups = newGroups()
	m.rows = flatRows(1)
	m.cursor = len(dashCommands)

	got, _ := m.dispatchKey(tea.KeyMsg{Type: tea.KeyEnter})
	hub := got.(monitorModel)
	if hub.view != viewPalette {
		t.Fatalf("view = %v, want the actions menu", hub.view)
	}
	items := hub.paletteItems()
	if len(items) == 0 || !strings.Contains(items[0].label, "Address PR feedback (3)") {
		t.Fatalf("menu should lead with Address PR feedback, got %+v", items[:min(3, len(items))])
	}
}

// Same fix, fix-review: Enter used to jump straight into the cards screen -
// if that screen ever misbehaves (a stuck card, an unreadable comment body),
// the row had no menu to fall back to, only however the cards screen's own
// esc/nav happened to work. The menu is one guaranteed-responsive screen the
// user can always retreat to; "Review the local fixes" leads it.
func TestEnterOnFixReviewOpensTheMenu(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := baseModel()
	m.flat = []store.Session{{Ticket: "K-2", State: store.StateFixReview}}
	m.groups = newGroups()
	m.rows = flatRows(1)
	m.cursor = len(dashCommands)

	got, _ := m.dispatchKey(tea.KeyMsg{Type: tea.KeyEnter})
	hub := got.(monitorModel)
	if hub.view != viewPalette {
		t.Fatalf("view = %v, want the actions menu", hub.view)
	}
	items := hub.paletteItems()
	if len(items) == 0 || !strings.Contains(items[0].label, "Review the local fixes") {
		t.Fatalf("menu should lead with Review the local fixes, got %+v", items[:min(3, len(items))])
	}
}
