package tui

import (
	"strings"
	"testing"
)

// Key sequences, end to end. Update is pure, so the whole interaction model is
// testable as a table - and the bugs worth catching here are keys leaking from
// one mode into another, which only a sequence can find.
func TestKeySequences(t *testing.T) {
	tests := []struct {
		name  string
		keys  []string
		check func(*testing.T, monitorModel)
	}{
		{
			// The spec's worked example: walk to the third comment and skip it.
			name: "down down enter enter skips the third comment",
			keys: []string{"down", "down", "enter", "down", "enter"},
			check: func(t *testing.T, m monitorModel) {
				if m.comments.sel["T3"] {
					t.Error("T3 should be skipped")
				}
				if !m.comments.sel["T1"] || !m.comments.sel["T2"] {
					t.Error("only the comment under the cursor should have changed")
				}
				if m.comments.mode != modeBrowsing {
					t.Error("the menu should have closed behind the pick")
				}
			},
		},
		{
			// One press on the aggregate row is the whole "start from nothing"
			// workflow; the old Uncheck all button did exactly this much.
			name: "enter on the aggregate row clears every box",
			keys: []string{"up", "enter"},
			check: func(t *testing.T, m monitorModel) {
				if n := len(m.selectedIDs()); n != 0 {
					t.Errorf("%d still checked", n)
				}
				if m.comments.mode != modeBrowsing {
					t.Error("the aggregate row has no menu")
				}
			},
		},
		{
			name: "enter on the aggregate row again checks every box back",
			keys: []string{"up", "enter", "enter"},
			check: func(t *testing.T, m monitorModel) {
				if n := len(m.selectedIDs()); n != 3 {
					t.Errorf("%d checked, want all 3 back", n)
				}
			},
		},
		{
			// esc out of the instruction field must leave the stored value alone.
			name: "enter down enter esc leaves the instruction unchanged",
			keys: []string{"enter", "down", "down", "enter", "x", "y", "esc"},
			check: func(t *testing.T, m monitorModel) {
				if m.comments.rows[0].UserNote != "" {
					t.Errorf("instruction = %q, want it untouched", m.comments.rows[0].UserNote)
				}
				if m.comments.mode != modeBrowsing {
					t.Error("esc should return to the list")
				}
			},
		},
		{
			name: "esc closes the menu without touching the selection",
			keys: []string{"enter", "esc"},
			check: func(t *testing.T, m monitorModel) {
				if m.comments.mode != modeBrowsing {
					t.Error("esc should close the menu")
				}
				if n := len(m.selectedIDs()); n != 3 {
					t.Errorf("%d checked, want the selection untouched", n)
				}
				if m.view != viewComments {
					t.Error("esc closed the menu AND left the screen - one level per press")
				}
			},
		},
		{
			// The list's handler must be unreachable while a menu is open: up and
			// down belong to the menu there, not to the cursor.
			name: "arrows in the menu move the menu, not the list cursor",
			keys: []string{"down", "enter", "down", "down"},
			check: func(t *testing.T, m monitorModel) {
				if m.comments.cursor != 1 {
					t.Errorf("list cursor = %d, want it parked at 1 while the menu is open", m.comments.cursor)
				}
				if m.comments.menuItem == 0 {
					t.Error("down should have moved the menu's own selection")
				}
			},
		},
		{
			// Typing must not reach the list either: every letter here is text.
			name: "typing an instruction does not disturb the list",
			keys: []string{"enter", "down", "down", "enter", "u", "s", "e"},
			check: func(t *testing.T, m monitorModel) {
				if m.comments.mode != modeEditing {
					t.Fatal("should still be editing")
				}
				if got := m.comments.nb.text(); got != "use" {
					t.Errorf("buffer = %q, want %q", got, "use")
				}
				if m.comments.cursor != 0 {
					t.Errorf("cursor = %d, want it unmoved by typing", m.comments.cursor)
				}
				if n := len(m.selectedIDs()); n != 3 {
					t.Errorf("%d checked, want the selection untouched by typing", n)
				}
			},
		},
		{
			// The menu clamps rather than wrapping, so a held key cannot run past
			// the item you meant into a different one.
			name: "the menu clamps at both ends",
			keys: []string{"enter", "down", "down", "down", "down", "down"},
			check: func(t *testing.T, m monitorModel) {
				items := m.commentMenu()
				if m.comments.menuItem != len(items)-1 {
					t.Errorf("menuItem = %d, want it clamped to the last entry", m.comments.menuItem)
				}
				if items[m.comments.menuItem].action == menuSep {
					t.Error("the cursor landed on the separator")
				}
			},
		},
		{
			name: "up in the menu clamps at the first entry",
			keys: []string{"enter", "up", "up", "up"},
			check: func(t *testing.T, m monitorModel) {
				if m.comments.menuItem != 0 {
					t.Errorf("menuItem = %d, want it clamped to the first entry", m.comments.menuItem)
				}
			},
		},
		{
			// The run row runs; it does not open anything.
			name: "the run row has no menu",
			keys: []string{"down", "down", "down"},
			check: func(t *testing.T, m monitorModel) {
				if !m.onRunBar() {
					t.Fatalf("cursor = %d, want the run row", m.comments.cursor)
				}
				got, _ := m.updateComments(keyOf("enter"))
				if got.(monitorModel).comments.mode != modeBrowsing {
					t.Error("the run row opened a menu")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, press(commentsModel(), tc.keys...))
		})
	}
}

