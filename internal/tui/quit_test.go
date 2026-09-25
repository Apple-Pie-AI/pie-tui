package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ctrl+c has to quit from every screen, and the invariant is easy to break: it
// used to be re-added by hand in each handler, and the stop-confirmation dialog
// was the one that never got it. dispatchKey now answers it before any screen
// sees the key, and this walks every screen to keep it that way.
//
// tea.Quit is compared by identity - a tea.Cmd is a func, so the only sound
// check is that the returned command is the same function value.
func TestCtrlCQuitsFromEveryScreen(t *testing.T) {
	quits := func(m monitorModel) bool {
		_, cmd := m.dispatchKey(tea.KeyMsg{Type: tea.KeyCtrlC})
		if cmd == nil {
			return false
		}
		// tea.Quit returns a tea.QuitMsg; nothing else in the hub does.
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}

	screens := map[string]func() monitorModel{
		"dashboard":   baseModel,
		"palette":     func() monitorModel { m := baseModel(); m.view = viewPalette; return m },
		"run method":  func() monitorModel { m := baseModel(); m.view = viewRunMethod; return m },
		"run input":   func() monitorModel { m := baseModel(); m.view = viewRunInput; m.run.mode = "jira"; return m },
		"output":      func() monitorModel { m := baseModel(); m.view = viewOutput; return m },
		"consent":     func() monitorModel { m := baseModel(); m.view = viewConsent; return m },
		"plan viewer": func() monitorModel { m := baseModel(); m.view = viewPlan; return m },
		"plan feedback": func() monitorModel {
			m := baseModel()
			m.view, m.plan.feedback = viewPlan, true
			return m
		},
		"form": func() monitorModel {
			m := baseModel()
			m.view = viewForm
			m.form = &formModel{fields: []formField{{label: "one"}}}
			return m
		},
		"id prompt": func() monitorModel {
			m := baseModel()
			m.view = viewRunInput
			m.run.tickets = []pendingTicket{{kind: "content", id: "A-1"}}
			m.run.idPrompt = 0
			return m
		},
		"branch prompt": func() monitorModel {
			m := baseModel()
			m.view = viewRunInput
			m.run.tickets = []pendingTicket{{kind: "content", id: "A-1", branch: "b"}}
			m.run.branchPrompt = 0
			return m
		},
		"image attach": func() monitorModel {
			m := baseModel()
			m.view = viewRunInput
			m.run.tickets = []pendingTicket{{kind: "content", id: "A-1"}}
			m.run.imgPrompt = 0
			return m
		},
		"stack picker": func() monitorModel {
			m := baseModel()
			m.view = viewRunInput
			m.run.tickets = []pendingTicket{{kind: "content", id: "A-1"}}
			m.run.stackPrompt = 0
			return m
		},
		"review picker": func() monitorModel {
			m := baseModel()
			m.view = viewRunInput
			m.run.tickets = []pendingTicket{{kind: "content", id: "A-1"}}
			m.run.reviewPrompt = 0
			return m
		},
		// The two overlays that pre-empt the active view.
		"answer overlay": func() monitorModel {
			m := baseModel()
			m.answer = answerState{open: true, ticket: "A-1"}
			return m
		},
		"stop confirmation": func() monitorModel {
			m := baseModel()
			m.confirm = confirmState{ticket: "A-1"}
			return m
		},
	}

	for name, build := range screens {
		t.Run(name, func(t *testing.T) {
			if !quits(build()) {
				t.Errorf("ctrl+c did not quit from the %s screen", name)
			}
		})
	}
}
