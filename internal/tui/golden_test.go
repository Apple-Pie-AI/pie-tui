package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// goldenModel is one fixed screen: a mixed review with humans and bots, one
// blocking comment, one nit, one instruction already written, one comment
// unchecked, and a diff hunk. Frozen so a golden diff only ever shows a change
// in rendering.
func goldenModel() monitorModel {
	m := baseModel()
	m.view = viewComments
	m.lastRefresh = time.Date(2026, 8, 4, 12, 1, 0, 0, time.UTC)
	m.comments.reset("PIE-207", "PR #482", "")
	// Age() is relative to now, so the fixture pins the age rather than the
	// instant - otherwise the golden rots the moment it is committed.
	m.comments.lastFetch = time.Now().Add(-90 * time.Second)
	m.flat = []store.Session{{
		Ticket: "PIE-207", State: store.StateReview,
		Branch: "ai/pie-207-login-retry", PRURL: "https://github.com/acme/android/pull/482",
	}}
	m.comments.rows = []store.Comment{
		cmt("T1", "alice", func(c *store.Comment) {
			c.Path, c.Line = "app/src/main/java/com/acme/LoginViewModel.kt", 42
			c.Body = "block: Don't swallow the exception here — this hides real auth failures from Crashlytics. Log it and rethrow."
			c.DiffHunk = "@@ -39,4 +42,4 @@ class LoginViewModel {\n     fun login() {\n-        emit(Error)\n+        emit(Error(e))\n     }"
			c.URL = "https://github.com/acme/android/pull/482#discussion_r5511"
		}),
		cmt("T2", "coderabbitai[bot]", func(c *store.Comment) {
			c.Bot, c.Path, c.Line = true, "app/src/main/java/com/acme/LoginViewModel.kt", 57
			c.Body = "block: viewModelScope.launch without a CoroutineExceptionHandler will crash the process."
			c.URL = "https://github.com/acme/android/pull/482#discussion_r5518"
		}),
		cmt("T3", "bob", func(c *store.Comment) {
			c.Path, c.Line = "app/src/main/java/com/acme/LoginViewModel.kt", 88
			c.Body = "nit: rename `loading` to `isLoading` for consistency with the rest of the file."
			c.UserNote = "only the local val — the public API stays as it is"
			c.URL = "https://github.com/acme/android/pull/482#discussion_r5522"
		}),
		cmt("T4", "sonarcloud[bot]", func(c *store.Comment) {
			c.Bot, c.Path, c.Line = true, "app/build.gradle.kts", 31
			c.Body = "chore: okhttp 4.9.3 is affected by CVE-2023-0833. Bump to 4.12.0."
			c.URL = "https://github.com/acme/android/pull/482#discussion_r5550"
		}),
		cmt("T5", "carol", func(c *store.Comment) {
			c.Path, c.Line = "", 0
			c.Body = "Overall this is close, but the retry policy should live in the repository layer, not the view model."
			c.URL = "https://github.com/acme/android/pull/482#pullrequestreview-99"
		}),
	}
	for _, c := range m.comments.rows {
		m.comments.sel[c.ID] = true
	}
	m.comments.sel["T4"] = false // one unchecked row, to pin the muted palette
	m.comments.cursor = 0
	m.renderFocused()
	return m
}

// Alignment regressions are invisible in a code review and obvious in a golden
// diff, which is the entire reason these files are committed.
func TestGoldenScreens(t *testing.T) {
	cases := []struct {
		name  string
		width int
		setup func(monitorModel) monitorModel
	}{
		{"browsing-80", 80, nil},
		{"browsing-104", 104, nil},
		{"browsing-190", 190, nil},
		{"menu-104", 104, func(m monitorModel) monitorModel { return press(m, "enter") }},
		{"allrow-104", 104, func(m monitorModel) monitorModel { return press(m, "up") }},
		{"button-focused-104", 104, func(m monitorModel) monitorModel {
			m.comments.cursor = len(m.visible())
			m.renderFocused()
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := goldenModel()
			m.width, m.height = tc.width, 34
			m.renderFocused() // what a WindowSizeMsg does: re-wrap for the new width
			if tc.setup != nil {
				m = tc.setup(m)
			}
			// Golden files carry the layout, not the colour: styling is stripped so
			// a diff shows columns moving rather than escape codes churning.
			got := stripAnsi(m.View())
			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Errorf("%s changed. Run `go test ./internal/tui -update` if intended.\n"+
					"--- want ---\n%s\n--- got ---\n%s", tc.name, want, got)
			}
		})
	}
}

