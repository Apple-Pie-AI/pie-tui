package tui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// commentsModelWithBots is the three-human screen plus three bot comments, one
// of them blocking. Under the filter that used to ship, the blocking one was
// invisible by default - which is the whole reason it does not ship any more.
func commentsModelWithBots() monitorModel {
	m := commentsModel()
	m.comments.rows = append(m.comments.rows,
		cmt("B1", "coderabbitai[bot]", func(c *store.Comment) {
			c.Bot, c.Line = true, 57
			c.Body = "blocking: viewModelScope.launch without a handler will crash the process."
		}),
		cmt("B2", "sonarcloud[bot]", func(c *store.Comment) {
			c.Bot, c.Line = true, 31
			c.Body = "okhttp 4.9.3 is affected by CVE-2023-0833. Bump to 4.12.0."
		}),
		cmt("B3", "github-actions[bot]", func(c *store.Comment) {
			c.Bot, c.Line = true, 22
			c.Body = "chore: detekt - LongParameterList, 7 parameters, threshold is 5."
		}),
	)
	for _, c := range m.comments.rows {
		if _, known := m.comments.sel[c.ID]; !known {
			m.comments.sel[c.ID] = true
		}
	}
	return m
}

// Nothing is filtered. A reviewer's comment the screen hides is a comment that
// ships unanswered, and the bots are where the blocking findings turned up.
func TestNothingIsHidden(t *testing.T) {
	m := commentsModelWithBots()
	if n := len(m.visible()); n != 6 {
		t.Fatalf("%d comments listed, want all 6 - bots included", n)
	}
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	for _, want := range []string{"CVE-2023-0833", "viewModelScope.launch", "detekt"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list is hiding %q\n--- got ---\n%s", want, out)
		}
	}
	// A bot is marked by its tint and by dropping the [bot] suffix, never by
	// being absent.
	if !strings.Contains(out, "coderabbitai") {
		t.Error("bot authors should be named in the list")
	}
	if strings.Contains(out, "[bot]") {
		t.Errorf("the [bot] suffix repeats what the tint says and costs 5 columns\n--- got ---\n%s", out)
	}
}

// The aggregate row is the only bulk control, and it behaves like every other
// row: same box, same enter.
func TestAllRowChecksAndUnchecksEverything(t *testing.T) {
	m := commentsModelWithBots()
	if !m.hasAllRow() {
		t.Fatal("the aggregate row should be drawn on the idle screen")
	}
	m = press(m, "up") // from the first comment onto it
	if !m.onAllRow() {
		t.Fatalf("cursor = %d, want the aggregate row at %d", m.comments.cursor, cursorAll)
	}

	m = press(m, "enter")
	if n := len(m.selectedIDs()); n != 0 {
		t.Fatalf("%d still checked after enter on the aggregate row", n)
	}
	m = press(m, "enter")
	if n := len(m.selectedIDs()); n != 6 {
		t.Fatalf("%d checked after enter again, want all 6", n)
	}

	// It is the first row: up from it goes nowhere.
	if m = press(m, "up"); !m.onAllRow() {
		t.Error("the aggregate row is the top of the list; up must clamp there")
	}
	if m = press(m, "down"); m.comments.cursor != 0 {
		t.Errorf("cursor = %d, want the first comment", m.comments.cursor)
	}
}

// A partial selection reads as partial. Showing [ ] while the run bar counts
// four would be the row contradicting the thing directly below it.
func TestAllRowShowsPartialSelection(t *testing.T) {
	m := commentsModelWithBots()
	m.comments.sel["T1"] = false
	out := ansiRe.ReplaceAllString(m.renderAllRow(100), "")
	if !strings.Contains(out, "[~]") {
		t.Errorf("a partial selection should read as partial\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "5 checked · 1 skipped") {
		t.Errorf("the aggregate row should carry the counts\n--- got ---\n%s", out)
	}
}

// One comment, one line. Group headings and blank spacers turned a list of
// eight into eight separate islands.
func TestListIsConsecutiveLinesWithNoHeadings(t *testing.T) {
	m := commentsModelWithBots()
	m.comments.cursor = 0
	out := ansiRe.ReplaceAllString(m.renderCommentList(100), "")
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")

	if len(lines) != 6 {
		t.Fatalf("%d lines for 6 comments, want one line each\n--- got ---\n%s", len(lines), out)
	}
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			t.Errorf("line %d is blank - rows must be consecutive", i)
		}
		if !strings.Contains(ln, "[x]") && !strings.Contains(ln, "[ ]") {
			t.Errorf("line %d is not a comment row: %q", i, ln)
		}
	}
	// The directory belongs in the detail pane, which has width for it.
	if strings.Contains(out, "app/src/main/java") {
		t.Errorf("the list is carrying source paths\n--- got ---\n%s", out)
	}
}

