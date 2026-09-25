package tui

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Every declared action must have an arm in doAction. The ids used to be
// produced in three places and checked in none, so a row could render,
// highlight, and do nothing at all when chosen; doAction's default arm now
// surfaces that as a notice, which is what this asserts against.
//
// It walks allActions rather than the menus on purpose. A menu only emits an
// action under the right session state, worktree presence and daemon state, so a
// menu walk silently skips whatever it fails to provoke - which is how an
// earlier version of this test missed actPlanMD and actDaemonStop entirely.
func TestEveryActionIsHandled(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	// A real store: actRefresh reloads, which the hub always has one for.
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	for _, id := range allActions {
		got, _ := monitorModel{store: st}.doAction(id)
		if n := got.(monitorModel).notice; n == "unknown action: "+string(id) {
			t.Errorf("doAction(%q) has no handler - choosing that menu row would do nothing", id)
		}
	}
}

// allActions has to actually list every constant, or the test above is walking a
// stale subset. There is no reflection over package-level consts in Go, so this
// pins the count: adding a constant without registering it fails here.
func TestAllActionsIsComplete(t *testing.T) {
	// The total number of actionID constants, not a running tally of additions -
	// two branches each appending their own "+actFoo" to this line is a conflict
	// that has to be counted out by hand to resolve.
	const declared = 30
	if len(allActions) != declared {
		t.Fatalf("allActions has %d entries but %d actionID constants are declared - "+
			"register the new one (and update this count)", len(allActions), declared)
	}
	seen := map[actionID]bool{}
	for _, id := range allActions {
		if seen[id] {
			t.Errorf("allActions lists %q twice", id)
		}
		seen[id] = true
	}
}

// And the menus must only ever emit registered actions - the other direction.
func TestMenusOnlyProduceRegisteredActions(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	registered := map[actionID]bool{}
	for _, id := range allActions {
		registered[id] = true
	}

	states := []string{
		store.StateQueued, store.StatePlanning, store.StateAwaiting, store.StateWorking,
		store.StateReviewing, store.StateBuilding, store.StateReview, store.StateNeedsYou,
		store.StateFailed, store.StatePlanReview, store.StateMerged, store.StateClosed, store.StateCheckedOut,
		store.StateStopped,
	}
	check := func(items []paletteItem) {
		for _, it := range items {
			if !registered[it.key] {
				t.Errorf("a menu produced unregistered action %q", it.key)
			}
		}
	}
	mkWorktree(t, "K2")
	for _, s := range states {
		check(agentActions(store.Session{Ticket: "K1", State: s})) // no worktree
		check(agentActions(store.Session{Ticket: "K2", State: s})) // with worktree
	}
	m := monitorModel{flat: []store.Session{{Ticket: "K2", State: store.StateNeedsYou}}, cursor: 3}
	check(m.paletteItems())
}

// The default arm itself: an id nothing handles says so rather than being a
// silent no-op indistinguishable from a hung UI.
func TestDoActionUnknownIDIsVisible(t *testing.T) {
	got, _ := monitorModel{}.doAction(actionID("no-such-action"))
	if n := got.(monitorModel).notice; n != "unknown action: no-such-action" {
		t.Errorf("notice = %q, want the unknown-action message", n)
	}
}

// newRunState is the only definition of a fresh run wizard, and the sentinels
// are the reason: the five prompt indices must be -1, because their zero value
// means "chip 0 is prompting" and silently routes into the wrong handler.
func TestNewRunStateSentinels(t *testing.T) {
	r := newRunState()
	for name, got := range map[string]int{
		"idPrompt":     r.idPrompt,
		"branchPrompt": r.branchPrompt,
		"imgPrompt":    r.imgPrompt,
		"stackPrompt":  r.stackPrompt,
		"reviewPrompt": r.reviewPrompt,
	} {
		if got != -1 {
			t.Errorf("newRunState().%s = %d, want -1", name, got)
		}
	}
}

