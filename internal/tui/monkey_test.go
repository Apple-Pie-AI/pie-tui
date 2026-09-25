package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The monkey test: a deterministic key-storm through the real Update router,
// rendering View after every step. The directed tests pin the paths we thought
// of; this one walks the paths we didn't - enter on empty lists, arrows past
// both ends, esc from half-open states, a resize landing mid-menu. A panic in
// either Update or View crashes the whole hub for a keystroke, which is the
// one bug class a TUI can never afford.
//
// Deterministic on purpose (fixed seeds, an LCG rather than math/rand): a
// failure prints the seed and the key trail, and the same seed replays it.

// stormKeys is the alphabet: every key the hub routes, plus letters it doesn't,
// because unbound keys reaching a handler are exactly what goes unexercised.
var stormKeys = []tea.KeyMsg{
	{Type: tea.KeyUp}, {Type: tea.KeyDown}, {Type: tea.KeyLeft}, {Type: tea.KeyRight},
	{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyTab}, {Type: tea.KeySpace},
	{Type: tea.KeyPgUp}, {Type: tea.KeyPgDown}, {Type: tea.KeyBackspace},
	{Type: tea.KeyRunes, Runes: []rune("q")}, {Type: tea.KeyRunes, Runes: []rune(":")},
	{Type: tea.KeyRunes, Runes: []rune("j")}, {Type: tea.KeyRunes, Runes: []rune("k")},
	{Type: tea.KeyRunes, Runes: []rune("x")}, {Type: tea.KeyRunes, Runes: []rune("o")},
	{Type: tea.KeyRunes, Runes: []rune("a")}, {Type: tea.KeyRunes, Runes: []rune("R")},
}

// stormSizes cycles the resize through every margin step and both sides of the
// too-narrow floor, so the storm keeps crossing the layout boundaries.
var stormSizes = []tea.WindowSizeMsg{
	{Width: 240, Height: 34}, {Width: 190, Height: 34}, {Width: 131, Height: 20},
	{Width: 104, Height: 34}, {Width: 90, Height: 12}, {Width: 76, Height: 34},
	{Width: 60, Height: 20}, {Width: 53, Height: 8}, {Width: 40, Height: 24},
	{Width: 80, Height: 34},
}

// keyName renders a key for the failure trail.
func keyName(k tea.KeyMsg) string {
	if k.Type == tea.KeyRunes {
		return string(k.Runes)
	}
	return k.String()
}

// storm drives n pseudo-random steps from m, failing with a replayable trail.
func storm(t *testing.T, m monitorModel, seed uint64, n int) {
	t.Helper()
	var trail []string
	step := 0
	defer func() {
		if r := recover(); r != nil {
			from := 0
			if len(trail) > 40 {
				from = len(trail) - 40
			}
			t.Fatalf("panic at step %d (seed %d): %v\nlast keys: %s",
				step, seed, r, strings.Join(trail[from:], " "))
		}
	}()
	rng := seed
	for step = 0; step < n; step++ {
		rng = rng*6364136223846793005 + 1442695040888963407 // Knuth LCG
		var msg tea.Msg
		if step%23 == 22 { // a resize lands mid-whatever-is-open
			msg = stormSizes[(rng>>33)%uint64(len(stormSizes))]
			trail = append(trail, fmt.Sprintf("[resize %dx%d]",
				msg.(tea.WindowSizeMsg).Width, msg.(tea.WindowSizeMsg).Height))
		} else {
			k := stormKeys[(rng>>33)%uint64(len(stormKeys))]
			msg = k
			trail = append(trail, keyName(k))
		}
		got, _ := m.Update(msg) // returned commands stay un-invoked: no I/O runs
		m = got.(monitorModel)
		_ = m.View() // View must survive every state Update can produce
	}
}

// TestMonkeyStorm runs the storm from every screen a user can be on. PATH is
// emptied and PIE_HOME isolated so even a handler that shelled out directly
// (none should) could not touch the machine.
func TestMonkeyStorm(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PIE_HOME", t.TempDir())

	starts := map[string]func() monitorModel{
		"empty-dashboard": func() monitorModel { m := baseModel(); m.groups = newGroups(); return m },
		"full-dashboard":  dashModel,
		"comments":        goldenModel,
		"palette": func() monitorModel {
			m := dashModel()
			m.view, m.paletteAgentOnly = viewPalette, true
			m.cursor = len(dashCommands)
			return m
		},
	}
	for name, mk := range starts {
		for _, seed := range []uint64{1, 7, 42} {
			t.Run(fmt.Sprintf("%s/seed%d", name, seed), func(t *testing.T) {
				storm(t, mk(), seed, 2500)
			})
		}
	}
}

// TestArrowsPastBothEnds is the directed version of the storm's most likely
// find: the cursor clamped at every boundary, on every screen, at every width.
func TestArrowsPastBothEnds(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	for _, w := range []int{53, 80, 104, 190} {
		for _, mk := range []func() monitorModel{dashModel, goldenModel} {
			m := mk()
			m.width = w
			for i := 0; i < 30; i++ {
				got, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
				m = got.(monitorModel)
				_ = m.View()
			}
			for i := 0; i < 30; i++ {
				got, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
				m = got.(monitorModel)
				_ = m.View()
			}
		}
	}
}

// TestEscUnwindsEverything: from any nesting the hub can enter with the
// keyboard, enough escs always land back on the dashboard - the "where am I,
// get me out" key must never wedge.
func TestEscUnwindsEverything(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	openers := [][]tea.KeyMsg{
		{{Type: tea.KeyEnter}},                      // ticket → menu
		{{Type: tea.KeyEnter}, {Type: tea.KeyDown}}, // menu, moved
		{{Type: tea.KeyRunes, Runes: []rune(":")}},  // command menu
		{{Type: tea.KeyRunes, Runes: []rune(":")}, {Type: tea.KeyUp}},
	}
	for i, opens := range openers {
		m := dashModel()
		m.cursor = len(dashCommands)
		for _, k := range opens {
			got, _ := m.Update(k)
			m = got.(monitorModel)
		}
		for j := 0; j < 6; j++ {
			got, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = got.(monitorModel)
			_ = m.View()
		}
		if m.view != viewDashboard {
			t.Errorf("opener %d: six escs landed on view %d, want the dashboard", i, m.view)
		}
	}
}
