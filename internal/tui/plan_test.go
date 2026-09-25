package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// planMarkdownDoc builds a markdown doc from the worktree's plan.json.
func TestPlanMarkdownDoc(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	s := store.Session{Ticket: "K-50", Summary: "Add topics", State: store.StatePlanReview}
	wt := paths.WorktreeFor(s.Repo, s.Ticket)
	if err := os.MkdirAll(filepath.Join(wt, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	planJSON := `{"plan":"Add topic discovery","steps":["Add topic field","Run tests"],"questions":["Which screen?"],"confidence":"high","type":"feature","diagram":"` + "```" + `\nHome --> Discover\n` + "```" + `"}`
	if err := os.WriteFile(filepath.Join(wt, ".agent", "plan.json"), []byte(planJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	md, ok := planMarkdownDoc(s)
	if !ok {
		t.Fatal("expected a plan doc")
	}
	for _, want := range []string{
		"# K-50 · Add topics",
		"**Confidence:** high",
		"Add topic discovery", // plan body is free-form markdown, included verbatim
		"## Diagram", "Home --> Discover",
		"## Steps", "1. Add topic field", "2. Run tests",
		"## Open questions", "- Which screen?",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("planMarkdownDoc missing %q in:\n%s", want, md)
		}
	}

	// No plan on disk → ok=false.
	if _, ok := planMarkdownDoc(store.Session{Ticket: "NOPE", State: store.StatePlanReview}); ok {
		t.Error("expected ok=false when no plan.json exists")
	}
}

// updatePlan scrolls within bounds and leaves on esc.
func TestUpdatePlanScrollAndExit(t *testing.T) {
	m := monitorModel{
		view:   viewPlan,
		height: 20,
		plan:   planState{ticket: "K-1"},
	}
	m.plan.lines = make([]string, 50)
	maxScroll := len(m.plan.lines) - m.planViewportHeight()

	// Down clamps up from 0.
	nm, _ := m.updatePlan(tea.KeyMsg{Type: tea.KeyDown})
	if got := nm.(monitorModel).plan.scroll; got != 1 {
		t.Errorf("down: scroll = %d, want 1", got)
	}
	// Up never goes below 0.
	nm, _ = m.updatePlan(tea.KeyMsg{Type: tea.KeyUp})
	if got := nm.(monitorModel).plan.scroll; got != 0 {
		t.Errorf("up at top: scroll = %d, want 0", got)
	}
	// End jumps to the max, and further down won't exceed it.
	nm, _ = m.updatePlan(tea.KeyMsg{Type: tea.KeyEnd})
	if got := nm.(monitorModel).plan.scroll; got != maxScroll {
		t.Errorf("end: scroll = %d, want %d", got, maxScroll)
	}
	m.plan.scroll = maxScroll
	nm, _ = m.updatePlan(tea.KeyMsg{Type: tea.KeyDown})
	if got := nm.(monitorModel).plan.scroll; got != maxScroll {
		t.Errorf("down at bottom: scroll = %d, want %d (clamped)", got, maxScroll)
	}
	// Esc returns to the dashboard.
	nm, _ = m.updatePlan(tea.KeyMsg{Type: tea.KeyEsc})
	if got := nm.(monitorModel).view; got != viewDashboard {
		t.Errorf("esc: view = %d, want dashboard", got)
	}
}

// The plan viewer's top menu: ←→ toggles between "Give feedback" (0) and
// "Approve" (1); enter on "Give feedback" opens the inline input, and esc in the
// input returns to the menu with the plan still open.
func TestPlanMenuAndFeedbackToggle(t *testing.T) {
	m := monitorModel{
		view:   viewPlan,
		height: 20,
		plan:   planState{ticket: "K-1"},
	}
	m.plan.lines = make([]string, 12)

	// Default selection is "Give feedback" (0); right toggles to "Approve" (1).
	nm, _ := m.updatePlan(tea.KeyMsg{Type: tea.KeyRight})
	if got := nm.(monitorModel).plan.menu; got != 1 {
		t.Errorf("right: planMenu = %d, want 1", got)
	}
	// Left toggles back to "Give feedback" (0).
	nm, _ = nm.(monitorModel).updatePlan(tea.KeyMsg{Type: tea.KeyLeft})
	if got := nm.(monitorModel).plan.menu; got != 0 {
		t.Errorf("left: planMenu = %d, want 0", got)
	}
	// Enter on "Give feedback" opens the inline input (stays in the plan view).
	nm, _ = nm.(monitorModel).updatePlan(tea.KeyMsg{Type: tea.KeyEnter})
	hub := nm.(monitorModel)
	if !hub.plan.feedback || hub.view != viewPlan {
		t.Errorf("enter feedback: planFeedback=%v view=%d, want true/viewPlan", hub.plan.feedback, hub.view)
	}
	// Typing appends to the feedback buffer.
	hub2, _ := hub.updatePlan(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	if got := hub2.(monitorModel).plan.fb.text(); got != "hi" {
		t.Errorf("typed feedback = %q, want %q", got, "hi")
	}
	// Esc cancels the input but keeps the plan open (back to the menu).
	hub3, _ := hub2.(monitorModel).updatePlan(tea.KeyMsg{Type: tea.KeyEsc})
	h3 := hub3.(monitorModel)
	if h3.plan.feedback || h3.view != viewPlan {
		t.Errorf("esc feedback: planFeedback=%v view=%d, want false/viewPlan", h3.plan.feedback, h3.view)
	}
}

// agentActions for a plan-review session offers approve-plan and plan-feedback,
// and does NOT offer resume/ship (plan-review is neither active nor stuck).
func TestAgentActionsPlanReview(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	// Build a worktree so studio/claude also show, matching a real plan-review row.
	wt := paths.WorktreeFor("", "K-43")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /x"), 0o644); err != nil {
		t.Fatal(err)
	}

	ids := map[actionID]bool{}
	for _, it := range agentActions(store.Session{Ticket: "K-43", State: store.StatePlanReview}) {
		ids[it.key] = true
	}
	for _, want := range []actionID{actApprovePlan, actPlanFeedback} {
		if !ids[want] {
			t.Errorf("plan-review menu missing %q", want)
		}
	}
	for _, no := range []actionID{actResume, actShip} {
		if ids[no] {
			t.Errorf("plan-review should NOT offer %q", no)
		}
	}
}