// reset must clear the WHOLE wizard. The four hand-written reset sites this
// replaced had each drifted to a different subset - doAction(actRun) left
// stackPrompt armed, so re-entering "Start new" after backing out of the stack
// picker reopened it on a chip that no longer existed.
//
// reset() is currently `*r = newRunState()`, so comparing the two directly could
// never fail. The assertion that carries weight is on the FIXTURE: every field
// must be dirtied before the reset, walked by reflection so a field added later
// is caught here rather than quietly escaping the test. That is what keeps this
// honest if reset ever becomes hand-written again - which is exactly the drift
// it exists to prevent.
func TestRunStateResetClearsEverything(t *testing.T) {
	ed := textarea.New()
	dirty := runState{
		mode: "content", methodCursor: 1, text: "typed", tickets: []pendingTicket{{id: "A-1"}},
		idPrompt:     1,
		branchPrompt: 2, imgPrompt: 3, stackPrompt: 4, reviewPrompt: 5,
		reviewCursor: 1, ticketLines: []string{"x"}, ticketScroll: 3, promptCursor: 4,
		content: &ed, preview: true, bar: true, btn: 2,
		stackBranches: []git.Branch{{Name: "ai/parent"}},
		stackLoading:  true,
		stackDefault:  "main", stackFilter: "ai/", stackCursor: 2,
		branchFirstPick: true, existingBranch: "feedback/fix", holderPath: "/held",
	}

	// Compared through fmt rather than reflect.Value.Interface(), which panics on
	// unexported fields - and every field of runState is unexported.
	fresh := reflect.ValueOf(newRunState())
	before := reflect.ValueOf(dirty)
	for i := 0; i < before.NumField(); i++ {
		name := before.Type().Field(i).Name
		if fmt.Sprint(before.Field(i)) == fmt.Sprint(fresh.Field(i)) {
			t.Errorf("fixture leaves runState.%s at its fresh value, so this test cannot "+
				"detect a reset that forgets it - dirty every field", name)
		}
	}

	dirty.reset()
	after := reflect.ValueOf(dirty)
	for i := 0; i < after.NumField(); i++ {
		name := after.Type().Field(i).Name
		if got, want := fmt.Sprint(after.Field(i)), fmt.Sprint(fresh.Field(i)); got != want {
			t.Errorf("reset() left runState.%s = %s, want %s", name, got, want)
		}
	}
}

// doAction(actRun) and cancelRunInput both go through that one reset, so an
// armed stack picker cannot survive into a fresh wizard.
func TestStartNewClearsAnArmedStackPicker(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := baseModel()
	m.run.stackPrompt = 0
	m.run.tickets = []pendingTicket{{id: "GONE-1", kind: "content"}}

	got, _ := m.doAction(actRun)
	hub := got.(monitorModel)
	if hub.run.stackPrompt != -1 {
		t.Errorf("stackPrompt = %d after Start new, want -1 (the picker would reopen on a dropped chip)", hub.run.stackPrompt)
	}
	if len(hub.run.tickets) != 0 {
		t.Errorf("tickets = %+v after Start new, want none", hub.run.tickets)
	}
}

// The atom input is one primitive shared by the answer overlay and the plan
// feedback box. Exercising it through the plan screen proves the sharing is
// real, not two copies that happen to agree today.
func TestPlanFeedbackUsesTheSharedAtomInput(t *testing.T) {
	m := baseModel()
	m.view, m.plan.feedback, m.plan.ticket = viewPlan, true, "K1"

	got, _ := m.updatePlanFeedback(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi ")})
	m = got.(monitorModel)
	got, _ = m.updatePlanFeedback(tea.KeyMsg{Paste: true, Runes: []rune("a\nb\nc")})
	m = got.(monitorModel)

	if want := "hi a\nb\nc"; m.plan.fb.text() != want {
		t.Errorf("feedback text = %q, want %q", m.plan.fb.text(), want)
	}
	// A multi-line paste is one atom, so one backspace removes it whole.
	got, _ = m.updatePlanFeedback(tea.KeyMsg{Type: tea.KeyBackspace})
	m = got.(monitorModel)
	if m.plan.fb.text() != "hi " {
		t.Errorf("backspace over a paste = %q, want %q", m.plan.fb.text(), "hi ")
	}
	// …and the rendered line collapses it to a chip, same as the answer box.
	m2 := baseModel()
	m2.view, m2.plan.feedback = viewPlan, true
	got, _ = m2.updatePlanFeedback(tea.KeyMsg{Paste: true, Runes: []rune("a\nb\nc")})
	if line := got.(monitorModel).plan.fb.render(); line != "[3 lines added]▌" {
		t.Errorf("rendered feedback = %q, want the collapsed paste chip", line)
	}
}
