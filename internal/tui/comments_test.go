package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func cmt(id, author string, mut ...func(*store.Comment)) store.Comment {
	c := store.Comment{
		ID: id, Author: author, Kind: review.KindThread,
		Path: "app/src/main/java/com/acme/LoginScreen.kt", Line: 31,
		Body: "this should collect the flow with repeatOnLifecycle",
		URL:  "https://github.com/o/r/pull/482#discussion_r1",
	}
	for _, f := range mut {
		f(&c)
	}
	return c
}

// commentsModel is a screen with three unresolved comments, all selected.
func commentsModel() monitorModel {
	m := baseModel()
	m.view = viewComments
	m.comments.reset("A-1", "PR #482", "")
	m.comments.rows = []store.Comment{
		cmt("T1", "bob", func(c *store.Comment) { c.Line = 12; c.Body = "nit: rename loading to isLoading" }),
		cmt("T2", "bob"),
		cmt("T3", "maria", func(c *store.Comment) {
			c.Path, c.Line = "app/src/main/java/com/acme/AuthRepo.kt", 88
			c.Body = "why not just use a sealed interface here?"
		}),
	}
	for _, c := range m.comments.rows {
		m.comments.sel[c.ID] = true
	}
	m.renderFocused()
	return m
}

func keyOf(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(m monitorModel, keys ...string) monitorModel {
	for _, k := range keys {
		got, _ := m.updateComments(keyOf(k))
		m = got.(monitorModel)
	}
	return m
}

// The default is "fix everything": the work is deselecting exceptions, not
// assembling a batch, because "yes, all of these" is the usual answer.
func TestEveryCommentStartsSelected(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.Claim("A-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("A-1", store.StateReview, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("A-1", "https://github.com/o/r/pull/482",
		[]review.Thread{
			{ID: "T1", Kind: review.KindThread, Author: "bob", Path: "a.kt", Line: 1, Body: "x", LastCommentID: "c1"},
			{ID: "T2", Kind: review.KindThread, Author: "maria", Path: "b.kt", Line: 2, Body: "y", LastCommentID: "c2"},
		}, true); err != nil {
		t.Fatal(err)
	}
	m := baseModel()
	m.store = st
	got, _ := m.openCommentsView(store.Session{
		Ticket: "A-1", Repo: "/repo", State: store.StateReview,
		PRURL: "https://github.com/o/r/pull/482",
	})
	mm := got.(monitorModel)
	if mm.view != viewComments {
		t.Fatalf("view = %v, want the comments screen", mm.view)
	}
	if n := len(mm.selectedIDs()); n != 2 {
		t.Fatalf("%d selected, want every comment selected on open", n)
	}
	if mm.comments.prLabel != "PR #482" {
		t.Errorf("prLabel = %q, want it parsed from the PR url", mm.comments.prLabel)
	}
}

// The cursor walks the comments and then the run bar, which is the last row of
// the list rather than a key of its own.
func TestCursorWalksCommentsThenTheRunBar(t *testing.T) {
	m := commentsModel()
	if m.onRunBar() {
		t.Fatal("the cursor should start on the first comment")
	}
	m = press(m, "down", "down")
	if m.onRunBar() || m.comments.cursor != 2 {
		t.Fatalf("cursor = %d, want the last comment", m.comments.cursor)
	}
	m = press(m, "down")
	if !m.onRunBar() {
		t.Fatal("one past the last comment should be the run bar")
	}
	if m.focused() != nil {
		t.Error("no comment is focused while the cursor is on the run bar")
	}
	if m = press(m, "down"); !m.onRunBar() {
		t.Fatal("the run bar is the last row; down must clamp there")
	}
	if m = press(m, "up"); m.onRunBar() {
		t.Fatal("up from the run bar returns to the comments")
	}
}

// The box is the state and enter is the verb: [x] means the agent fixes it,
// [ ] means it walks past. One keystroke per decision - skipping four comments
// is ↓ enter ↓ enter ↓ enter ↓ enter.
func TestEnterOpensTheMenuAndTheMenuToggles(t *testing.T) {
	m := commentsModel()
	if !m.comments.sel["T1"] {
		t.Fatal("setup: everything starts checked")
	}
	// enter, down, enter: Reply & resolve leads the menu (issue #16), the
	// toggle is right under it - the common case is still no aiming.
	m = press(m, "enter")
	if m.comments.mode != modeMenu {
		t.Fatal("enter on a comment row should open its menu")
	}
	if !m.comments.sel["T1"] {
		t.Error("opening a menu must not change the checkbox")
	}
	m = press(m, "down", "enter")
	if m.comments.sel["T1"] {
		t.Fatal("the menu's toggle item should skip the comment")
	}
	if m.comments.mode != modeBrowsing {
		t.Fatal("picking an item should close the menu")
	}
	if m = press(m, "enter", "down", "enter"); !m.comments.sel["T1"] {
		t.Fatal("the item flips back to Fix once the comment is skipped")
	}
}

// esc leaves the screen directly - there is no menu layer to back out of.
func TestEscLeavesTheScreen(t *testing.T) {
	if m := press(commentsModel(), "esc"); m.view != viewDashboard {
		t.Fatal("esc should return to the dashboard")
	}
}

// The run bar's label carries the live count and the consequence. That binding
// is what makes the screen safe without a confirmation dialog, so it is pinned.
func TestRunBarLabelTracksTheSelection(t *testing.T) {
	m := commentsModel()
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "FIX 3 COMMENTS AND PREVIEW THE FIXES") {
		t.Errorf("run bar should name the count and the consequence\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "3 checked · 0 skipped") {
		t.Errorf("run bar should carry the consequence meta\n--- got ---\n%s", out)
	}
	m = press(m, "enter", "down", "enter") // open the menu, take the toggle (second row)
	out = ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "FIX 2 COMMENTS AND PREVIEW THE FIXES") {
		t.Errorf("run bar should follow the selection down\n--- got ---\n%s", out)
	}
	// The aggregate row carries the same counts, and must not drift from the bar.
	if !strings.Contains(out, "2 checked · 1 skipped") {
		t.Errorf("the all-comments row should agree with the run bar\n--- got ---\n%s", out)
	}
}

