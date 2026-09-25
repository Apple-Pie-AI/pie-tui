package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Form styles: the label (description) is muted, the editable value uses the
// primary color (same as "Start new ticket") so it stands out from the prose;
// the focused field brightens both and gets a ▸ marker.
// formField is one editable line in a form. secret fields render masked and are
// meant for keychain storage, never the config file. cursor is the rune index
// of the edit caret within value (0..len(value)).
//
// key names which setting the line edits. It exists because the form's producer
// (configFields) and its consumer (applyConfigFields) used to be coupled by
// nothing but list position: inserting a row in the middle of one wrote every
// later value into the wrong config key, and config.Save persisted the result.
// Fields built ad hoc (the id/branch prompts reuse formField purely as a caret
// editor) leave it empty.
type formField struct {
	key    string
	label  string
	value  string
	secret bool
	cursor int
}

// formModel is a minimal hand-rolled multi-field form (no bubbles dependency).
// submit receives the filled fields and returns a tea.Msg with the result.
type formModel struct {
	title  string
	note   string
	fields []formField
	focus  int
	submit func(fields []formField) tea.Msg
}

// ---- single-line caret editing (shared by the form and the id/branch prompts) ----

// clamp keeps the caret within [0, len(value)] and returns the value's runes
// and the clamped position.
func (f *formField) clamp() ([]rune, int) {
	r := []rune(f.value)
	if f.cursor < 0 {
		f.cursor = 0
	}
	if f.cursor > len(r) {
		f.cursor = len(r)
	}
	return r, f.cursor
}

// insert inserts s at the caret and advances it.
func (f *formField) insert(s string) {
	r, pos := f.clamp()
	ins := []rune(s)
	out := make([]rune, 0, len(r)+len(ins))
	out = append(out, r[:pos]...)
	out = append(out, ins...)
	out = append(out, r[pos:]...)
	f.value = string(out)
	f.cursor = pos + len(ins)
}

// backspace deletes the rune before the caret.
func (f *formField) backspace() {
	r, pos := f.clamp()
	if pos > 0 {
		f.value = string(r[:pos-1]) + string(r[pos:])
		f.cursor = pos - 1
	}
}

// deleteForward deletes the rune at the caret (the Delete key).
func (f *formField) deleteForward() {
	r, pos := f.clamp()
	if pos < len(r) {
		f.value = string(r[:pos]) + string(r[pos+1:])
	}
}

func (f *formField) left() {
	if _, pos := f.clamp(); pos > 0 {
		f.cursor = pos - 1
	}
}

func (f *formField) right() {
	if r, pos := f.clamp(); pos < len(r) {
		f.cursor = pos + 1
	}
}

func (f *formField) home() { f.cursor = 0 }
func (f *formField) end()  { f.cursor = len([]rune(f.value)) }

// caretView returns the value with the caret glyph drawn at the cursor.
func (f formField) caretView() string {
	r, pos := f.clamp()
	return string(r[:pos]) + "▌" + string(r[pos:])
}

// editKey applies a caret-editing key to the field. It reports whether the key
// was an editing key (callers fall through to their own bindings otherwise).
func (f *formField) editKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyLeft:
		f.left()
	case tea.KeyRight:
		f.right()
	case tea.KeyHome:
		f.home()
	case tea.KeyEnd:
		f.end()
	case tea.KeyBackspace:
		f.backspace()
	case tea.KeyDelete:
		f.deleteForward()
	case tea.KeySpace:
		f.insert(" ")
	case tea.KeyRunes:
		f.insert(string(msg.Runes))
	default:
		return false
	}
	return true
}

// ---- formModel: delegate editing to the focused field ----

func (f *formModel) focused() *formField { return &f.fields[f.focus] }

// typeRunes is the one delegation worth a name: the paste path inserts text
// without a keystroke to hand to editKey. The other six wrappers (backspace,
// deleteForward, left, right, home, end) existed only to let updateForm restate
// editKey's switch, and went with it.
func (f *formModel) typeRunes(s string) { f.focused().insert(s) }

// focusEnd places the caret at the end of the focused field - the natural spot
// when a field gains focus (e.g. on open or after tab).
func (f *formModel) focusEnd() {
	f.fields[f.focus].cursor = len([]rune(f.fields[f.focus].value))
}

// focusField moves focus to the field with the given key (no-op if absent),
// caret at its end. Lets a caller deep-link a form open straight to the field a
// preflight flagged, instead of dumping the user at the top to find it.
func (f *formModel) focusField(key string) {
	for i, fld := range f.fields {
		if fld.key == key {
			f.focus = i
			f.focusEnd()
			return
		}
	}
}

func (f *formModel) next() {
	if f.focus < len(f.fields)-1 {
		f.focus++
		f.focusEnd()
	}
}

func (f *formModel) prev() {
	if f.focus > 0 {
		f.focus--
		f.focusEnd()
	}
}

func (f *formModel) render(w int) string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("  "+f.title) + "\n")
	if f.note != "" {
		b.WriteString(dimStyle.Render("  "+f.note) + "\n")
	}
	b.WriteString("\n")
	for i, fld := range f.fields {
		val := fld.value
		if fld.secret {
			val = strings.Repeat("•", len([]rune(fld.value)))
		}
		focused := i == f.focus

		marker, labelSty, valSty := "  ", formLabelStyle, formValStyle
		if focused {
			marker, labelSty, valSty = formMarkFocus.Render("▸ "), formLabelFocus, formValFocus
			// Draw the caret inside the (possibly masked) value.
			val = formField{value: val, cursor: fld.cursor}.caretView()
		}

		// Truncate the plain value to the space left after the label, then style
		// (styling first would let truncate cut an ANSI escape).
		prefix := "  " + fld.label + ": "
		avail := w - lipgloss.Width(prefix)
		if avail < 6 {
			avail = 6
		}
		shown := valSty.Render(truncate(val, avail))
		if val == "" && !focused {
			shown = dimStyle.Render("(empty)")
		}
		b.WriteString(marker + labelSty.Render(fld.label+":") + " " + shown + "\n")
	}
	b.WriteString("\n  " + dimStyle.Render("tab/↑↓ move · ←→ edit · type to change · enter save · esc cancel"))
	return b.String()
}