// dashModel is the dashboard's fixed screen: one ticket per section, so every
// group header, phase colour and column drop is exercised by one golden.
func dashModel() monitorModel {
	m := baseModel()
	m.view = viewDashboard
	m.lastRefresh = time.Date(2026, 8, 4, 12, 1, 0, 0, time.UTC)
	// Age() measures against time.Now(), so the ages have to be offsets from now
	// rather than from a fixed instant - a pinned date would read "12h" today and
	// "3d" on Thursday. They sit on the half hour so Age's integer hours hold
	// steady for thirty minutes rather than ticking between runs.
	now := time.Now()
	m.groups = newGroups()
	for _, s := range []store.Session{
		{Ticket: "PIE-14", State: "needs-you", ShortDesc: "Review pull request",
			Summary: "Review the PR", UpdatedAt: now.Add(-4*time.Hour - 30*time.Minute)},
		{Ticket: "PIE-12", State: "working", ShortDesc: "Create a new text hello world",
			UpdatedAt: now.Add(-3*time.Hour - 30*time.Minute), PID: 1},
		{Ticket: "PIE-1-ADD-HELLO-WORLD-TO-MAIN-ACTIVITY", State: store.StateReview,
			ShortDesc: "Add what's up man text below hello world text",
			PRURL:     "https://github.com/acme/android/pull/12",
			UpdatedAt: now.Add(-2*time.Hour - 30*time.Minute)},
		{Ticket: "DEMO-EMULATOR", State: store.StateStopped,
			ShortDesc: "Add subtitle Text composable and string resource",
			UpdatedAt: now.Add(-time.Hour - 30*time.Minute)},
		{Ticket: "PIE-3", State: store.StateMerged,
			ShortDesc: "Bump the emulator boot timeout",
			PRURL:     "https://github.com/acme/android/pull/9",
			UpdatedAt: now.Add(-30 * time.Minute)},
	} {
		for _, g := range m.groups {
			if g.match(s) {
				g.sessions = append(g.sessions, s)
				break
			}
		}
	}
	for _, g := range m.groups {
		m.flat = append(m.flat, g.sessions...)
	}
	m.rows = buildRows(m.groups, m.expanded)
	return m
}

// The dashboard's own goldens. Same contract as the review screen's: colour is
// stripped, so a diff shows columns moving rather than escape codes churning.
func TestGoldenDashboard(t *testing.T) {
	cases := []struct {
		name  string
		width int
		setup func(monitorModel) monitorModel
	}{
		{"dash-80", 80, nil},
		{"dash-104", 104, nil},
		{"dash-190", 190, nil},
		{"dash-command-selected", 152, func(m monitorModel) monitorModel { m.cursor = 1; return m }},
		// The two cold sections unfolded, cursor on the CLOSED header: the
		// toggle row renders focused and its tickets are visible below it.
		{"dash-sections-expanded", 104, func(m monitorModel) monitorModel {
			m.expanded = map[string]bool{"STOPPED": true, "CLOSED": true}
			m.rows = buildRows(m.groups, m.expanded)
			for i, r := range m.rows {
				if r.kind == rowHeader && m.groups[r.group].label == "CLOSED" {
					m.cursor = i + len(dashCommands)
				}
			}
			return m
		}},
		{"dash-menu", 152, func(m monitorModel) monitorModel {
			m.view, m.paletteAgentOnly, m.paletteCursor = viewPalette, true, 0
			return m
		}},
		{"dash-menu-close", 152, func(m monitorModel) monitorModel {
			m.view, m.paletteAgentOnly = viewPalette, true
			m.paletteCursor = len(m.paletteItems()) // the Close row
			return m
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := dashModel()
			m.width, m.height = tc.width, 34
			m.cursor = len(dashCommands) // the first ticket
			if tc.setup != nil {
				m = tc.setup(m)
			}
			got := stripAnsi(m.View())
			path := filepath.Join("testdata", tc.name+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Errorf("%s changed. Run `go test ./internal/tui -update` if intended.\n"+
					"--- want ---\n%s\n--- got ---\n%s", tc.name, want, got)
			}
		})
	}
}

