package tui

import (
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
	tea "github.com/charmbracelet/bubbletea"
)

// typeAnswer feeds a string into the answer input one rune at a time, as if
// typed, and returns the resulting model.
func typeAnswer(m monitorModel, s string) monitorModel {
	for _, ch := range s {
		got, _ := m.updateAnswering(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{ch}})
		m = got.(monitorModel)
	}
	return m
}

func TestUpdateAnsweringPasteInline(t *testing.T) {
	m := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}

	// Type a lead-in, then paste a 3-line blob.
	m = typeAnswer(m, "I want this class:")
	got, _ := m.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune("a\nb\nc")})
	m = got.(monitorModel)

	// The paste is one atom placed after the typed text; cursor sits past it.
	if m.answer.in.cursor != len(m.answer.in.atoms) {
		t.Errorf("cursor should be at end: cursor=%d len=%d", m.answer.in.cursor, len(m.answer.in.atoms))
	}
	// A second, independent 2-line paste must count as 2 - not merge with the
	// first paste's last line (the old accumulation bug reported "+1").
	got, _ = m.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune("d\ne")})
	m = got.(monitorModel)

	var blobs []string
	for _, a := range m.answer.in.atoms {
		if a.blob != "" {
			blobs = append(blobs, a.blob)
		}
	}
	if len(blobs) != 2 {
		t.Fatalf("want 2 distinct paste atoms, got %d", len(blobs))
	}
	if ticket.LineCount(blobs[0]) != 3 || ticket.LineCount(blobs[1]) != 2 {
		t.Errorf("paste line counts: got %d and %d, want 3 and 2", ticket.LineCount(blobs[0]), ticket.LineCount(blobs[1]))
	}

	// Submitting expands pastes to their full bodies interleaved with the text.
	full := m.answer.in.text()
	if !strings.Contains(full, "I want this class:") || !strings.Contains(full, "a\nb\nc") || !strings.Contains(full, "d\ne") {
		t.Errorf("assembled answer missing parts: %q", full)
	}
}

func TestUpdateAnsweringManyPastes(t *testing.T) {
	m := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}

	// Interleave a typed word and a paste, five times over. Nothing caps the
	// number of pastes - each is its own atom.
	// All multi-line so each stays a collapsed chip (short single-line pastes
	// inline as text - covered separately).
	pastes := []string{"a\nb", "c\nd\ne", "f\ng", "g\nh\ni\nj", "k\nl"}
	wantLines := []int{2, 3, 2, 4, 2}
	for _, p := range pastes {
		m = typeAnswer(m, "word ")
		got, _ := m.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune(p)})
		m = got.(monitorModel)
	}

	var got []int
	for _, a := range m.answer.in.atoms {
		if a.blob != "" {
			got = append(got, ticket.LineCount(a.blob))
		}
	}
	if len(got) != len(pastes) {
		t.Fatalf("want %d distinct pastes, got %d", len(pastes), len(got))
	}
	for i := range wantLines {
		if got[i] != wantLines[i] {
			t.Errorf("paste %d line count: got %d, want %d", i, got[i], wantLines[i])
		}
	}
}

func TestUpdateAnsweringShortPasteInlinesAsText(t *testing.T) {
	// A short single-line paste becomes literal typed text - no paste atom, no
	// "[1 line added]" chip.
	m := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}
	got, _ := m.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune("https://example.com/x")})
	m = got.(monitorModel)
	for _, a := range m.answer.in.atoms {
		if a.blob != "" {
			t.Fatalf("short single-line paste should not create a paste atom")
		}
	}
	if m.answer.in.text() != "https://example.com/x" {
		t.Errorf("short paste text: got %q", m.answer.in.text())
	}
	if m.answer.in.cursor != len([]rune("https://example.com/x")) {
		t.Errorf("cursor should be past the inlined text: got %d", m.answer.in.cursor)
	}

	// A single line at/over the cutoff stays a collapsed paste atom.
	long := strings.Repeat("z", pasteInlineMaxRunes+1)
	m2 := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}
	got2, _ := m2.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune(long)})
	m2 = got2.(monitorModel)
	blobs := 0
	for _, a := range m2.answer.in.atoms {
		if a.blob != "" {
			blobs++
		}
	}
	if blobs != 1 {
		t.Errorf("long single-line paste should stay one paste atom, got %d", blobs)
	}

	// A multi-line paste, even a short one, stays a paste atom.
	m3 := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}
	got3, _ := m3.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune("a\nb")})
	m3 = got3.(monitorModel)
	if len(m3.answer.in.atoms) != 1 || m3.answer.in.atoms[0].blob == "" {
		t.Errorf("multi-line paste should stay one paste atom: %+v", m3.answer.in.atoms)
	}
}

func TestUpdateAnsweringEscClears(t *testing.T) {
	m := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}
	m = typeAnswer(m, "hello")
	got, _ := m.updateAnswering(tea.KeyMsg{Type: tea.KeyEsc})
	mm := got.(monitorModel)
	if mm.answer.open || mm.answer.in.atoms != nil || mm.answer.in.cursor != 0 {
		t.Error("Esc should clear answering, answerAtoms, and answerCursor")
	}
}

func TestUpdateAnsweringCursor(t *testing.T) {
	m := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}
	m = typeAnswer(m, "abc")
	if m.answer.in.text() != "abc" || m.answer.in.cursor != 3 {
		t.Errorf("after typing abc: text=%q cursor=%d", m.answer.in.text(), m.answer.in.cursor)
	}

	// Left, then insert "X" mid-string → "abXc".
	got, _ := m.updateAnswering(tea.KeyMsg{Type: tea.KeyLeft})
	m = got.(monitorModel)
	if m.answer.in.cursor != 2 {
		t.Errorf("after Left: cursor=%d, want 2", m.answer.in.cursor)
	}
	m = typeAnswer(m, "X")
	if m.answer.in.text() != "abXc" || m.answer.in.cursor != 3 {
		t.Errorf("insert mid: text=%q cursor=%d", m.answer.in.text(), m.answer.in.cursor)
	}

	// Backspace deletes "X" → "abc", cursor=2.
	got, _ = m.updateAnswering(tea.KeyMsg{Type: tea.KeyBackspace})
	m = got.(monitorModel)
	if m.answer.in.text() != "abc" || m.answer.in.cursor != 2 {
		t.Errorf("backspace: text=%q cursor=%d", m.answer.in.text(), m.answer.in.cursor)
	}

	// Left clamps at 0.
	for i := 0; i < 10; i++ {
		got, _ = m.updateAnswering(tea.KeyMsg{Type: tea.KeyLeft})
		m = got.(monitorModel)
	}
	if m.answer.in.cursor != 0 {
		t.Errorf("cursor clamped at 0: got %d", m.answer.in.cursor)
	}
}

func TestUpdateAnsweringBackspaceDeletesWholePaste(t *testing.T) {
	m := monitorModel{
		answer: answerState{open: true, ticket: "K1"},
	}
	m = typeAnswer(m, "hi")
	got, _ := m.updateAnswering(tea.KeyMsg{Paste: true, Runes: []rune("x\ny\nz")})
	m = got.(monitorModel)

	// One backspace removes the entire paste chunk, not one character of it.
	got, _ = m.updateAnswering(tea.KeyMsg{Type: tea.KeyBackspace})
	m = got.(monitorModel)
	if m.answer.in.text() != "hi" {
		t.Errorf("backspace over paste should remove it whole: got %q", m.answer.in.text())
	}
}