// With nothing selected the bar goes inert and cannot start a run.
func TestRunBarIsInertAtZero(t *testing.T) {
	m := commentsModel()
	for _, c := range m.comments.rows {
		m.comments.sel[c.ID] = false
	}
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "NOTHING TO FIX") {
		t.Errorf("an empty selection should read as inert\n--- got ---\n%s", out)
	}
	m.comments.cursor = len(m.visible())
	got, cmd := m.updateComments(keyOf("enter"))
	if cmd != nil {
		t.Fatal("an empty selection must not start a run")
	}
	if got.(monitorModel).view != viewComments {
		t.Error("a refused run should leave the screen open")
	}
}

// The list states the whole review without any row being opened.
func TestListShowsSelectionStateWithoutOpeningRows(t *testing.T) {
	m := commentsModel()
	m.comments.sel["T3"] = false
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	for _, want := range []string{
		"LoginScreen.kt:12", // the row carries its own file and line - no group heading
		"AuthRepo.kt:88",
		"[ ]", // deselected, still listed
		"[x]",
		"nit",            // the severity marker, lifted into its own column
		"rename loading", // ...and out of the body
		"maria",
		"bob",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("list is missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// A saved note is flagged on the row, so it is visible from the list.
func TestNoteIsFlaggedOnTheRow(t *testing.T) {
	m := commentsModel()
	m.comments.rows[0].UserNote = "use flowWithLifecycle"
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "✎") {
		t.Errorf("a comment with a note should be marked in the list\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "use flowWithLifecycle") {
		t.Error("the note should render under the comment too")
	}
}

// The detail pane shows the code and text for the row the cursor is on.
func TestDetailFollowsTheCursor(t *testing.T) {
	m := commentsModel()
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "app/src/main/java/com/acme/LoginScreen.kt") {
		t.Errorf("the detail pane should name the full path\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "rename loading to isLoading") {
		t.Error("the detail pane should show the focused comment's text")
	}
	// The marker belongs to the severity column and the quote header, not the prose.
	if strings.Contains(out, "nit: rename") {
		t.Errorf("the pane still repeats the severity marker inside the body\n--- got ---\n%s", out)
	}
	m = press(m, "down", "down")
	out = ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "sealed interface") {
		t.Errorf("the detail pane should follow the cursor\n--- got ---\n%s", out)
	}
}

// Only arrows, enter and esc. A letter reaching an action would be a shortcut,
// which is the thing this screen is specified not to have.
func TestNoLetterShortcuts(t *testing.T) {
	base := commentsModel()
	for _, k := range []string{"f", "s", "t", "o", "b", "d", "a", "n", "p", "L", "+", "-", " "} {
		m := press(base, k)
		if len(m.selectedIDs()) != len(base.selectedIDs()) {
			t.Errorf("%q changed the selection - it should do nothing", k)
		}
		if m.comments.mode != modeBrowsing {
			t.Errorf("%q opened an input or a menu - it should do nothing", k)
		}
		if m.comments.cursor != base.comments.cursor {
			t.Errorf("%q moved the cursor - it should do nothing", k)
		}
	}
	if m := press(base, "esc"); m.view != viewDashboard {
		t.Error("esc should leave the screen")
	}
}

func TestManualRefreshActuallyFetches(t *testing.T) {
	m := commentsModel()
	m.store = openTestStore(t)
	s := store.Session{
		Ticket: "A-1", State: store.StateReview,
		Repo: "/repo", PRURL: "https://github.com/o/r/pull/482",
	}
	m.flat = []store.Session{s}
	m.comments.lastFetch = time.Now()
	if cmd := m.fetchComments(s, true); cmd == nil {
		t.Fatal("a forced fetch must not be suppressed by the freshness check")
	}
	if cmd := m.fetchComments(s, false); cmd != nil {
		t.Fatal("an unforced fetch right after one should be suppressed")
	}
}

// The badge is derived, not a lifecycle state - so grouping, glyph and label
// all read the count. This is what moves a reviewed PR out of READY FOR REVIEW
// without touching `state`, keeping the daemon's merge detection intact.
func TestReviewWithCommentsIsNeedsYou(t *testing.T) {
	withComments := store.Session{Ticket: "A-1", State: store.StateReview, OpenComments: 3}
	clean := store.Session{Ticket: "A-2", State: store.StateReview}

	groups := newGroups()
	label := func(s store.Session) string {
		for _, g := range groups {
			if g.match(s) {
				return g.label // first match wins, as reload() does
			}
		}
		return "(none)"
	}
	if got := label(withComments); got != "NEEDS YOU" {
		t.Errorf("a PR with open comments grouped as %q, want NEEDS YOU", got)
	}
	if got := label(clean); got != "PR READY FOR REVIEW" {
		t.Errorf("a PR with no comments grouped as %q, want READY FOR REVIEW", got)
	}
	if got := phaseStyle(withComments).GetForeground(); got != amberC {
		t.Errorf("phase colour = %v, want amber - an unanswered review is not green", got)
	}
	if got := stateLabel(withComments); got != "3 comments" {
		t.Errorf("stateLabel = %q, want %q", got, "3 comments")
	}
	if why := whyLines(withComments); len(why) == 0 || !strings.Contains(why[0], "3 review comments") {
		t.Errorf("whyLines = %v, want it to name the comment count", why)
	}
}

func TestPRLabel(t *testing.T) {
	tests := []struct{ url, want string }{
		{"https://github.com/acme/app/pull/482", "PR #482"},
		{"https://github.example.com/acme/app/pull/7", "PR #7"},
		{"", "PR"},
		{"https://github.com/acme/app/pull/not-a-number", "PR"},
	}
	for _, tt := range tests {
		if got := prLabel(tt.url); got != tt.want {
			t.Errorf("prLabel(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestPlural(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{{1, "1 comment"}, {3, "3 comments"}, {0, "0 comments"}} {
		if got := plural(tt.n, "comment"); got != tt.want {
			t.Errorf("plural(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// runningModel is the screen with a batch in flight.
func runningModel(t *testing.T) monitorModel {
	t.Helper()
	m := commentsModel()
	m.flat = []store.Session{{
		Ticket: "A-1", State: store.StateWorking, Repo: "/repo",
		PRURL: "https://github.com/o/r/pull/482",
	}}
	m.comments.ran = map[string]bool{"T1": true, "T2": true}
	m.comments.startedAt = time.Now()
	return m
}

// While the agent works the screen is frozen: the batch is already with it, so
// there is nothing left on this screen to decide.
func TestRunningPhaseIsReadOnly(t *testing.T) {
	m := runningModel(t)
	if m.phase() != phaseRunning {
		t.Fatalf("phase = %d, want running while the session is active", m.phase())
	}
	before := len(m.selectedIDs())
	after := press(m, "enter", "down", "up", "enter")
	if after.comments.mode != modeBrowsing {
		t.Error("enter must not open an input while the run is in flight")
	}
	if len(after.selectedIDs()) != before {
		t.Error("the selection must be frozen while the run is in flight")
	}
	if after.comments.cursor != m.comments.cursor {
		t.Error("the cursor must not move while the run is in flight")
	}
	// esc still leaves, and the run carries on without the screen.
	if got := press(m, "esc"); got.view != viewDashboard {
		t.Error("esc should return to the dashboard")
	}
	if m.onRunBar() {
		t.Error("there is no run bar while a run is in flight")
	}
}

func TestRunningPhaseShowsTheAgentLog(t *testing.T) {
	m := runningModel(t)
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	for _, want := range []string{"fixing 2 comments", "in this run", "not included"} {
		if !strings.Contains(out, want) {
			t.Errorf("running screen is missing %q\n--- got ---\n%s", want, out)
		}
	}
	// With no log file yet it must say so rather than render an empty band.
	if !strings.Contains(out, "waiting for the agent") {
		t.Errorf("want a placeholder before the log exists\n--- got ---\n%s", out)
	}
}

// Once the run finishes, the comments it fixed are no longer "open" - so
// without remembering the batch, the result would vanish the instant it worked.
func TestDonePhaseKeepsFixedCommentsOnScreen(t *testing.T) {
	m := commentsModel()
	m.flat = []store.Session{{
		Ticket: "A-1", State: store.StateReview, Repo: "/repo",
		PRURL: "https://github.com/o/r/pull/482",
	}}
	m.comments.ran = map[string]bool{"T1": true, "T2": true}
	m.comments.rows[0].AddressedAt = time.Now()
	m.comments.rows[0].AddressedSHA = "a1b2c3d4e5"
	m.comments.rows[1].AgentNote = "the suggested API does not exist"

	if m.phase() != phaseDone {
		t.Fatalf("phase = %d, want done", m.phase())
	}
	if len(m.visible()) != 3 {
		t.Fatalf("%d rows visible, want the fixed comment kept on screen", len(m.visible()))
	}
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	for _, want := range []string{
		"done",
		"1 fixed · 1 not fixed",
		"ok  fixed · a1b2c3d4",
		"!!  could not fix",
		"--  not included", // T3 was never selected
		"BACK TO THE DASHBOARD",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("done screen is missing %q\n--- got ---\n%s", want, out)
		}
	}
}

// The agent's reason for declining has to reach the user, or "could not fix"
// is an unactionable dead end.
func TestDonePhaseShowsWhyAFixWasDeclined(t *testing.T) {
	m := commentsModel()
	m.flat = []store.Session{{Ticket: "A-1", State: store.StateReview, Repo: "/repo", PRURL: "https://x/pull/1"}}
	m.comments.ran = map[string]bool{"T1": true}
	m.comments.rows[0].AgentNote = "the suggested API does not exist"
	m.renderFocused()
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "the suggested API does not exist") {
		t.Errorf("the agent's reason must reach the user\n--- got ---\n%s", out)
	}
}

// Enter on the run bar means "run" while idle and "leave" once done - never a
// second run of a batch that already happened.
func TestDoneRunBarLeavesRatherThanRerunning(t *testing.T) {
	m := commentsModel()
	m.flat = []store.Session{{Ticket: "A-1", State: store.StateReview, Repo: "/repo", PRURL: "https://x/pull/1"}}
	m.comments.ran = map[string]bool{"T1": true}
	m.comments.cursor = len(m.visible())
	got, cmd := m.updateComments(keyOf("enter"))
	if cmd != nil {
		t.Fatal("the done run bar must not launch another run")
	}
	if got.(monitorModel).view != viewDashboard {
		t.Error("the done run bar should return to the dashboard")
	}
}

// After the run there is nothing left to select or annotate, so a comment row
// is only a thing to read - offering the menu would promise an edit that no
// longer has anywhere to go.
func TestDonePhaseHasNoRowMenu(t *testing.T) {
	m := commentsModel()
	m.flat = []store.Session{{Ticket: "A-1", State: store.StateReview, Repo: "/repo", PRURL: "https://x/pull/1"}}
	m.comments.ran = map[string]bool{"T1": true}
	if got := press(m, "enter"); got.comments.mode != modeBrowsing {
		t.Error("a done row offers no menu - the batch is history")
	}
}

// The running header shows the pipeline stage, not stateLabel's PID-liveness
// verdict, which reads "stopped" for the first seconds of every run.
func TestRunningHeaderNamesTheStage(t *testing.T) {
	m := runningModel(t)
	m.flat[0].State = store.StateBuilding
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "building") {
		t.Errorf("want the live stage in the header\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "stopped") {
		t.Errorf("a live run must not report itself stopped\n--- got ---\n%s", out)
	}
}

// A comment whose file is not in the worktree still gets a real code window:
// the hunk renders with the same gutter and anchor as the live view - numbered
// new-side lines, a red unnumbered removed line, ▶ on the commented line - not
// a gray dump of the raw diff.
func TestHunkRendersLikeTheCodeWindow(t *testing.T) {
	m := commentsModel()
	m.comments.rows[0] = cmt("T1", "bob", func(c *store.Comment) {
		c.Path, c.Line = "app/A.kt", 130
		c.DiffHunk = "@@ -129,3 +129,3 @@ class A(\n     val a = 1\n-    repeat(3) { attempt ->\n+    repeat(MAX_RETRIES) { attempt ->"
	})
	m.renderFocused()
	out := strings.Join(m.codeRows(100), "\n")
	plain := ansiRe.ReplaceAllString(out, "")
	if !strings.Contains(plain, "129") || !strings.Contains(plain, "val a = 1") {
		t.Errorf("context lines should carry their new-file number\n--- got ---\n%s", plain)
	}
	// Every line carries the rail, so the diff reads as a diff at a glance.
	if !strings.Contains(plain, "┆") {
		t.Errorf("the diff should carry its rail\n--- got ---\n%s", plain)
	}
	added := ""
	for _, ln := range strings.Split(plain, "\n") {
		if strings.Contains(ln, "MAX_RETRIES") {
			added = ln
		}
	}
	if !strings.Contains(added, "+") {
		t.Errorf("the added line should render with a + sign\n--- got ---\n%s", plain)
	}
	removed := ""
	for _, ln := range strings.Split(plain, "\n") {
		if strings.Contains(ln, "repeat(3)") {
			removed = ln
		}
	}
	if !strings.Contains(removed, "-") {
		t.Errorf("the removed line should render with a - sign\n--- got ---\n%s", plain)
	}
	// No number in its gutter: it has none in the new file. Everything between
	// the rail and the sign is blank.
	rail := strings.Index(removed, "┆")
	sign := strings.Index(removed, "-")
	if rail < 0 || sign < rail {
		t.Fatalf("unexpected removed-line shape: %q", removed)
	}
	if strings.TrimSpace(removed[rail+len("┆"):sign]) != "" {
		t.Errorf("a removed line has no number in the new file\n--- got ---\n%q", removed)
	}
}

// Pressing the run button hands the work to the dashboard, not to a log-tail
// screen. Manual QA called the old behavior what it was - blocking the user on
// a wall of agent output for a minutes-long run. The dashboard files the
// ticket under RUNNING and shows the same log in its detail pane; the screen
// state survives for re-entry.
func TestFixButtonReturnsToTheDashboard(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.Claim("A-1", "/repo", "x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetState("A-1", store.StateReview, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertThreads("A-1", "https://github.com/o/r/pull/482",
		[]review.Thread{
			{ID: "T1", Kind: review.KindThread, Author: "bob", Path: "a.kt", Line: 1, Body: "x", LastCommentID: "c1"},
		}, true); err != nil {
		t.Fatal(err)
	}
	m := baseModel()
	m.store = st
	got, _ := m.openCommentsView(store.Session{
		Ticket: "A-1", Repo: "/repo", State: store.StateReview,
		PRURL: "https://github.com/o/r/pull/482",
	})
	mm := got.(monitorModel)
	mm.flat = []store.Session{{Ticket: "A-1", Repo: "/repo", State: store.StateReview}}

	mm.comments.cursor = len(mm.visible()) // the run button
	if !mm.onRunBar() {
		t.Fatal("cursor should be on the run button")
	}
	got2, cmd := mm.activate()
	after := got2.(monitorModel)

	if after.view != viewDashboard {
		t.Errorf("view = %v after pressing the button, want the dashboard", after.view)
	}
	if cmd == nil {
		t.Error("no spawn command returned - the run never starts")
	}
	if !strings.Contains(after.notice, "fixing 1 comment") ||
		!strings.Contains(after.notice, "A-1") {
		t.Errorf("notice = %q, want it to say what is running and for which ticket", after.notice)
	}
	// The batch must be queued before the view leaves: the spawned run reads it.
	q, err := st.QueuedComments("A-1")
	if err != nil || len(q) != 1 {
		t.Fatalf("queued = %v (err %v), want the checked comment queued", len(q), err)
	}
}

// The pane's code window centers on the ▶ anchor. GitHub's hunks END at the
// commented line, and the old head-first cut hid exactly the line the pane
// exists to show (a 60-line hunk rendered as 60 lines of imports).
func TestPaneCodeWindowKeepsTheAnchorVisible(t *testing.T) {
	m := commentsModel()
	m.height = 34 // room for the window plus its edge indicators
	// A long hunk whose anchor is its last line, like GitHub serves them.
	var hunk strings.Builder
	hunk.WriteString("@@ -1,60 +1,60 @@\n")
	for i := 1; i <= 59; i++ {
		hunk.WriteString(fmt.Sprintf(" filler line %d\n", i))
	}
	hunk.WriteString("+the commented line\n")
	m.comments.rows[0].DiffHunk = hunk.String()
	m.comments.rows[0].Line = 60
	m.comments.cursor = 0
	m.comments.code = nil // force the hunk path

	rows := m.paneCodeRows(100)
	joined := stripAnsi(strings.Join(rows, "\n"))
	if !strings.Contains(joined, "the commented line") {
		t.Fatalf("the anchor line is not in the window:\n%s", joined)
	}
	if !strings.Contains(joined, "line(s) above") {
		t.Errorf("a window that skipped the top must say so:\n%s", joined)
	}
}

// The menu's "See the whole diff" opens the full-screen viewer; esc returns.
func TestMenuSeeWholeDiff(t *testing.T) {
	m := commentsModel()
	m.width, m.height = 100, 30
	m.comments.cursor = 0
	m.openMenu()
	found := -1
	for i, e := range m.commentMenu() {
		if e.label == "See the whole diff" {
			found = i
		}
	}
	if found < 0 {
		t.Fatal("menu is missing See the whole diff")
	}
	m.comments.menuItem = found
	got, _ := m.updateComments(keyOf("enter"))
	m2 := got.(monitorModel)
	if m2.comments.cardMode != cardDiff {
		t.Fatal("choosing it must open the viewer")
	}
	out := ansiRe.ReplaceAllString(m2.renderComments(100), "")
	if !strings.Contains(out, "the whole diff") || !strings.Contains(out, "esc back to the comments") {
		t.Fatalf("viewer not rendered:\n%s", truncate(out, 300))
	}
	got, _ = m2.updateComments(keyOf("esc"))
	m3 := got.(monitorModel)
	if m3.comments.cardMode != cardView || m3.view != viewComments {
		t.Fatal("esc must return to the triage list, not the dashboard")
	}
}