// No rendered line may exceed the terminal, at any size, on any screen. The
// clipped "6h" and "1h" in the age column came from rows drawn wider than the
// window; this is the assertion that would have caught it.
func TestNoScreenOverflowsTheTerminal(t *testing.T) {
	for _, termW := range []int{52, 60, 72, 80, 96, 104, 132, 190, 240} {
		for _, view := range []hubView{viewDashboard, viewPalette} {
			m := dashModel()
			m.width, m.height, m.view = termW, 34, view
			m.cursor = len(dashCommands)
			for _, ln := range strings.Split(stripAnsi(m.View()), "\n") {
				if w := lipWidth(strings.TrimRight(ln, " ")); w > termW {
					t.Errorf("view=%d w=%d: line runs to column %d: %q", view, termW, w, ln)
				}
			}
		}
	}
}

// Below the minimum the hub says so rather than drawing something broken.
func TestVeryNarrowTerminalSaysSo(t *testing.T) {
	m := dashModel()
	m.width, m.height = 40, 20
	out := stripAnsi(m.View())
	if !strings.Contains(out, "52 columns") {
		t.Errorf("a 40-column terminal should ask for a wider window:\n%s", out)
	}
}

// The acceptance criterion, asserted rather than eyeballed: the screen fills
// whatever terminal it is given, inset by the margin and never past it. This is
// what a fixed content width used to fail - it left a wide terminal two thirds
// empty, which read as a UI that had not noticed the window.
func TestTheScreenFillsTheTerminal(t *testing.T) {
	for _, termW := range []int{80, 104, 140, 190, 240} {
		m := goldenModel()
		m.width, m.height = termW, 34
		m.renderFocused()
		margin, content := layout(termW)

		widest := 0
		for _, ln := range strings.Split(stripAnsi(m.View()), "\n") {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			if lead := len(ln) - len(strings.TrimLeft(ln, " ")); lead < margin {
				t.Errorf("w=%d: line starts at column %d, want the %d-column margin: %q",
					termW, lead, margin, ln)
			}
			if w := lipWidth(strings.TrimRight(ln, " ")); w > margin+content {
				t.Errorf("w=%d: line runs to column %d, want it inside %d: %q",
					termW, w, margin+content, ln)
			} else if w > widest {
				widest = w
			}
		}
		// Reaching the right margin is the whole point: something on the screen
		// - the header's sync state, a selection band - has to touch it.
		if widest < margin+content {
			t.Errorf("w=%d: widest line is %d cells, want the screen out to %d",
				termW, widest, margin+content)
		}
	}
}

// The margin is the only thing held back, and the first thing given up. The
// content is always the rest of the terminal, never a fixed column.
func TestLayoutFillsAndDropsTheMarginFirst(t *testing.T) {
	for _, w := range []int{190, 240, 400} {
		if margin, content := layout(w); margin != Margin || content != w-2*Margin {
			t.Errorf("layout(%d) = (%d, %d), want (%d, %d) - the content is not capped",
				w, margin, content, Margin, w-2*Margin)
		}
	}
	for _, tc := range []struct{ term, margin int }{
		{200, Margin}, {120, Margin}, {119, 3}, {90, 3}, {89, 2}, {76, 2}, {75, 1}, {52, 1},
	} {
		if margin, _ := layout(tc.term); margin != tc.margin {
			t.Errorf("layout(%d) margin = %d, want %d", tc.term, margin, tc.margin)
		}
	}
	for _, w := range []int{20, 40, 45, 50, 55, 60, 80, 104, 110, 140, 190, 240} {
		margin, content := layout(w)
		if margin+content > w {
			t.Errorf("layout(%d) = (%d, %d): %d cells on a %d-cell terminal",
				w, margin, content, margin+content, w)
		}
		if content < 1 {
			t.Errorf("layout(%d) content = %d", w, content)
		}
	}
}

