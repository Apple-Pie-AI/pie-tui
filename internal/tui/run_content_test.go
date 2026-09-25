package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	tea "github.com/charmbracelet/bubbletea"
)

// Pasting content opens the id prompt (step 1) pre-filled with the detected
// id; confirming it computes the branch pre-fill from the CONFIRMED id and
// opens the branch prompt (step 2). Regression coverage for the id prompt
// showing up FIRST, with no start-mode question in between - that question
// only belongs to the dashboard's separate "Start from existing branch" entry
// (see run_start_test.go), not this scratch-ticket path.
func TestContentFlowIDThenBranch(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(&config.Config{
		Repos: []config.Repo{{Path: "/r", Branch: "pie/{ticket}"}},
	}); err != nil {
		t.Fatal(err)
	}
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}
	nm, _ := m.addContentTicket("PROJ-123 Fix the crash\nmore detail")
	hub := nm.(monitorModel)
	if len(hub.run.tickets) != 1 {
		t.Fatalf("expected 1 chip, got %+v", hub.run.tickets)
	}
	if hub.run.idPrompt != 0 || hub.run.branchPrompt != -1 {
		t.Fatalf("paste should open the id prompt first: idPrompt=%d branchPrompt=%d",
			hub.run.idPrompt, hub.run.branchPrompt)
	}
	if hub.run.tickets[0].id != "PROJ-123" {
		t.Errorf("detected id should pre-fill, got %q", hub.run.tickets[0].id)
	}
	if hub.run.tickets[0].branch != "" {
		t.Errorf("branch must not be set before the id is confirmed, got %q", hub.run.tickets[0].branch)
	}

	// The user fixes the id, then confirms: the branch pre-fill must use the
	// EDITED id, and the flow advances to the branch prompt.
	hub.run.tickets[0].id = "PROJ-777"
	nm, _ = hub.updateRunIDPrompt(tea.KeyMsg{Type: tea.KeyEnter})
	hub = nm.(monitorModel)
	if hub.run.idPrompt != -1 || hub.run.branchPrompt != 0 {
		t.Errorf("id Enter should advance to the branch prompt: idPrompt=%d branchPrompt=%d",
			hub.run.idPrompt, hub.run.branchPrompt)
	}
	if got := hub.run.tickets[0].branch; got != "pie/proj-777" {
		t.Errorf("branch pre-fill should use the confirmed id: got %q, want %q", got, "pie/proj-777")
	}
}

// Content mode is a real multi-line editor: Enter inserts a NEW LINE (it never
// creates a chip), and ctrl+d confirms the composed markdown into a chip that
// enters the id-confirm step, resetting the editor.
func TestContentEditorEnterIsNewlineCtrlDConfirms(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}.withContentEditor()
	step := func(msg tea.KeyMsg) {
		nm, _ := m.updateRunContent(msg)
		m = nm.(monitorModel)
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("PROJ-5 Fix the crash")})
	step(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.run.tickets) != 0 {
		t.Fatalf("Enter must insert a newline, not create a chip: %+v", m.run.tickets)
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("More detail")})
	if got := m.run.content.Value(); !strings.Contains(got, "\n") {
		t.Fatalf("editor should be multi-line after Enter, got %q", got)
	}
	step(tea.KeyMsg{Type: tea.KeyCtrlD})
	if len(m.run.tickets) != 1 || m.run.tickets[0].kind != "content" {
		t.Fatalf("ctrl+d should create a content chip, got %+v", m.run.tickets)
	}
	if m.run.tickets[0].id != "PROJ-5" || m.run.idPrompt != 0 {
		t.Errorf("chip should enter the id-confirm step: id=%q idPrompt=%d",
			m.run.tickets[0].id, m.run.idPrompt)
	}
	if m.run.tickets[0].lines != 2 {
		t.Errorf("chip should carry both lines, got %d", m.run.tickets[0].lines)
	}
	if m.run.content.Value() != "" {
		t.Errorf("editor should reset after confirm, got %q", m.run.content.Value())
	}
}

// Esc in the editor steps out to the action bar (it must NOT cancel and
// destroy the draft); Enter on Continue confirms the chip; Esc on the bar
// returns to editing; Discard is the only way Esc-ish input drops the draft.
func TestContentEditorEscFocusesActionBar(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}.withContentEditor()
	step := func(msg tea.KeyMsg) {
		nm, _ := m.updateRunContent(msg)
		m = nm.(monitorModel)
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("PROJ-8 A draft")})
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if m.view != viewRunInput || m.run.content == nil || m.run.content.Value() != "PROJ-8 A draft" {
		t.Fatal("Esc must keep the draft and stay on the screen")
	}
	if !m.run.bar || m.run.btn != 0 {
		t.Fatalf("Esc should focus the action bar on Continue: bar=%v btn=%d", m.run.bar, m.run.btn)
	}

	// Esc on the bar → back to editing, draft intact.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if m.run.bar || m.run.content.Value() != "PROJ-8 A draft" {
		t.Fatal("Esc on the bar should return to editing with the draft intact")
	}

	// Esc → Enter (Continue) confirms the ticket into the id step.
	step(tea.KeyMsg{Type: tea.KeyEsc})
	step(tea.KeyMsg{Type: tea.KeyEnter})
	if len(m.run.tickets) != 1 || m.run.tickets[0].id != "PROJ-8" || m.run.idPrompt != 0 {
		t.Fatalf("Continue should confirm the chip into the id step, got %+v idPrompt=%d",
			m.run.tickets, m.run.idPrompt)
	}
}