// The review summary is a reviewer's most load-bearing paragraph. Rendering it
// dim made it look disabled.
func TestReviewSummaryIsNotDimmed(t *testing.T) {
	m := commentsModel()
	m.comments.rows[2].Path, m.comments.rows[2].Line = "", 0
	out := ansiRe.ReplaceAllString(m.renderCommentList(100), "")
	if !strings.Contains(out, "summary") {
		t.Fatalf("the summary row should be labelled\n--- got ---\n%s", out)
	}
	// It has no heading of its own and no separate render path - it is a row like
	// any other, which is what keeps it at the same brightness.
	if strings.Contains(out, "REVIEW SUMMARY") {
		t.Errorf("the summary should be a row, not a heading\n--- got ---\n%s", out)
	}
	row := ansiRe.ReplaceAllString(m.renderCommentRow(m.comments.rows[2], 100, false), "")
	plain := ansiRe.ReplaceAllString(m.renderCommentRow(m.comments.rows[1], 100, false), "")
	if lipWidth(row) != lipWidth(plain) {
		t.Errorf("the summary row is laid out differently from an ordinary comment:\n%q\n%q", row, plain)
	}
	if !strings.Contains(row, "[x]") {
		t.Errorf("the summary row should carry the same checkbox: %q", row)
	}
}

func TestSeverity(t *testing.T) {
	tests := []struct {
		body, tag, rest string
	}{
		{"nit: rename loading", sevNit, "rename loading"},
		{"Nitpick: this import is unused.", sevNit, "this import is unused."},
		{"blocking: this will crash", sevBlock, "this will crash"},
		{"block - swallowed exception", sevBlock, "swallowed exception"},
		{"chore: bump the plugin", sevChore, "bump the plugin"},
		// No marker, so no tag. Guessing severity from wording would be a
		// confident lie about what the reviewer meant.
		{"This timeout should be configurable.", "", "This timeout should be configurable."},
		// The word alone, unpunctuated, is part of the sentence.
		{"blocking the release on this one", "", "blocking the release on this one"},
		{"nitrogen levels look wrong", "", "nitrogen levels look wrong"},
		// A marker with nothing after it is not a severity, it is the comment.
		{"nit:", "", "nit:"},
	}
	for _, tc := range tests {
		tag, rest := severity(tc.body)
		if tag != tc.tag || rest != tc.rest {
			t.Errorf("severity(%q) = (%q, %q), want (%q, %q)", tc.body, tag, rest, tc.tag, tc.rest)
		}
	}
}

// The columns degrade in a fixed order so a narrow terminal loses prose, never
// the line number that says which code a comment is about.
func TestColumnsDegradeInOrder(t *testing.T) {
	wide, _, _ := commentCols(100)
	if wide != 22 {
		t.Errorf("location = %d at 100 cols, want its full 22", wide)
	}
	for _, w := range []int{100, 80, 70, 60, 50, 40} {
		loc, body, auth := commentCols(w)
		if loc < 18 {
			t.Errorf("at %d cols the location is %d, never allowed below 18", w, loc)
		}
		if body < 1 || auth < 1 {
			t.Errorf("at %d cols: loc=%d body=%d auth=%d - no column may vanish",
				w, loc, body, auth)
		}
		if total := colBox + colMark + colGap + loc + body + auth; total > w {
			t.Errorf("at %d cols the row sums to %d and would wrap", w, total)
		}
	}
	// The author is given up before the location.
	loc55, _, auth55 := commentCols(55)
	if auth55 >= 13 && loc55 < 22 {
		t.Error("the location gave up width while the author still had some to give")
	}
}

