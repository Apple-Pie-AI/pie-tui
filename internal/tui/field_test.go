package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// updateForm now delegates its editing keys to editKey instead of restating all
// eight cases. This drives the real handler - the link the refactor changed -
// rather than editKey in isolation, and pins the split: ↑/↓ still move BETWEEN
// fields (the form owns them) while the rest edits within one.
func TestUpdateFormDelegatesEditingToEditKey(t *testing.T) {
	m := monitorModel{view: viewForm, form: &formModel{
		fields: []formField{{key: "a", label: "one", value: "main"}, {key: "b", label: "two", value: "xy"}},
	}}
	m.form.focusEnd()
	step := func(msg tea.KeyMsg) {
		t.Helper()
		got, _ := m.updateForm(msg)
		m = got.(monitorModel)
	}

	// Editing keys reach the focused field.
	step(tea.KeyMsg{Type: tea.KeyLeft})
	step(tea.KeyMsg{Type: tea.KeyLeft})
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("XY")})
	if got := m.form.fields[0].value; got != "maXYin" {
		t.Errorf("typing mid-value = %q, want %q - the key never reached editKey", got, "maXYin")
	}
	step(tea.KeyMsg{Type: tea.KeyBackspace})
	step(tea.KeyMsg{Type: tea.KeyHome})
	step(tea.KeyMsg{Type: tea.KeyDelete})
	if got := m.form.fields[0].value; got != "aXin" {
		t.Errorf("backspace+delete = %q, want %q", got, "aXin")
	}

	// ↓ is the form's, not the field's: it moves focus and must not insert.
	step(tea.KeyMsg{Type: tea.KeyDown})
	if m.form.focus != 1 {
		t.Fatalf("focus = %d after ↓, want 1 - the field swallowed a form key", m.form.focus)
	}
	if got := m.form.fields[0].value; got != "aXin" {
		t.Errorf("↓ modified the field it left: %q", got)
	}

	// Esc closes without submitting.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != viewDashboard || m.form != nil {
		t.Errorf("esc should close the form, got view=%v form=%v", m.view, m.form)
	}
}

// A form with no fields must not panic: both the paste path and the editing
// path index fields[focus].
func TestUpdateFormWithNoFields(t *testing.T) {
	m := monitorModel{view: viewForm, form: &formModel{}}
	got, _ := m.updateForm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	if hub := got.(monitorModel); hub.view != viewDashboard || hub.form != nil {
		t.Errorf("an empty form should bail to the dashboard, got view=%v form=%v", hub.view, hub.form)
	}
}

// The form must support in-field cursor editing: type/backspace/delete happen at
// the caret, and ←→/Home/End move it - so a value can be edited in the middle,
// not only by deleting from the end. This exercises editKey directly; the test
// above covers updateForm's delegation to it.
func TestFormCursorEditing(t *testing.T) {
	f := &formModel{fields: []formField{{label: "Branch", value: "main"}}}
	f.focusEnd()
	if f.fields[0].cursor != 4 {
		t.Fatalf("focusEnd: cursor = %d, want 4", f.fields[0].cursor)
	}
	key := func(tp tea.KeyType, runes ...rune) {
		t.Helper()
		if !f.focused().editKey(tea.KeyMsg{Type: tp, Runes: runes}) {
			t.Fatalf("editKey did not consume %v", tp)
		}
	}

	// Move left twice → between "ma" and "in"; insert "XY".
	key(tea.KeyLeft)
	key(tea.KeyLeft)
	key(tea.KeyRunes, 'X', 'Y')
	if got := f.fields[0].value; got != "maXYin" {
		t.Errorf("insert mid-value = %q, want %q", got, "maXYin")
	}
	if f.fields[0].cursor != 4 { // after the inserted "XY"
		t.Errorf("cursor after insert = %d, want 4", f.fields[0].cursor)
	}

	// Backspace deletes the rune before the caret ("Y").
	key(tea.KeyBackspace)
	if got := f.fields[0].value; got != "maXin" {
		t.Errorf("backspace mid-value = %q, want %q", got, "maXin")
	}

	// Home, then forward-delete removes the first rune.
	key(tea.KeyHome)
	key(tea.KeyDelete)
	if got := f.fields[0].value; got != "aXin" {
		t.Errorf("delete at home = %q, want %q", got, "aXin")
	}

	// Right past the end clamps; End jumps to the end.
	key(tea.KeyEnd)
	if f.fields[0].cursor != len([]rune("aXin")) {
		t.Errorf("end: cursor = %d, want %d", f.fields[0].cursor, len([]rune("aXin")))
	}
	key(tea.KeyRight) // no-op at end
	if f.fields[0].cursor != len([]rune("aXin")) {
		t.Errorf("right at end should clamp, got %d", f.fields[0].cursor)
	}

	// A key editKey does not own falls through to the caller unconsumed.
	if f.focused().editKey(tea.KeyMsg{Type: tea.KeyEnter}) {
		t.Error("editKey must not consume Enter - the form owns submit")
	}
}

// Switching fields moves the caret to the end of the newly focused field.
func TestFormFocusResetsCursorToEnd(t *testing.T) {
	f := &formModel{fields: []formField{{value: "abc"}, {value: "hello"}}}
	f.focusEnd()
	f.next()
	if f.focus != 1 || f.fields[1].cursor != 5 {
		t.Errorf("next: focus=%d cursor=%d, want 1/5", f.focus, f.fields[1].cursor)
	}
	f.prev()
	if f.focus != 0 || f.fields[0].cursor != 3 {
		t.Errorf("prev: focus=%d cursor=%d, want 0/3", f.focus, f.fields[0].cursor)
	}
}