// Growing, the surplus does not all land in the body: a wide terminal that
// still truncates the author to "github-copil…" has spent its width badly.
func TestWideRowsGiveTheShortColumnsTheirFill(t *testing.T) {
	narrowLoc, _, narrowAuth := commentCols(92)
	wideLoc, wideBody, wideAuth := commentCols(228)
	if wideLoc <= narrowLoc {
		t.Errorf("location stayed at %d on a wide row, want it grown past %d", wideLoc, narrowLoc)
	}
	if wideAuth <= narrowAuth {
		t.Errorf("author stayed at %d on a wide row, want it grown past %d", wideAuth, narrowAuth)
	}
	if wideBody <= 80 {
		t.Errorf("body is %d cells, want it still taking the bulk of the surplus", wideBody)
	}
}

// Golden files strip colour, so the two things the design turns on - the filled
// band and the filled button - need their own assertion. Structural rather than
// literal: Lip Gloss rounds hex to RGB, so a test pinned to exact codes breaks
// on a palette tweak that changed nothing about the design.
func TestOnlyTheBandAndTheButtonPaint(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	const bg = "48;2;" // a truecolor background SGR
	m := goldenModel()
	m.width, m.height = 104, 34
	m.comments.cursor = 1
	m.renderFocused()
	_, cw := layout(m.width)

	selected := m.renderCommentRow(m.visible()[1], cw, true)
	if !strings.Contains(selected, bg) {
		t.Error("the selected row is not filled - a one-cell bar is invisible on a wide screen")
	}
	if lipWidth(selected) != cw {
		t.Errorf("the band is %d cells, want the full content width %d", lipWidth(selected), cw)
	}
	if !strings.Contains(selected, "\x1b[1;") {
		t.Error("the selected row's body should be the one bold thing on the screen")
	}
	plain := m.renderCommentRow(m.visible()[0], cw, false)
	if strings.Contains(plain, bg) {
		t.Error("an unselected row paints a background; only the band and the menu may")
	}
	if strings.Contains(plain, "\x1b[1;") {
		t.Error("an unselected row is bold; if two things are emphasised, neither is")
	}

	// The button is the only bordered element, and solid when focused.
	m.comments.cursor = len(m.visible())
	if focused := m.renderRunButton(cw); !strings.Contains(focused, bg) ||
		!strings.Contains(focused, "╭") {
		t.Error("the focused run button should be a solid, bordered, filled button")
	}
	m.comments.cursor = 1
	if unfocused := m.renderRunButton(cw); strings.Contains(unfocused, bg) {
		t.Error("the unfocused button should be an outline, not a fill")
	}

	// Nothing in the list is bordered - the button is the only one.
	if list := m.renderCommentList(cw); strings.ContainsAny(list, "╭╮╰╯") {
		t.Error("a list row is bordered; the run button must be the only bordered element")
	}

	// Added and removed diff lines differ in sign, colour and background.
	m.comments.cursor = 0
	m.renderFocused()
	var add, del string
	for _, ln := range m.codeRows(cw) {
		switch {
		case strings.Contains(ln, "emit(Error(e))"):
			add = ln
		case strings.Contains(ln, "emit(Error)"):
			del = ln
		}
	}
	if add == "" || del == "" {
		t.Fatal("the fixture should render one added and one removed line")
	}
	for name, ln := range map[string]string{"added": add, "removed": del} {
		if !strings.Contains(ln, bg) {
			t.Errorf("the %s line has no background wash - it reads as coloured text, not a diff", name)
		}
	}
	if !strings.Contains(add, "+") || !strings.Contains(del, "-") {
		t.Error("added and removed lines must differ by sign as well as colour")
	}
	if stripAnsi(add) == stripAnsi(del) {
		t.Error("added and removed lines render identically once colour is stripped")
	}
}