// A location too long for its column keeps its tail: the line number is the
// part that says which code this is.
func TestLocationTruncatesFromTheFront(t *testing.T) {
	got := truncFront("VeryLongFileNameIndeed.kt:4210", 18)
	if lipWidth(got) > 18 {
		t.Fatalf("truncFront returned %d cells, want at most 18: %q", lipWidth(got), got)
	}
	if !strings.HasSuffix(got, ":4210") {
		t.Errorf("truncFront(%q) = %q, want the line number kept", "VeryLongFileNameIndeed.kt:4210", got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("a truncated location should say so: %q", got)
	}
}

// The screen carries one header. A second one above it, brighter and counting a
// different scope, contradicted the screen's own numbers.
func TestScreenHasOneHeader(t *testing.T) {
	m := commentsModel()
	m.width, m.height = 100, 30
	out := ansiRe.ReplaceAllString(m.View(), "")
	if strings.Contains(out, "needs you") || strings.Contains(out, "daemon") {
		t.Errorf("the global app bar should be suppressed on this screen\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, letterSpace("APPLE PIE")) {
		t.Errorf("the screen should carry the brand in its own header\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "PR #482") {
		t.Errorf("the screen should name the PR in its own header\n--- got ---\n%s", out)
	}
}

// Fetching is automatic, so its whole interface is a line of text.
func TestSyncLabel(t *testing.T) {
	m := commentsModel()
	if got := m.syncLabel(); got != "syncing…" {
		t.Errorf("before the first fetch: %q, want %q", got, "syncing…")
	}
	m.comments.syncFailed = true
	if got := m.syncLabel(); !strings.Contains(got, "sync failed") {
		t.Errorf("a failed fetch should say so in the header, got %q", got)
	}
	m.comments.syncFailed = false
	m.comments.lastFetch = time.Now().Add(-2 * time.Minute)
	if got := m.syncLabel(); !strings.HasPrefix(got, "synced ") {
		t.Errorf("after a fetch: %q, want a 'synced ... ago' reading", got)
	}
}

// Every row is laid out on the same columns, so their right edges line up all
// the way down the list. A row that renders one cell wider than its neighbour
// puts a step in the author column, and at the terminal's width it wraps.
func TestColumnsLineUpAtEveryWidth(t *testing.T) {
	for _, w := range []int{80, 100, 140} {
		m := commentsModelWithBots()
		m.comments.rows[0].UserNote = "an instruction, which adds a ✎"
		m.comments.sel["T2"] = false // an unchecked row paints differently
		m.comments.cursor = 2        // and one row carries the selection band

		var lines []string
		lines = append(lines, strings.TrimRight(
			ansiRe.ReplaceAllString(m.renderAllRow(w), ""), "\n"))
		lines = append(lines, strings.Split(strings.TrimRight(
			ansiRe.ReplaceAllString(m.renderCommentList(w), ""), "\n"), "\n")...)

		want := lipWidth(lines[0])
		for i, ln := range lines {
			if got := lipWidth(ln); got != want {
				t.Errorf("at %d cols row %d is %d cells, want %d:\n%q", w, i, got, want, ln)
			}
			if lipWidth(ln) > w {
				t.Errorf("at %d cols row %d overflows to %d cells", w, i, lipWidth(ln))
			}
		}
		// The author column is right-aligned against the same edge on every row.
		if want > w {
			t.Errorf("at %d cols the rows are %d cells wide", w, want)
		}
	}
}

// The screen must fit the terminal exactly at every height, with the footer on
// the last row and the instruction line above it.
//
// This is the regression that made "your instruction" invisible: the pane was
// budgeted by a rough guess, the frame came out three lines taller than the
// terminal, and the lines that fell off the bottom were the instruction and the
// footer - the pane's most important line and the only place the keys are
// written down.
func TestScreenAlwaysFitsTheTerminal(t *testing.T) {
	long := strings.Repeat("Don't swallow the exception here, it hides real failures. ", 8)
	for _, comments := range []int{1, 3, 8, 40} {
		for h := 20; h <= 60; h++ {
			m := commentsModel()
			m.comments.rows[0].Body = long
			for i := len(m.comments.rows); i < comments; i++ {
				m.comments.rows = append(m.comments.rows, cmt("F"+strconv.Itoa(i), "eve"))
			}
			m.comments.rows = m.comments.rows[:min(comments, len(m.comments.rows))]
			for _, c := range m.comments.rows {
				m.comments.sel[c.ID] = true
			}
			m.width, m.height = 100, h
			m.comments.cursor = 0
			m.renderFocused()

			out := stripAnsi(m.View())
			lines := strings.Split(out, "\n")
			if len(lines) != h {
				t.Fatalf("%d comments at height %d: rendered %d lines", comments, h, len(lines))
			}
			if !strings.Contains(out, "your instruction") {
				t.Fatalf("%d comments at height %d: the instruction line is gone", comments, h)
			}
			if !strings.Contains(lines[len(lines)-1], "esc back") {
				t.Fatalf("%d comments at height %d: the footer is not the last row: %q",
					comments, h, lines[len(lines)-1])
			}
		}
	}
}

// A long list scrolls rather than pushing the run row off the screen, and the
// cursor stays inside the window it scrolls.
func TestLongListScrollsAndKeepsTheCursorInView(t *testing.T) {
	m := commentsModel()
	for i := 0; i < 40; i++ {
		m.comments.rows = append(m.comments.rows, cmt("F"+strconv.Itoa(i), "eve"))
	}
	for _, c := range m.comments.rows {
		m.comments.sel[c.ID] = true
	}
	m.width, m.height = 100, 30

	for _, cur := range []int{0, 5, 20, 42} {
		m.comments.cursor = cur
		m.renderFocused()
		start, end := m.listWindow()
		if cur < len(m.visible()) && (cur < start || cur >= end) {
			t.Errorf("cursor %d is outside the drawn window [%d,%d)", cur, start, end)
		}
		if end-start > m.listHeight() {
			t.Errorf("window [%d,%d) is bigger than the %d rows that fit", start, end, m.listHeight())
		}
		out := stripAnsi(m.View())
		if !strings.Contains(out, "AND PREVIEW THE FIXES") {
			t.Errorf("cursor %d: the run row was pushed off the screen", cur)
		}
	}
}