// The menu names the comment it belongs to, states the outcome of its first
// item, and offers exactly what the screen is allowed to do.
func TestMenuContents(t *testing.T) {
	m := press(commentsModel(), "enter")
	out := ansiRe.ReplaceAllString(m.renderMenu(100), "")

	if !strings.Contains(out, "LoginScreen.kt:12") {
		t.Errorf("the menu should be titled with the comment's location\n--- got ---\n%s", out)
	}
	for _, want := range []string{"Skip this comment", "[x] → [ ]",
		"Write an instruction for the agent", "Open on GitHub", "Close", "esc"} {
		if !strings.Contains(out, want) {
			t.Errorf("the menu is missing %q\n--- got ---\n%s", want, out)
		}
	}
	// The label states the outcome, so it has to follow the box.
	m.comments.sel["T1"] = false
	if out := ansiRe.ReplaceAllString(m.renderMenu(100), ""); !strings.Contains(out, "Fix this comment") {
		t.Errorf("on a skipped comment the item should read \"Fix this comment\"\n--- got ---\n%s", out)
	}
}

// The menu is a box: every line the same width, borders lined up.
func TestMenuBoxIsRectangular(t *testing.T) {
	for _, w := range []int{80, 100, 140} {
		m := press(commentsModel(), "enter")
		lines := strings.Split(strings.TrimRight(
			ansiRe.ReplaceAllString(m.renderMenu(w), ""), "\n"), "\n")
		if len(lines) < 4 {
			t.Fatalf("at %d cols the menu rendered %d lines", w, len(lines))
		}
		want := lipWidth(lines[0])
		for i, ln := range lines {
			if got := lipWidth(ln); got != want {
				t.Errorf("at %d cols line %d is %d cells, want %d: %q", w, i, got, want, ln)
			}
		}
		if !strings.HasPrefix(strings.TrimSpace(lines[0]), "╭") ||
			!strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "╰") {
			t.Errorf("at %d cols the menu is not closed top and bottom:\n%s", w, strings.Join(lines, "\n"))
		}
		if lipWidth(lines[0]) > w {
			t.Errorf("at %d cols the menu is %d cells wide", w, lipWidth(lines[0]))
		}
	}
}

// An existing instruction is loaded for editing rather than starting blank, or
// "Edit your instruction" would silently mean "replace it".
func TestMenuLoadsTheExistingInstruction(t *testing.T) {
	m := commentsModel()
	m.comments.rows[0].UserNote = "only the local val"
	m = press(m, "enter", "down", "down", "enter") // Reply & resolve leads; the instruction is third

	if m.comments.mode != modeEditing {
		t.Fatal("the instruction item should open the field")
	}
	if got := m.comments.nb.text(); got != "only the local val" {
		t.Errorf("buffer = %q, want the existing instruction loaded", got)
	}
	// And the item says so, rather than offering to "write" one that exists.
	menu := ansiRe.ReplaceAllString(press(commentsModel(), "enter").renderMenu(100), "")
	if !strings.Contains(menu, "Write an instruction") {
		t.Error("with no instruction the item should offer to write one")
	}
	withNote := commentsModel()
	withNote.comments.rows[0].UserNote = "x"
	if got := ansiRe.ReplaceAllString(press(withNote, "enter").renderMenu(100), ""); !strings.Contains(got, "Edit your instruction") {
		t.Errorf("with an instruction the item should offer to edit it\n--- got ---\n%s", got)
	}
}

// A row carrying an instruction says so in the list, next to the author.
func TestInstructionIsFlaggedOnTheRow(t *testing.T) {
	m := commentsModel()
	m.comments.rows[0].UserNote = "only the local val"
	out := ansiRe.ReplaceAllString(m.renderCommentList(100), "")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	if !strings.Contains(lines[0], "✎") {
		t.Errorf("the row with an instruction should carry ✎: %q", lines[0])
	}
	if strings.Contains(lines[1], "✎") {
		t.Errorf("a row with no instruction should not: %q", lines[1])
	}
	// It rides with the author, so it cannot eat into the body column.
	if i, j := strings.Index(lines[0], "✎"), strings.Index(lines[0], "bob"); i < 0 || i > j {
		t.Errorf("✎ should sit immediately before the author name: %q", lines[0])
	}
}

// The pane's instruction line is never called a note: a note reads as something
// the reviewer will see, and this is read only by the agent.
func TestInstructionLineIsLabelledForTheAgent(t *testing.T) {
	m := commentsModel()
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "your instruction") {
		t.Errorf("the pane should label the field \"your instruction\"\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "note ") {
		t.Errorf("the word \"note\" is still on screen\n--- got ---\n%s", out)
	}
}

func TestShortURL(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://github.com/o/r/pull/482#discussion_r5541", "github.com/…/482#discussion_r5541"},
		{"http://github.com/o/r/pull/482", "github.com/…/482"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := shortURL(tc.in); got != tc.want {
			t.Errorf("shortURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The hyperlink is an escape sequence, so it must carry no display width - the
// column it sits in is measured by its label.
func TestOSC8CarriesNoWidth(t *testing.T) {
	label := "github.com/…/482"
	linked := osc8("https://github.com/o/r/pull/482", label)
	if lipWidth(linked) != lipWidth(label) {
		t.Errorf("hyperlink measures %d cells, label measures %d", lipWidth(linked), lipWidth(label))
	}
	if osc8("", label) != label {
		t.Error("with no URL the label should be emitted plain")
	}
}

// stripAnsi has to understand the hyperlink it is asked to strip: an OSC
// sequence carries printable text (the URL) that the CSI rule would leave
// behind as content.
func TestStripAnsiRemovesHyperlinks(t *testing.T) {
	label := "github.com/…/482"
	got := stripAnsi(cmtDimSty.Render(osc8("https://github.com/o/r/pull/482", label)))
	if got != label {
		t.Errorf("stripAnsi left %q, want just %q", got, label)
	}
}
