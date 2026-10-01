package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// changeModel is a hub looking at a ticket parked at change-review, with a
// two-file diff already delivered (the tea.Cmd round trip is exercised by
// feeding changeDiffMsg through Update, stale-guard included).
func changeModel(t *testing.T) monitorModel {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Claim("C-1", "/repo", "add a use case"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetFields("C-1", "b", "/wt", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("C-1", store.StateChangeReview, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.SetChangeParked("C-1", "head:hash", 1); err != nil {
		t.Fatal(err)
	}

	m := baseModel()
	m.store = st
	m.width, m.height = 120, 40
	m.reload()
	sess, _ := st.Get("C-1")
	mm, _ := m.openChangeView(*sess)
	m = mm.(monitorModel)

	got, _ := m.Update(changeDiffMsg{
		ticket: "C-1",
		files: []changeFile{
			{path: "a/One.kt", status: "mod", adds: 4, dels: 1},
			{path: "b/Two.kt", status: "new", adds: 9},
		},
		diff: map[string][]string{
			"a/One.kt": {"@@ -1,3 +1,6 @@", "+added line", " context"},
			"b/Two.kt": {"@@ -0,0 +1,9 @@", "+new file line"},
		},
	})
	// The screen opens in the chat box; most tests drive the file list.
	return chPress(t, got.(monitorModel), "esc")
}

func TestChangeOpensWithChatFocused(t *testing.T) {
	m := changeModel(t)
	sess, _ := m.store.Get("C-1")
	mm, _ := m.openChangeView(*sess)
	m = mm.(monitorModel)
	if m.change.mode != chBox || !m.change.draft.Focused() {
		t.Fatalf("entry must focus the chat box: mode=%d focused=%v", m.change.mode, m.change.draft.Focused())
	}
	m = chType(t, m, "hi")
	if got := m.change.draft.Value(); got != "hi" {
		t.Fatalf("typing on entry must reach the box, got %q", got)
	}
	m = chPress(t, m, "esc")
	if m.change.mode != chSplit || m.change.cursor != 0 {
		t.Fatalf("esc from the entry box must land on the list's top row: mode=%d cursor=%d", m.change.mode, m.change.cursor)
	}
}

func TestChangeBoxShowsCaretOnlyWhenFocused(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	const reverse = "\x1b[7m"
	m := changeModel(t)
	if strings.Contains(strings.Join(m.renderChangeBoxInput(100), "\n"), reverse) {
		t.Fatal("an unfocused box must not draw a caret")
	}
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down")
	if !strings.Contains(strings.Join(m.renderChangeBoxInput(100), "\n"), reverse) {
		t.Fatal("the focused, empty box must draw a caret")
	}
	m = chType(t, m, "fix it")
	if !strings.Contains(strings.Join(m.renderChangeBoxInput(100), "\n"), reverse) {
		t.Fatal("the focused box with text must draw a caret")
	}
}

func chPress(t *testing.T, m monitorModel, keys ...string) monitorModel {
	t.Helper()
	for _, k := range keys {
		got, _ := m.updateChange(keyOf(k))
		m = got.(monitorModel)
	}
	return m
}

// chType sends each rune of s through updateChange as a KeyRunes message.
func chType(t *testing.T, m monitorModel, s string) monitorModel {
	t.Helper()
	for _, r := range s {
		got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = got.(monitorModel)
	}
	return m
}

// The key table: split (Summary → file, one down each since there's no more
// "All files" row) → full (→) → next file (↓) → scroll (enter) → back out
// one level per ← → esc from split leaves for the dashboard.
func TestChangeScreenKeyTable(t *testing.T) {
	m := changeModel(t)
	if m.view != viewChange || m.change.mode != chSplit {
		t.Fatalf("open: view=%v mode=%v", m.view, m.change.mode)
	}
	m = chPress(t, m, "down") // Summary -> file 0
	if m.change.fileAt(m.change.cursor) == nil {
		t.Fatalf("cursor = %d, want the first file", m.change.cursor)
	}
	m = chPress(t, m, "right")
	if m.change.mode != chFull || m.change.fileIdx != 0 {
		t.Fatalf("→ must open full screen on the file: mode=%v idx=%d", m.change.mode, m.change.fileIdx)
	}
	m = chPress(t, m, "down")
	if m.change.fileIdx != 1 {
		t.Fatalf("↓ in full screen must go to the next file, idx=%d", m.change.fileIdx)
	}
	m = chPress(t, m, "enter")
	if m.change.mode != chScroll {
		t.Fatalf("enter in full screen must enter scroll mode, mode=%v", m.change.mode)
	}
	m = chPress(t, m, "left")
	if m.change.mode != chFull {
		t.Fatalf("← must exit scroll to full screen, mode=%v", m.change.mode)
	}
	m = chPress(t, m, "left")
	if m.change.mode != chSplit || m.change.fileAt(m.change.cursor) == nil {
		t.Fatalf("← must return to split with the cursor on the file: mode=%v cursor=%d", m.change.mode, m.change.cursor)
	}
	m = chPress(t, m, "esc")
	if m.view != viewDashboard {
		t.Fatalf("esc from split must return to the dashboard, view=%v", m.view)
	}
}

// Enter on a file row inserts "@path " and focuses the box; Esc returns to
// that exact file row, draft kept.
func TestChangeCommentOnFileOpensBoxAndEscReturns(t *testing.T) {
	m := changeModel(t)
	m = chPress(t, m, "down") // file 0
	fileCursor := m.change.cursor
	m = chPress(t, m, "enter")
	if m.change.mode != chBox {
		t.Fatalf("enter on a file must open the box, mode=%v", m.change.mode)
	}
	if got := m.change.draft.Value(); got != "@a/One.kt " {
		t.Fatalf("draft = %q, want the file mention", got)
	}
	m = chPress(t, m, "esc")
	if m.change.mode != chSplit || m.change.cursor != fileCursor {
		t.Fatalf("esc must return to the same file row: mode=%v cursor=%d (want %d)", m.change.mode, m.change.cursor, fileCursor)
	}
	if got := m.change.draft.Value(); got != "@a/One.kt " {
		t.Fatalf("esc must keep the draft, got %q", got)
	}
}

// A mention appends to existing text rather than replacing it.
func TestChangeCommentOnFileAppendsToExistingDraft(t *testing.T) {
	m := changeModel(t)
	m = chPress(t, m, "down") // general typing first, via file 0's box
	m = chPress(t, m, "enter")
	m = chType(t, m, "hello")
	m = chPress(t, m, "esc")
	// Now comment on the second file - must append, not clobber "hello".
	m = chPress(t, m, "down")
	m = chPress(t, m, "enter")
	got := m.change.draft.Value()
	if !strings.HasPrefix(got, "@a/One.kt hello") || !strings.Contains(got, "@b/Two.kt") {
		t.Fatalf("mention must append to the existing draft, got %q", got)
	}
}

// ↓ past the CTA focuses the box empty (general feedback).
func TestChangeDownPastApproveFocusesEmptyBox(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down")
	if m.change.mode != chBox {
		t.Fatalf("down past the CTA must open the box, mode=%v", m.change.mode)
	}
	if got := m.change.draft.Value(); got != "" {
		t.Fatalf("general feedback must start empty, got %q", got)
	}
}

// ↑ on the box's first visual line exits to the list's top row, "PR
// description", regardless of how the box was opened - distinct from Esc,
// which returns to the file/line.
func TestChangeBoxUpAtFirstLineExitsToPRDescription(t *testing.T) {
	m := changeModel(t)
	m = chPress(t, m, "down") // file 0
	m = chPress(t, m, "enter")
	m = chPress(t, m, "up") // single short line -> RowOffset is 0
	if m.change.mode != chSplit || m.change.cursor != 0 {
		t.Fatalf("up at the first line must land on PR description: mode=%v cursor=%d", m.change.mode, m.change.cursor)
	}
}

// The focused box names where ↑ goes, so the list above is discoverable.
func TestChangeBoxHintsUpToPRDescription(t *testing.T) {
	m := changeModel(t)
	const hint = "↑ PR description & changed files"
	if strings.Contains(stripAnsiStr(strings.Join(m.renderChangeBoxInput(100), "\n")), hint) {
		t.Fatal("the unfocused box must not show the ↑ hint")
	}
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down")
	if !strings.Contains(stripAnsiStr(strings.Join(m.renderChangeBoxInput(100), "\n")), hint) {
		t.Fatal("the focused box must show the ↑ hint")
	}
}

// Send: empty draft is a no-op with a notice, in either mode.
func TestChangeSendFeedbackEmptyIsANoOp(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // empty box
	got, cmd := m.updateChange(keyOf("enter"))
	m = got.(monitorModel)
	if cmd != nil || m.notice != "nothing to send" {
		t.Fatalf("empty send: notice=%q cmd=%v", m.notice, cmd)
	}
}

// Plan mode (the default) sends a message without leaving the screen -
// change-review's whole point for this mode is that it stays open.
func TestChangeSendFeedbackPlanModeStaysOnScreen(t *testing.T) {
	m := changeModel(t)
	if !m.change.planMode {
		t.Fatal("Plan mode must be the default")
	}
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // empty box
	m = chType(t, m, "what does this do?")
	got, cmd := m.updateChange(keyOf("enter"))
	m = got.(monitorModel)
	if cmd == nil || m.view != viewChange || !strings.Contains(m.notice, "asking") {
		t.Fatalf("plan-mode send must spawn and stay: cmd=%v view=%v notice=%q", cmd, m.view, m.notice)
	}
	if !m.change.discussing {
		t.Error("plan-mode send must optimistically mark a round as running")
	}
	if got := m.change.draft.Value(); got != "" {
		t.Errorf("the box must clear after sending, got %q", got)
	}
	sess, _ := m.store.Get("C-1")
	if sess.ChangeNotes != "what does this do?" {
		t.Fatalf("message not queued (ChangeNotes): %q", sess.ChangeNotes)
	}
}

// Auto mode doesn't spawn on a bare Enter - it arms the explicit
// "start implementing?" confirm, matching the Approve CTA's own two-step
// pattern; confirming it is what actually spawns --rework and leaves.
func TestChangeSendFeedbackAutoModeRequiresConfirm(t *testing.T) {
	m := changeModel(t)
	m.change.planMode = false // Auto mode (toggled via shift+tab in the box - see TestChangeBoxShiftTabTogglesModeAndShowsClaudeCodeStatusLine)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // empty box
	m = chType(t, m, "please rename it")

	got, cmd := m.updateChange(keyOf("enter"))
	m = got.(monitorModel)
	if cmd != nil || m.view != viewChange || !m.change.reworkConfirmOpen {
		t.Fatalf("auto-mode send must arm the confirm, not spawn yet: cmd=%v view=%v confirmOpen=%v",
			cmd, m.view, m.change.reworkConfirmOpen)
	}

	got, cmd = m.updateChange(keyOf("enter")) // Confirm (default selection)
	m = got.(monitorModel)
	if cmd == nil || m.view != viewDashboard || !strings.Contains(m.notice, "reworking") {
		t.Fatalf("confirming must spawn and leave: cmd=%v view=%v notice=%q", cmd, m.view, m.notice)
	}
	sess, _ := m.store.Get("C-1")
	if sess.ChangeNotes != "please rename it" {
		t.Fatalf("draft not persisted before spawning: %q", sess.ChangeNotes)
	}
}

// Cancelling the auto-mode confirm returns to the box with the draft intact.
func TestChangeSendFeedbackAutoModeConfirmCancel(t *testing.T) {
	m := changeModel(t)
	m.change.planMode = false
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down")
	m = chType(t, m, "please rename it")
	got, _ := m.updateChange(keyOf("enter")) // arm the confirm
	m = got.(monitorModel)

	got, _ = m.updateChange(keyOf("down")) // move to Cancel
	m = got.(monitorModel)
	got, cmd := m.updateChange(keyOf("enter")) // confirm Cancel
	m = got.(monitorModel)
	if cmd != nil || m.change.reworkConfirmOpen || m.view != viewChange {
		t.Fatalf("cancel must close the confirm and stay: cmd=%v confirmOpen=%v view=%v", cmd, m.change.reworkConfirmOpen, m.view)
	}
	if got := m.change.draft.Value(); got != "please rename it" {
		t.Errorf("cancel must keep the draft, got %q", got)
	}
}

// Approve: Enter on the CTA opens the inline confirm; Cancel collapses back
// to the CTA; Create pull request spawns --ship-change.
func TestChangeApproveConfirm(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "enter")
	if !m.change.approveOpen {
		t.Fatal("enter on the CTA must open the confirm")
	}
	m = chPress(t, m, "down", "enter") // Cancel
	if m.change.approveOpen {
		t.Fatal("Cancel must close the confirm")
	}
	if m.change.cursor != m.change.ctaAt() {
		t.Fatalf("cursor after cancel = %d, want the CTA (%d)", m.change.cursor, m.change.ctaAt())
	}

	m = chPress(t, m, "enter") // reopen
	got, cmd := m.updateChange(keyOf("enter"))
	m = got.(monitorModel)
	if cmd == nil || m.view != viewDashboard || !strings.Contains(m.notice, "shipping") {
		t.Fatalf("Create pull request must spawn: cmd=%v view=%v notice=%q", cmd, m.view, m.notice)
	}
}

// The @ picker: typing @ opens it, changed files lead, a repo-only file also
// matches, Enter inserts the selection (replacing the partial query) and
// keeps focus in the box.
func TestChangePickerFiltersAndInserts(t *testing.T) {
	m := changeModel(t)
	m.change.repoFiles = []string{"a/One.kt", "b/Two.kt", "c/Repo.kt"}
	m.change.repoFilesLoaded = true
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // empty box
	m = chType(t, m, "@")
	if !m.change.picker.open {
		t.Fatal("typing @ must open the picker")
	}
	if len(m.change.picker.items) == 0 || m.change.picker.items[0] != "a/One.kt" {
		t.Fatalf("changed files must lead the picker: %v", m.change.picker.items)
	}
	m = chType(t, m, "repo")
	found := false
	for _, it := range m.change.picker.items {
		if it == "c/Repo.kt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("repo-only file must match the query: %v", m.change.picker.items)
	}
	got, _ := m.updateChange(keyOf("enter"))
	m = got.(monitorModel)
	if m.change.picker.open {
		t.Fatal("enter must close the picker")
	}
	if got := m.change.draft.Value(); got != "c/Repo.kt " {
		t.Fatalf("enter must replace the partial query with the full path, got %q", got)
	}
	if m.change.mode != chBox {
		t.Fatal("inserting from the picker must keep focus in the box")
	}
}

// Backspace past the "@" and Space both close the picker; the character
// itself still reaches the draft.
func TestChangePickerClosesOnSpaceAndEmptyBackspace(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down")
	m = chType(t, m, "@")
	// A real space keypress carries Runes=[' '] even though its Type is
	// KeySpace (bubbletea's decoder, key.go) - keyOf's shared " " case omits
	// Runes, so construct the real shape directly rather than widen a helper
	// other tests depend on.
	got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = got.(monitorModel)
	if m.change.picker.open {
		t.Fatal("space must close the picker")
	}
	if got := m.change.draft.Value(); got != "@ " {
		t.Fatalf("the space itself must still land in the draft, got %q", got)
	}

	m2 := changeModel(t)
	m2.change.cursor = m2.change.ctaAt()
	m2 = chPress(t, m2, "down")
	m2 = chType(t, m2, "@")
	got2, _ := m2.updateChange(tea.KeyMsg{Type: tea.KeyBackspace})
	m2 = got2.(monitorModel)
	if m2.change.picker.open {
		t.Fatal("backspacing an empty query must close the picker")
	}
}

// Draft persistence: what's typed survives leaving to the dashboard and
// re-opening the screen.
func TestChangeDraftPersistsAcrossReopen(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down")
	m = chType(t, m, "keep me")
	m = chPress(t, m, "esc")
	sess, _ := m.store.Get("C-1")
	if sess.ChangeNotes != "keep me" {
		t.Fatalf("draft not persisted on esc: %q", sess.ChangeNotes)
	}
	mm, _ := m.openChangeView(*sess)
	m = mm.(monitorModel)
	if got := m.change.draft.Value(); got != "keep me" {
		t.Fatalf("draft lost on re-open: %q", got)
	}
}

// Comment on a line in scroll mode inserts @path:line and opens the box;
// Esc returns to the same line.
func TestChangeCommentOnLine(t *testing.T) {
	m := changeModel(t)
	m = chPress(t, m, "down", "right", "enter") // file 0, full, scroll
	m.change.lineCur = 1                        // the "+added line" row
	m = chPress(t, m, "enter")
	if m.change.mode != chBox || m.change.boxOrigin.mode != chScroll {
		t.Fatalf("enter in scroll must open the box from scroll origin: mode=%v origin=%v", m.change.mode, m.change.boxOrigin)
	}
	if got := m.change.draft.Value(); got != "@a/One.kt:1 " {
		t.Fatalf("draft = %q, want the line mention", got)
	}
	m = chPress(t, m, "esc")
	if m.change.mode != chScroll || m.change.lineCur != 1 {
		t.Fatalf("esc must return to the same line: mode=%v lineCur=%d", m.change.mode, m.change.lineCur)
	}
}

// The outer frame (view.go's frame()) always reserves 2 rows below the body
// for its own rule + action-bar line, even though this screen leaves that
// bar empty and draws its own footer inline - and frame() TRUNCATES the body
// to fit before those 2 rows, not after. A screen that budgets its own
// layout against the raw terminal height (not height-2) computes a diff pane
// tall enough that the trailing rule+box+footer spill past frame()'s real
// limit - so the footer (drawn last) is the first thing silently clipped.
// The reported symptom: the footer line was entirely missing from a real
// screenshot. Small synthetic diffs never reproduced it because they never
// came close to filling the render budget; this uses a realistically long
// one, at a moderate terminal height, through the FULL m.View() (frame
// included, not renderChange directly).
func TestChangeFooterSurvivesTheOuterFrameTruncation(t *testing.T) {
	m := changeModel(t)
	m.width, m.height = 100, 24

	long := make([]string, 0, 60)
	long = append(long, "@@ -1,60 +1,60 @@")
	for i := 1; i <= 58; i++ {
		long = append(long, fmt.Sprintf(" line %03d of a realistic diff", i))
	}
	got, _ := m.Update(changeDiffMsg{
		ticket: "C-1",
		files:  []changeFile{{path: "a/One.kt", status: "mod", adds: 30, dels: 28}},
		diff:   map[string][]string{"a/One.kt": long},
	})
	m = got.(monitorModel)
	m.change.cursor = 1 // the file, so its long diff is what fills the pane

	out := stripAnsiStr(m.View())
	for _, want := range []string{"Feedback", "esc dashboard", "↑↓ move"} {
		if !strings.Contains(out, want) {
			t.Errorf("the screen's own footer was clipped by the outer frame - missing %q:\n%s", want, out)
		}
	}
	// The rendered output must never exceed the terminal - frame() truncates
	// silently, so a regression here would pass invisibly without this check.
	if n := strings.Count(out, "\n") + 1; n > m.height {
		t.Errorf("rendered %d lines into a %d-line terminal", n, m.height)
	}
}

// The split view renders the screen's fixed furniture per the new layout.
func TestChangeRender(t *testing.T) {
	m := changeModel(t)
	out := stripAnsiStr(m.View())
	for _, want := range []string{
		"C-1", "add a use case", "Changed files", "+13", "−1",
		"Summary", "One.kt", "Two.kt",
		"Approve and create pull request",
		"↑↓ move", "esc dashboard",
		"Feedback",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("split view missing %q\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"All files", "Send feedback", "Stop task"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("split view must not show removed v1 furniture %q\n%s", unwanted, out)
		}
	}
	// Full screen announces the file position; scroll announces SCROLL.
	m = chPress(t, m, "down", "right")
	out = stripAnsiStr(m.View())
	if !strings.Contains(out, "file 1/2") {
		t.Errorf("full screen missing the file counter:\n%s", out)
	}
	m = chPress(t, m, "enter")
	out = stripAnsiStr(m.View())
	if !strings.Contains(out, "SCROLL") {
		t.Errorf("scroll mode not announced:\n%s", out)
	}
}

// changeDiffCmd's baselines against a real repo: the round tree shows only
// what moved since the feedback; showAll returns to the whole change.
func TestChangeDiffBaselines(t *testing.T) {
	wt := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "b")
	if err := os.WriteFile(filepath.Join(wt, "old.kt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	// The original change (round 1) edits old.kt...
	if err := os.WriteFile(filepath.Join(wt, "old.kt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := git.SnapshotTree(wt) // the feedback moment
	if err != nil {
		t.Fatal(err)
	}
	// ...and the rework adds new.kt.
	if err := os.WriteFile(filepath.Join(wt, "new.kt"), []byte("added\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg := changeDiffCmd(wt, "T", tree, 2, false, "")().(changeDiffMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if _, ok := msg.diff["new.kt"]; !ok {
		t.Errorf("round diff missing the rework's file: %v", msg.diff)
	}
	if _, ok := msg.diff["old.kt"]; ok {
		t.Errorf("round diff must not re-show the already-reviewed edit: %v", msg.diff)
	}

	// A round-1 park must never trust a leftover roundTree from an earlier,
	// unrelated change-review cycle on this same ticket (e.g. one that
	// already shipped a PR) - it must fall back to the real merge-base diff.
	stale := changeDiffCmd(wt, "T", tree, 1, false, "")().(changeDiffMsg)
	if stale.err != nil {
		t.Fatal(stale.err)
	}
	if _, ok := stale.diff["old.kt"]; !ok {
		t.Errorf("round 1 must ignore a stale roundTree and show the full diff: %v", stale.diff)
	}

	all := changeDiffCmd(wt, "T", tree, 2, true, "")().(changeDiffMsg)
	if all.err != nil {
		t.Fatal(all.err)
	}
	if _, ok := all.diff["old.kt"]; !ok {
		t.Errorf("show-all diff missing the original edit: %v", all.diff)
	}
	if _, ok := all.diff["new.kt"]; !ok {
		t.Errorf("show-all diff missing the rework's file: %v", all.diff)
	}
}

// Scrolling in scroll mode must actually move the line cursor through a long
// diff: enter scroll mode, press down repeatedly, and the visible window
// must advance - an early-only marker leaves view and a late-only marker
// enters it.
func TestChangeScrollAdvancesThroughLongDiff(t *testing.T) {
	m := changeModel(t)
	m.width, m.height = 120, 20 // a short terminal, so one file overflows it

	long := []string{"@@ -1,80 +1,80 @@"}
	for i := 1; i <= 80; i++ {
		long = append(long, fmt.Sprintf(" line %03d of a long file", i))
	}
	got, _ := m.Update(changeDiffMsg{
		ticket: "C-1",
		files:  []changeFile{{path: "a/One.kt", status: "mod", adds: 0, dels: 0}},
		diff:   map[string][]string{"a/One.kt": long},
	})
	m = got.(monitorModel)

	m = chPress(t, m, "down", "right") // onto the only file, full screen
	if m.change.mode != chFull {
		t.Fatalf("did not enter full screen, mode=%v", m.change.mode)
	}
	top := stripAnsiStr(m.View())
	if !strings.Contains(top, "line 001") {
		t.Fatalf("full screen must start at the top:\n%s", top)
	}

	m = chPress(t, m, "enter") // scroll mode
	if m.change.mode != chScroll {
		t.Fatalf("enter did not start scroll mode, mode=%v", m.change.mode)
	}
	for i := 0; i < 15; i++ {
		m = chPress(t, m, "down")
	}
	if m.change.lineCur != 15 {
		t.Fatalf("lineCur = %d after 15 downs, want 15", m.change.lineCur)
	}
	if m.change.yOff == 0 {
		t.Fatal("the viewport must have followed the cursor")
	}
	scrolled := stripAnsiStr(m.View())
	if strings.Contains(scrolled, "line 001") {
		t.Errorf("after scrolling down, line 001 should have scrolled out of view:\n%s", scrolled)
	}

	// up must scroll back.
	for i := 0; i < 15; i++ {
		m = chPress(t, m, "up")
	}
	if m.change.lineCur != 0 || m.change.yOff != 0 {
		t.Errorf("lineCur/yOff after scrolling all the way back up = %d/%d, want 0/0", m.change.lineCur, m.change.yOff)
	}
}

// The reported bug: "without pressing enter I cannot scroll using the
// mouse". The wheel must scroll the full-screen diff even in chFull mode,
// before any enter commits to keyboard SCROLL mode - a mouse has no such
// mode to switch into. Mouse reporting turns on entering full screen and
// off leaving it, the same rule the comments screen's diff viewer follows.
func TestChangeMouseWheelScrollsWithoutEnter(t *testing.T) {
	m := changeModel(t)
	m.width, m.height = 120, 20

	long := []string{"@@ -1,80 +1,80 @@"}
	for i := 1; i <= 80; i++ {
		long = append(long, fmt.Sprintf(" line %03d of a long file", i))
	}
	got, _ := m.Update(changeDiffMsg{
		ticket: "C-1",
		files:  []changeFile{{path: "a/One.kt", status: "mod"}},
		diff:   map[string][]string{"a/One.kt": long},
	})
	m = got.(monitorModel)

	m = chPress(t, m, "down")
	got, cmd := m.updateChange(keyOf("right")) // enter full screen
	m = got.(monitorModel)
	if m.change.mode != chFull {
		t.Fatalf("did not enter full screen, mode=%v", m.change.mode)
	}
	if cmd == nil || !strings.Contains(fmt.Sprintf("%T", cmd()), "enableMouseCellMotion") {
		t.Fatalf("entering full screen must enable mouse reporting, cmd=%v", cmd)
	}

	// The wheel scrolls right here, in chFull, with NO enter pressed.
	got, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m = got.(monitorModel)
	if m.change.yOff == 0 {
		t.Fatal("wheel-down in full-screen mode (no enter) did not scroll")
	}
	before := stripAnsiStr(m.View())
	if strings.Contains(before, "line 001") {
		t.Errorf("after wheel-down the top of the file should have scrolled out:\n%s", before)
	}
	got, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	m = got.(monitorModel)
	if m.change.yOff != 0 {
		t.Errorf("yOff = %d after wheel-up back to the top, want 0", m.change.yOff)
	}

	// The wheel keeps working after enter commits to keyboard SCROLL too.
	got, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = got.(monitorModel)
	got, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	m = got.(monitorModel)
	if m.change.lineCur == 0 {
		t.Fatal("wheel-down in scroll mode did not move the line cursor")
	}

	// Leaving: one left exits scroll to full, a second leaves full for split
	// and gives the mouse back.
	got, cmd = m.updateChange(keyOf("left"))
	m = got.(monitorModel)
	if m.change.mode != chFull {
		t.Fatalf("left from scroll must land on full screen first, mode=%v", m.change.mode)
	}
	got, cmd = m.updateChange(keyOf("left"))
	m = got.(monitorModel)
	if m.change.mode != chSplit {
		t.Fatalf("a second left must reach split, mode=%v", m.change.mode)
	}
	if cmd == nil || !strings.Contains(fmt.Sprintf("%T", cmd()), "disableMouse") {
		t.Fatalf("leaving full screen must disable mouse reporting, cmd=%v", cmd)
	}
}

// The ASKME-TEST crash: real Android paths (deep packages, long basenames)
// drove shortPath's width negative and its slice index to [-1:]. Every mode
// of the redesigned screen must render at every plausible terminal width
// without panicking.
func TestChangeRenderNeverPanicsOnRealPaths(t *testing.T) {
	paths := []string{
		"app/src/main/java/com/example/myapplication/domain/usecase/UploadPhotosUseCase.kt",
		"app/src/main/java/com/example/myapplication/upload/UploadViewModel.kt",
		"app/src/test/java/com/example/myapplication/upload/UploadViewModelTest.kt",
		"a.kt", // and a tiny one
	}
	m := changeModel(t)
	var files []changeFile
	diff := map[string][]string{}
	for _, p := range paths {
		files = append(files, changeFile{path: p, status: "mod", adds: 41, dels: 12})
		diff[p] = []string{"@@ -1,3 +1,6 @@", "+added", " context"}
	}
	got, _ := m.Update(changeDiffMsg{ticket: "C-1", files: files, diff: diff})
	m = got.(monitorModel)
	m.change.round, m.change.roundTree = 2, "deadbeef" // exercise the Show-all row too
	m.change.repoFiles = []string{"z/Other.kt"}
	m.change.repoFilesLoaded = true

	for _, w := range []int{40, 60, 80, 96, 100, 120, 160, 200} {
		for _, h := range []int{10, 24, 40} {
			m.width, m.height = w, h
			m.change.mode = chSplit
			for cur := 0; cur <= m.change.ctaAt(); cur++ {
				m.change.cursor = cur
				_ = m.View()
			}
			m.change.approveOpen = true
			_ = m.View()
			m.change.approveOpen = false

			for i := range files {
				m.change.fileIdx = i
				m.change.mode = chFull
				_ = m.View()
				m.change.mode = chScroll
				m.change.lineCur = 1
				m.change.keepLineInView(len(diff[files[i].path]), h)
				_ = m.View()
			}
			// The box, focused, empty and with a long draft, with the picker open.
			m.change.mode = chBox
			m.change.boxOrigin = changeBoxOrigin{mode: chSplit, cursor: 1}
			m.change.draft.SetValue("")
			_ = m.View()
			m.change.draft.SetValue(strings.Repeat("a very long draft comment ", 10))
			_ = m.View()
			m.change.picker = mentionPicker{open: true, query: "one", items: []string{paths[0], "z/Other.kt"}}
			_ = m.View()
			m.change.picker = mentionPicker{}
			// Composing under full-screen (scroll origin).
			m.change.boxOrigin = changeBoxOrigin{mode: chScroll, fileIdx: 0, lineCur: 1}
			_ = m.View()
		}
	}
}

// shortPath across every width, including the exact off-by-one that crashed
// ASKME-TEST (width == len(basename)+2): never panic, never empty.
func TestShortPathNeverPanics(t *testing.T) {
	cases := []string{
		"app/src/main/java/com/example/myapplication/domain/usecase/UploadPhotosUseCase.kt",
		"UploadPhotosUseCase.kt",
		"a/b.kt",
		"x",
		"",
	}
	for _, p := range cases {
		for w := -30; w <= len(p)+5; w++ {
			got := shortPath(p, w)
			if p != "" && got == "" {
				t.Errorf("shortPath(%q, %d) = empty", p, w)
			}
		}
	}
}

// The ASKME-TEST hole: a change already committed AND pushed to its branch
// diffs empty against upstream. The baseline must be the PR base branch, so
// the screen shows everything the PR contains.
func TestChangeDiffOnPushedBranchUsesThePRBase(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", origin).CombinedOutput(); err != nil {
		t.Fatalf("bare init: %v: %s", err, out)
	}
	wt := filepath.Join(root, "wt")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", wt}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	run("remote", "add", "origin", origin)
	run("push", "-q", "-u", "origin", "main")
	// The PR branch: one committed, PUSHED change - upstream == HEAD.
	run("checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("commit", "-q", "-am", "the change")
	run("push", "-q", "-u", "origin", "feature")

	msg := changeDiffCmd(wt, "T", "", 1, false, "main")().(changeDiffMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	if _, ok := msg.diff["a.kt"]; !ok || len(msg.files) != 1 {
		t.Fatalf("pushed-branch diff must show the PR's change vs its base: files=%v diff=%v", msg.files, msg.diff)
	}
}

// changeLineNumbers tracks both sides of a hunk, index-aligned with
// renderHunk's own output: new-file numbers for context/added rows, old-file
// numbers for removed rows, 0 for the header.
func TestChangeLineNumbers(t *testing.T) {
	lines := []string{
		"@@ -10,3 +10,4 @@",
		" context one", // old 10, new 10
		"-removed",     // old 11
		"+added",       // new 11
		" context two", // old 12, new 12
	}
	got := changeLineNumbers(lines)
	want := []int{0, 10, 11, 11, 12}
	if len(got) != len(want) {
		t.Fatalf("changeLineNumbers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %d, want %d (%v)", i, got[i], want[i], got)
		}
	}
}

// From round 2 the header names the round and the diff baseline, and the
// Show-all row toggles it.
func TestChangeRoundTwoHeaderAndToggle(t *testing.T) {
	m := changeModel(t)
	m.change.round, m.change.roundTree = 2, "deadbeef"
	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "round 2") {
		t.Errorf("round-2 header missing:\n%s", out)
	}
	if !strings.Contains(out, "Show all changes") {
		t.Errorf("round-2 action row missing:\n%s", out)
	}
	if a := m.change.showAllAt(); a < 0 {
		t.Fatal("showAllAt must exist at round 2")
	} else {
		m.change.cursor = a
	}
	got, cmd := m.updateChange(keyOf("enter"))
	m = got.(monitorModel)
	if !m.change.showAll || cmd == nil || m.change.diffLoaded {
		t.Fatalf("toggle must flip showAll and re-read the diff (showAll=%v cmd=%v)", m.change.showAll, cmd)
	}
	out = stripAnsiStr(m.View())
	if !strings.Contains(out, "Show changes since your feedback") {
		t.Errorf("toggled label missing:\n%s", out)
	}
}

// "Review and make changes" is the most important row action: it leads the
// menu for gate-parked rows AND for hand-checked-out branches (where it sits
// above "Check for a pull request").
func TestReviewAndMakeChangesLeadsTheMenu(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	parked := agentActions(store.Session{Ticket: "T", State: store.StateChangeReview})
	if len(parked) == 0 || parked[0].label != "Review and make changes" {
		t.Fatalf("change-review menu = %+v, want Review and make changes first", parked)
	}
	checked := agentActions(store.Session{Ticket: "T", State: store.StateCheckedOut})
	if len(checked) < 2 || checked[0].label != "Review and make changes" ||
		checked[1].label != "Check for a pull request" {
		t.Fatalf("checked-out menu = %+v, want Review and make changes leading", checked)
	}
	// And on every idle row with a worktree: needs-you parks and open PRs.
	// (worktreeExists wants a .git inside, like a real checkout has.)
	wt := paths.WorktreeFor("", "T")
	if err := os.MkdirAll(filepath.Join(wt, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{store.StateNeedsYou, store.StateFailed, store.StateReview} {
		items := agentActions(store.Session{Ticket: "T", State: state, Worktree: wt})
		if len(items) == 0 || items[0].label != "Review and make changes" {
			t.Errorf("state %q menu = %+v, want Review and make changes leading", state, items)
		}
	}
}

// The dashboard rows a change-review ticket under NEEDS YOU with the label,
// and Enter opens that row's menu (not the screen directly) - change-review
// is reachable from several idle states where the row still has other live
// reasons to open a menu (Pause, Open Android Studio, Stop & clean up...),
// so Enter must never bypass it. "Review and make changes" leads the menu.
func TestChangeDashboardRow(t *testing.T) {
	m := changeModel(t)
	m.view = viewDashboard
	out := stripAnsiStr(m.View())
	// The section header renders letter-spaced; the label may truncate at
	// narrow column widths, so match its stable prefix.
	if !strings.Contains(out, letterSpace("NEEDS YOU")) || !strings.Contains(out, "review bef") {
		t.Errorf("dashboard missing the gate row:\n%s", out)
	}
	// Walk the cursor onto the ticket row (the fixed command rows lead).
	found := false
	for cur := 0; cur < len(m.rows)+len(dashCommands); cur++ {
		m.cursor = cur
		if s := m.selected(); s != nil && s.Ticket == "C-1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no dashboard row for the parked ticket")
	}
	got, _ := m.Update(keyOf("enter"))
	m = got.(monitorModel)
	if m.view != viewPalette || !m.paletteAgentOnly {
		t.Fatalf("Enter on the row must open its menu, view=%v paletteAgentOnly=%v", m.view, m.paletteAgentOnly)
	}
	items := agentActions(*m.selected())
	if len(items) == 0 || items[0].label != "Review and make changes" {
		t.Fatalf("menu = %+v, want Review and make changes leading", items)
	}
}

// The @ picker's file list is cached per worktree (mention_picker.go's
// repoFileCache) so a screen reopened against a worktree already loaded
// reads through the cache synchronously instead of re-shelling `git
// ls-files` and waiting on another round trip.
func TestChangeRepoFileCache(t *testing.T) {
	m := changeModel(t)
	sess, err := m.store.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := m.Update(changeRepoFilesMsg{ticket: "C-1", worktree: "/wt", files: []string{"a/One.kt", "Z/Cached.KT"}})
	m = got.(monitorModel)
	rfl, ok := m.repoFileCache["/wt"]
	if !ok || len(rfl.files) != 2 || rfl.lower[1] != "z/cached.kt" {
		t.Fatalf("repoFileCache[/wt] = %+v, want the lowercased listing", rfl)
	}

	got2, _ := m.openChangeView(*sess)
	m = got2.(monitorModel)
	if !m.change.repoFilesLoaded {
		t.Fatal("a cached worktree listing must be applied synchronously on open, not scheduled as another command")
	}
	if len(m.change.repoFiles) != 2 || m.change.repoFilesLower[1] != "z/cached.kt" {
		t.Fatalf("change screen didn't read through the cache: files=%v lower=%v", m.change.repoFiles, m.change.repoFilesLower)
	}
}

// The Approve CTA stays visible and reachable whether or not a PR already
// exists - a change-review round can happen AFTER a PR was already opened
// (a further round of feedback), and that round still needs a way to ship.
// Only the label changes: "create" only makes sense before one exists;
// finishShip already no-ops PR creation and just pushes when one does.
func TestChangeCTALabelReflectsWhetherAPRExists(t *testing.T) {
	m := changeModel(t)
	if at := m.change.ctaAt(); at < 0 {
		t.Fatalf("ctaAt() = %d, want a real row - the CTA must stay reachable", at)
	}
	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "Approve and create pull request") {
		t.Fatalf("with no PR yet, the CTA must offer to create one:\n%s", out)
	}

	// A PR exists AND there's something new since it was opened (an edit made
	// from this screen, say) - the CTA stays reachable, relabeled to "push".
	m.change.prURL = "https://github.com/o/r/pull/1"
	m.change.hasUnshipped = true
	out2 := stripAnsiStr(m.View())
	if strings.Contains(out2, "Approve and create pull request") {
		t.Fatalf("once a PR exists, the CTA must not offer to create another:\n%s", out2)
	}
	if !strings.Contains(out2, "Approve and push updates to the PR") {
		t.Fatalf("once a PR exists, the CTA must offer to push instead:\n%s", out2)
	}

	m.change.cursor = m.change.ctaAt()
	got, _ := m.updateChange(keyOf("enter"))
	m2 := got.(monitorModel)
	if !m2.change.approveOpen {
		t.Fatal("enter on the CTA must still open the inline confirm once a PR exists")
	}
}

// Once the worktree exactly matches an already-open PR - nothing uncommitted,
// nothing unpushed - the Approve CTA disappears entirely and says so plainly,
// instead of offering a button that would be a no-op. "Down" past the last
// row still reaches the feedback box.
func TestChangeCTAHidesWhenUpToDateWithAnExistingPR(t *testing.T) {
	m := changeModel(t)
	m.change.prURL = "https://github.com/o/r/pull/1"
	m.change.hasUnshipped = false

	if at := m.change.ctaAt(); at != -1 {
		t.Fatalf("ctaAt() = %d when up to date, want -1", at)
	}
	out := stripAnsiStr(m.View())
	if strings.Contains(out, "Approve and push updates to the PR") || strings.Contains(out, "Approve and create pull request") {
		t.Fatalf("an up-to-date change must not show an approve button:\n%s", out)
	}
	if !strings.Contains(out, "Up to date with the open PR") {
		t.Fatalf("an up-to-date change must say so plainly:\n%s", out)
	}

	m.change.cursor = m.change.lastRowAt()
	got, _ := m.updateChange(keyOf("down"))
	m2 := got.(monitorModel)
	if m2.change.mode != chBox {
		t.Fatalf("down past the last row must still open the feedback box, mode=%d", m2.change.mode)
	}
}

// changeHasUnshippedContent tells a clean, fully pushed branch apart from one
// with an uncommitted edit or a local commit the remote doesn't have yet -
// the Approve CTA's whole point once a PR already exists.
func TestChangeHasUnshippedContentDetectsCleanVsDirty(t *testing.T) {
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	wt := filepath.Join(root, "wt")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run(root, "init", "-q", "--bare", origin)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	run(wt, "init", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(wt, "add", ".")
	run(wt, "commit", "-q", "-m", "base")
	run(wt, "remote", "add", "origin", origin)
	run(wt, "push", "-q", "-u", "origin", "feature")

	if changeHasUnshippedContent(wt) {
		t.Fatal("a clean, fully pushed branch has nothing unshipped")
	}

	if err := os.WriteFile(filepath.Join(wt, "a.kt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !changeHasUnshippedContent(wt) {
		t.Fatal("an uncommitted edit must count as unshipped")
	}

	run(wt, "commit", "-q", "-am", "local only")
	if !changeHasUnshippedContent(wt) {
		t.Fatal("a local commit not yet pushed must count as unshipped")
	}
}

// The "PR description" row shows the actual text this change will ship with
// (or already has), not the ticket's one-line title - changePRBody mirrors
// runner/ship.go's own body selection so the preview never lies about what
// will be posted.
func TestChangeSummaryShowsIntendedPRBody(t *testing.T) {
	m := changeModel(t)
	if !strings.Contains(m.change.prBody, "## Changes") {
		t.Fatalf("prBody = %q, want the default PR template's structure", m.change.prBody)
	}
	m.change.cursor = 0 // the "PR description" row
	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "not created yet") {
		t.Fatalf("with no PR yet, the pane must say so:\n%s", out)
	}

	m.change.prURL = "https://github.com/o/r/pull/1"
	out2 := stripAnsiStr(m.View())
	if strings.Contains(out2, "not created yet") {
		t.Fatalf("once a PR exists, the pane must not claim it's still pending:\n%s", out2)
	}
}

// changePRBody prefers the agent's own report.PRBody verbatim when no repo
// template exists to repair it against.
func TestChangePRBodyPrefersTheAgentsReport(t *testing.T) {
	report := &agent.Report{PRBody: "# custom body from the agent"}
	got := changePRBody(t.TempDir(), report, "T-1", "add x", "b", "main")
	if got != "# custom body from the agent" {
		t.Fatalf("changePRBody = %q, want the report's own PRBody verbatim", got)
	}
}

// With no report.PRBody at all, changePRBody falls back to Apple Pie's own
// default template, the same one finishShip would render.
func TestChangePRBodyFallsBackToTheDefaultTemplate(t *testing.T) {
	got := changePRBody(t.TempDir(), nil, "T-1", "add x", "b", "main")
	if !strings.Contains(got, "[T-1] add x") || !strings.Contains(got, "## Changes") {
		t.Fatalf("changePRBody = %q, want the default template rendered with the ticket's fields", got)
	}
}

// The split view's selected row must render bold, the dashboard/comments
// list convention (comments_row.go's rowLabelStyle: "the selected row's text
// is bold and bright, and nothing else on the screen is") - not the older,
// unbolded selStyle this screen used before it was brought in line with the
// rest of the hub.
func TestChangeSelectedRowMatchesTheDashboardsBoldRowConvention(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	m := changeModel(t)
	m = chPress(t, m, "down") // cursor onto the first file row
	lines := m.changeListLines(38)
	var selected string
	for _, ln := range lines {
		if strings.Contains(ln, "One.kt") {
			selected = ln
		}
	}
	if selected == "" {
		t.Fatalf("no rendered row for the selected file: %v", lines)
	}
	if !strings.Contains(selected, "\x1b[1") {
		t.Fatalf("the selected row must render bold (SGR 1), got %q", selected)
	}
}

// The transcript renders above the box once there's a conversation, and a
// running round says so - both read from the session's live state, which is
// what syncChangeFromSession refreshes every tick.
func TestChangeTranscriptRendersAboveTheBox(t *testing.T) {
	m := changeModel(t)
	m.change.transcript = []string{"you: what does this do?", "claude: it renders the home screen."}
	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "what does this do?") || !strings.Contains(out, "it renders the home screen.") {
		t.Fatalf("transcript not rendered:\n%s", out)
	}

	m.change.discussing = true
	out = stripAnsiStr(m.View())
	if !strings.Contains(out, "thinking") {
		t.Fatalf("a running round must say so:\n%s", out)
	}
}

// syncChangeFromSession is the tick-driven refresh a live --discuss round
// depends on: the transcript, the last error, and whether a round is
// currently running all come from the store, not from anything the discuss
// process could reach into the TUI process to update directly.
func TestSyncChangeFromSessionRefreshesLiveState(t *testing.T) {
	m := changeModel(t)
	if err := m.store.AppendChangeTranscript("C-1", "you: hello"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.AppendChangeTranscript("C-1", "claude: hi there"); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetState("C-1", store.StateWorking, 0); err != nil {
		t.Fatal(err)
	}
	if err := m.store.SetPID("C-1", os.Getpid()); err != nil {
		t.Fatal(err)
	}

	// sessionByTicket reads m.flat, the cache reload() fills - matching the
	// real tick flow (update.go: reload() then syncChangeFromSession()).
	m.reload()
	m.syncChangeFromSession()

	want := []string{"you: hello", "claude: hi there"}
	if !reflect.DeepEqual(m.change.transcript, want) {
		t.Fatalf("transcript = %v, want %v", m.change.transcript, want)
	}
	if !m.change.discussing {
		t.Error("a StateWorking row with a live pid must read as discussing")
	}

	if err := m.store.SetState("C-1", store.StateChangeReview, 0); err != nil {
		t.Fatal(err)
	}
	m.reload()
	m.syncChangeFromSession()
	if m.change.discussing {
		t.Error("discussing must clear once the round returns to change-review")
	}
}

// cancelDiscuss is Esc's mid-turn interrupt: it must park the ticket back at
// change-review (not needs-you/stopped - those are the general pause/cancel
// paths, this one is a normal, expected outcome) and report itself via
// discussCancelledMsg, the same shape cancelAgent/pauseAgent already use for
// their own done-messages (modals.go). No live pid here, mirroring
// TestCancelAgentMarksStopped's own reasoning: stopRun must not reach a real
// process from a test.
func TestCancelDiscussParksBackAtChangeReview(t *testing.T) {
	m := changeModel(t)
	if err := m.store.SetState("C-1", store.StateWorking, 0); err != nil {
		t.Fatal(err)
	}
	sess, err := m.store.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}

	msg := m.cancelDiscuss(*sess)()
	done, ok := msg.(discussCancelledMsg)
	if !ok || done.ticket != "C-1" {
		t.Fatalf("unexpected msg: %#v", msg)
	}
	got, err := m.store.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.StateChangeReview {
		t.Errorf("state = %q, want %q", got.State, store.StateChangeReview)
	}
}

// Esc on a live discuss round cancels it instead of navigating away - the box
// stays open (no jump back to the list mid-cancel) until the cancel actually
// completes, matching Claude Code's own mid-turn Esc. Auto mode has no
// equivalent to test: sendReworkMessage already leaves for the dashboard
// before anything could be "working" here.
func TestChangeBoxEscCancelsALiveDiscussRoundInsteadOfNavigatingBack(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty
	m.change.discussing = true

	got, cmd := m.updateChange(tea.KeyMsg{Type: tea.KeyEsc})
	m = got.(monitorModel)
	if m.change.mode != chBox {
		t.Fatalf("Esc on a live round must not navigate away yet, mode=%v", m.change.mode)
	}
	if cmd == nil {
		t.Fatal("Esc on a live round must return the cancel command")
	}

	msg := cmd()
	if done, ok := msg.(discussCancelledMsg); !ok || done.ticket != "C-1" {
		t.Fatalf("unexpected msg: %#v", msg)
	}
	got2, _ := m.Update(msg)
	m = got2.(monitorModel)
	if m.change.discussing {
		t.Error("discussing must clear once the cancel message is processed")
	}
	sess, err := m.store.Get("C-1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.State != store.StateChangeReview {
		t.Errorf("state = %q, want %q", sess.State, store.StateChangeReview)
	}
}

// The same cancel-on-Esc applies once the round has been left running in the
// background (↑ back out to the split list while it streams) - "esc" cancels
// there too, but "left" (an arrow key) must keep navigating regardless, per
// this screen's own "arrows always work" rule.
func TestChangeSplitEscCancelsALiveDiscussRoundButLeftStillNavigates(t *testing.T) {
	m := changeModel(t)
	m.change.discussing = true

	got, cmd := m.updateChange(tea.KeyMsg{Type: tea.KeyEsc})
	m = got.(monitorModel)
	if m.view != viewChange {
		t.Fatalf("esc on a live round must not leave the screen yet, view=%v", m.view)
	}
	if cmd == nil {
		t.Fatal("esc on a live round must return the cancel command")
	}
	msg := cmd()
	if done, ok := msg.(discussCancelledMsg); !ok || done.ticket != "C-1" {
		t.Fatalf("unexpected msg: %#v", msg)
	}

	m.view = viewChange // reset - "left" must still work while discussing
	got, _ = m.updateChange(tea.KeyMsg{Type: tea.KeyLeft})
	m = got.(monitorModel)
	if m.view != viewDashboard {
		t.Fatalf("left must keep navigating even during a live round, view=%v", m.view)
	}
}

// The box shows the "❯ " prompt Claude Code's own CLI uses, once, on the
// input's first line.
func TestChangeBoxShowsThePromptGlyph(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty
	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "❯") {
		t.Fatalf("the box must show the ❯ prompt:\n%s", out)
	}
}

// Shift+Tab toggles Plan/Auto from inside the box - the same binding Claude
// Code's own CLI uses to cycle its permission mode - and the box's status
// line matches that CLI's own wording and glyphs for each mode.
func TestChangeBoxShiftTabTogglesModeAndShowsClaudeCodeStatusLine(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty, Plan mode (the default)

	out := stripAnsiStr(m.View())
	if !strings.Contains(out, "⏸ plan mode on (shift+tab to cycle)") {
		t.Fatalf("Plan mode's status line must match Claude Code's own wording:\n%s", out)
	}

	got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = got.(monitorModel)
	if m.change.planMode {
		t.Fatal("shift+tab must flip to Auto mode")
	}
	out = stripAnsiStr(m.View())
	if !strings.Contains(out, "⏵⏵ auto mode on (shift+tab to cycle)") {
		t.Fatalf("Auto mode's status line must match Claude Code's own wording:\n%s", out)
	}

	got, _ = m.updateChange(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = got.(monitorModel)
	if !m.change.planMode {
		t.Fatal("shift+tab again must flip back to Plan mode")
	}
}

// The box names the model each stage Enter reaches, in the same green
// (stAccent) the rest of the app reserves for "the agent will act on this" -
// no config.toml exists in this test's PIE_HOME, so config.Load fails and
// every stage must fall back to "default" rather than showing a blank or an
// error. Auto mode is a genuinely different label, not a copy of Plan mode's
// - it names two stages (impl, verify), since those can land on two
// different models and a single "model:" line would hide that.
func TestChangeBoxShowsTheModelItWillRunOn(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty, Plan mode (the default)

	raw := m.View()
	wantDiscuss := stAccent.Render("Planning model: default")
	if !strings.Contains(raw, wantDiscuss) {
		t.Fatalf("Plan mode must show the discuss model line in stAccent, got:\n%s", raw)
	}

	got, _ := m.updateChange(tea.KeyMsg{Type: tea.KeyShiftTab})
	m = got.(monitorModel)
	raw = m.View()
	wantAuto := stAccent.Render("Implementation model: default   Verify model: default")
	if !strings.Contains(raw, wantAuto) {
		t.Fatalf("Auto mode must show both the impl and verify model in stAccent, got:\n%s", raw)
	}
}

// A configured model_verify genuinely differs from model_impl (this is the
// exact setup that prompted this display: haiku implementing, a distinct
// model verifying) - the box must surface both, not silently collapse Auto
// mode down to one, since that is precisely the "half the story" a bare
// "model:" line used to tell.
func TestChangeBoxAutoModeNamesDistinctImplAndVerifyModels(t *testing.T) {
	m := changeModel(t)
	if err := os.WriteFile(paths.Config(), []byte("model_impl = \"haiku\"\nmodel_verify = \"sonnet\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, Plan mode (the default)
	m.change.planMode = false
	m.change.boxModelDiscuss, m.change.boxModelImpl, m.change.boxModelVerify = effectiveChangeBoxModels()

	raw := m.View()
	want := stAccent.Render("Implementation model: haiku   Verify model: sonnet")
	if !strings.Contains(raw, want) {
		t.Fatalf("Auto mode must name both the differing impl and verify models, got:\n%s", raw)
	}
}

// Plan mode's discuss model must come from model_plan, not model_impl - the
// exact confusion this display exists to resolve: a user with Fable set for
// "the planning stage" and Haiku for implementation was seeing "Haiku" under
// Plan mode, which was reading the wrong config field (change_discuss.go
// actually runs the discuss round on ModelPlan; this just asserts the box's
// own display agrees with what the runner does).
func TestChangeBoxDiscussModelComesFromModelPlanNotModelImpl(t *testing.T) {
	m := changeModel(t)
	if err := os.WriteFile(paths.Config(), []byte("model_plan = \"fable\"\nmodel_impl = \"haiku\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, Plan mode (the default)

	raw := m.View()
	want := stAccent.Render("Planning model: fable")
	if !strings.Contains(raw, want) {
		t.Fatalf("Plan mode must show model_plan's value, not model_impl's, got:\n%s", raw)
	}
}

// Once the box owns the keyboard, the list row it was opened from must stop
// rendering as selected - the origin cursor doesn't move, so without gating
// each row's "on" state on the list actually being focused, that row kept
// showing its ▸/bold selection at the same time the box did, a "two things
// selected at once" look reported after landing on the CTA and pressing ↓.
func TestChangeListRowNotSelectedWhileTheBoxIsFocused(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()

	before := m.changeListLines(38)
	var ctaBefore string
	for _, ln := range before {
		if strings.Contains(ln, "Approve and create pull request") {
			ctaBefore = ln
		}
	}
	if !strings.Contains(ctaBefore, "▸") {
		t.Fatalf("sanity check: the CTA must show selected before opening the box: %q", ctaBefore)
	}

	// Down past the last row opens the box empty (updateChangeSplit).
	got, _ := m.updateChange(keyOf("down"))
	m = got.(monitorModel)
	if m.change.mode != chBox {
		t.Fatalf("down past the last row must open the box, mode=%v", m.change.mode)
	}

	after := m.changeListLines(38)
	var ctaAfter string
	for _, ln := range after {
		if strings.Contains(ln, "Approve and create pull request") {
			ctaAfter = ln
		}
	}
	if strings.Contains(ctaAfter, "▸") {
		t.Fatalf("the CTA row must not still render as selected once the box owns the keyboard: %q", ctaAfter)
	}
}

// renderChangeBox strips the textarea's own ANSI (its rendered lines are
// re-wrapped in the border's color) before it ever reaches display, so
// setting styles directly on the textarea.Model (FocusedStyle.Placeholder,
// say) has zero visible effect - it's stripped a moment later. The
// placeholder must be re-styled dim in renderChangeBox itself, or it renders
// in the same plain color as real typed text and reads as something the
// user already wrote.
func TestChangeBoxPlaceholderIsDimmerThanTypedText(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty - placeholder showing

	placeholderRaw := m.View()
	wantDim := stMeta.Render("Comment")
	if !strings.Contains(placeholderRaw, wantDim) {
		t.Fatalf("the placeholder must render in stMeta (dim), got:\n%s", placeholderRaw)
	}

	m.change.draft.SetValue("fix the flaky test")
	typedRaw := m.View()
	if strings.Contains(typedRaw, wantDim) {
		t.Fatalf("typed text must not carry the placeholder's dim styling:\n%s", typedRaw)
	}
}

// bubbles/textarea has no text-selection concept (confirmed against its
// source: no selection state, no highlighted-span rendering, no Select-type
// method exists anywhere in the widget), so there is nothing in this package
// to add for drag/shift-select. What it DOES ship, as its DefaultKeyMap, is
// the same readline/Emacs word- and line-delete set Claude Code's own CLI
// input uses for fast deletion without selection: ctrl+w deletes the word
// behind the cursor. newChangeBox never overrides KeyMap, and
// updateChangeBox only special-cases Esc/Enter/Shift+Tab/Up/"@" before
// falling through to the textarea's own Update - so ctrl+w (and its
// siblings: alt+backspace, alt+delete/alt+d word-forward, ctrl+u/ctrl+k
// clear-to-line-start/end) already reaches the widget unmodified. This test
// is the guard against a future change to that switch accidentally
// intercepting one of them.
func TestChangeBoxWordDeleteShortcutReachesTheTextarea(t *testing.T) {
	m := changeModel(t)
	m.change.cursor = m.change.ctaAt()
	m = chPress(t, m, "down") // open the box, empty

	m.change.draft.SetValue("fix the flaky test")
	m.change.draft.CursorEnd()

	got, _ := m.updateChangeBox(tea.KeyMsg{Type: tea.KeyCtrlW})
	m = got.(monitorModel)
	if got := m.change.draft.Value(); got != "fix the flaky " {
		t.Fatalf("ctrl+w must delete the last word via the textarea's own default keymap, got %q", got)
	}
}