// Discard on the action bar cancels the whole creation.
func TestContentEditorDiscard(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}.withContentEditor()
	step := func(msg tea.KeyMsg) {
		nm, _ := m.updateRunContent(msg)
		m = nm.(monitorModel)
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("scrap this")})
	step(tea.KeyMsg{Type: tea.KeyEsc})
	step(tea.KeyMsg{Type: tea.KeyRight})
	step(tea.KeyMsg{Type: tea.KeyRight})
	if m.run.btn != 2 {
		t.Fatalf("→→ should land on Discard, got btn=%d", m.run.btn)
	}
	step(tea.KeyMsg{Type: tea.KeyEnter})
	if m.view != viewDashboard || m.run.tickets != nil || m.run.content != nil {
		t.Errorf("Discard should cancel the creation: view=%d tickets=%+v", m.view, m.run.tickets)
	}
}

// A paste lands IN the editor (for review/editing) instead of instantly
// becoming a chip, and ctrl+p toggles the markdown preview; esc in the preview
// returns to editing rather than cancelling the creation.
func TestContentEditorPasteAndPreview(t *testing.T) {
	m := monitorModel{
		view: viewRunInput,
		run: newRun(func(r *runState) {
			r.mode = "content"
			r.idPrompt = -1
			r.branchPrompt = -1
			r.imgPrompt = -1
		}),
	}.withContentEditor()
	step := func(msg tea.KeyMsg) {
		nm, _ := m.updateRunContent(msg)
		m = nm.(monitorModel)
	}
	step(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("# Title\rpasted body"), Paste: true})
	if len(m.run.tickets) != 0 {
		t.Fatalf("paste must fill the editor, not create a chip: %+v", m.run.tickets)
	}
	if got := m.run.content.Value(); got != "# Title\npasted body" {
		t.Fatalf("paste should land in the editor (\\r normalized), got %q", got)
	}
	step(tea.KeyMsg{Type: tea.KeyCtrlP})
	if !m.run.preview || len(m.run.ticketLines) == 0 {
		t.Fatalf("ctrl+p should open the rendered preview: preview=%v lines=%d",
			m.run.preview, len(m.run.ticketLines))
	}
	step(tea.KeyMsg{Type: tea.KeyEsc})
	if m.run.preview {
		t.Error("esc in preview should return to editing")
	}
	if m.view != viewRunInput || m.run.content == nil {
		t.Error("esc in preview must not cancel the creation")
	}
}

// No detectable ticket id → the pattern expands with the PASTED fallback id
// (matching ticket.WritePasted), never an empty field.
func TestInferBranchNoIDFallsBack(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if got := inferBranch("", "Fix the crash"); got != "pasted-fix-the-crash" {
		t.Errorf("inferBranch fallback: got %q, want %q", got, "pasted-fix-the-crash")
	}
}

// renderBodyPreview: first lines appear verbatim in a quoted block; empty body
// renders nothing.
func TestRenderBodyPreview(t *testing.T) {
	// Empty body → "".
	if got := renderBodyPreview("", 80); got != "" {
		t.Errorf("empty body should render nothing, got %q", got)
	}
	if got := renderBodyPreview("   \n  ", 80); got != "" {
		t.Errorf("whitespace-only body should render nothing, got %q", got)
	}

	// A 3-line body: all three lines verbatim, no "+N more" suffix.
	out := renderBodyPreview("line one\nline two\nline three", 80)
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "more lines") {
		t.Errorf("3-line body should have no +N suffix:\n%s", out)
	}
}

// renderBodyPreview: a >12-line body shows the first 12 and a "+N more lines".
func TestRenderBodyPreviewMore(t *testing.T) {
	var lines []string
	for i := 1; i <= 20; i++ {
		lines = append(lines, fmt.Sprintf("row%d", i))
	}
	out := renderBodyPreview(strings.Join(lines, "\n"), 80)
	if !strings.Contains(out, "row1") || !strings.Contains(out, "row12") {
		t.Errorf("first 12 rows should show:\n%s", out)
	}
	if strings.Contains(out, "row13") {
		t.Errorf("13th row must be truncated (only 12 shown):\n%s", out)
	}
	if !strings.Contains(out, "+8 more lines") {
		t.Errorf("20 lines should show '+8 more lines':\n%s", out)
	}
}
