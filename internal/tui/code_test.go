package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// worktreeWith writes a file into a fake worktree and returns its root.
func worktreeWith(t *testing.T, path, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const kotlinSrc = `package com.acme

class LoginViewModel {
    fun login(email: String) = flow {
        emit(Loading)
        try {
            val user = api.login(email)
            emit(Success(user))
        } catch (e: IOException) {
            emit(Error)
        }
    }
}
`

func TestCodeContext(t *testing.T) {
	wt := worktreeWith(t, "app/src/main/java/com/acme/LoginViewModel.kt", kotlinSrc)

	got := codeContext(wt, "app/src/main/java/com/acme/LoginViewModel.kt", 9, 3)
	if len(got) == 0 {
		t.Fatal("no code context returned")
	}
	// Line 9 is the `catch`; a radius of 3 spans 6..12.
	if got[0].num != 6 || got[len(got)-1].num != 12 {
		t.Fatalf("window = %d..%d, want 6..12", got[0].num, got[len(got)-1].num)
	}
	var marked int
	for _, l := range got {
		if l.marked {
			marked++
			if l.num != 9 {
				t.Errorf("marked line %d, want 9", l.num)
			}
			if !strings.Contains(l.text, "catch") {
				t.Errorf("line 9 = %q, want the catch line", l.text)
			}
		}
	}
	if marked != 1 {
		t.Errorf("%d lines marked, want exactly the commented one", marked)
	}
}

// Near the top of a file the window must clamp rather than ask for line -2.
func TestCodeContextClampsAtFileStart(t *testing.T) {
	wt := worktreeWith(t, "a.kt", kotlinSrc)
	got := codeContext(wt, "a.kt", 2, 8)
	if len(got) == 0 {
		t.Fatal("no context returned")
	}
	if got[0].num != 1 {
		t.Errorf("first line = %d, want 1", got[0].num)
	}
}

// Every "we cannot show the real code" case must return nil so the caller falls
// back to the diff hunk, rather than showing an empty box or an error.
func TestCodeContextFallsBackToNil(t *testing.T) {
	wt := worktreeWith(t, "a.kt", kotlinSrc)
	tests := []struct {
		name, worktree, path string
		line                 int
	}{
		{"no worktree", "", "a.kt", 3},
		{"no path (review summary)", wt, "", 3},
		{"no line", wt, "a.kt", 0},
		{"file not in the worktree", wt, "gone.kt", 3},
		{"line past the end of the file", wt, "a.kt", 9999},
		{"path escaping the worktree", wt, "../../../../etc/passwd", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codeContext(tt.worktree, tt.path, tt.line, 4); got != nil {
				t.Fatalf("want nil so the caller shows the diff hunk, got %d lines", len(got))
			}
		})
	}
}

func TestRenderCodeHasGutterAndMarker(t *testing.T) {
	lines := []codeLine{
		{num: 8, text: "            val user = api.login(email)"},
		{num: 9, text: "            emit(Success(user))", marked: true},
		{num: 10, text: "        } catch (e: IOException) {"},
	}
	rows := renderCode(lines, "LoginViewModel.kt", 100)
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	plain := ansiRe.ReplaceAllString(strings.Join(rows, "\n"), "")
	for _, want := range []string{"8", "9", "10", "▶", "api.login", "catch"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered code is missing %q\n--- got ---\n%s", want, plain)
		}
	}
	if strings.Count(plain, "▶") != 1 {
		t.Errorf("want exactly one marker, got %d\n%s", strings.Count(plain, "▶"), plain)
	}
}

// The path is what tells you WHICH file, and the identifying part is the end.
// Truncating from the right (the old behaviour) produced stubs like
// "app/src/main/java/com/acm…", which named nothing at all.
func TestShortenPathKeepsTheFilename(t *testing.T) {
	const long = "app/src/main/java/com/acme/feature/login/LoginViewModel.kt"
	tests := []struct{ max int }{{30}, {40}, {25}, {12}}
	for _, tt := range tests {
		got := shortenPath(long, tt.max)
		if !strings.HasSuffix(got, "LoginViewModel.kt") {
			t.Errorf("shortenPath(max=%d) = %q, must keep the filename", tt.max, got)
		}
	}
	// Short enough already: leave it alone.
	if got := shortenPath("a/b.kt", 40); got != "a/b.kt" {
		t.Errorf("shortenPath = %q, want it unchanged", got)
	}
	// It keeps as much leading context as fits.
	if got := shortenPath(long, 40); !strings.Contains(got, "login/") {
		t.Errorf("shortenPath(40) = %q, want it to keep the nearest directory too", got)
	}
}

// The reader must always show SOMETHING under the comment: the real file when
// it can be read, and GitHub's hunk when it cannot.
func TestReaderFallsBackToDiffHunk(t *testing.T) {
	wt := worktreeWith(t, "app/Login.kt", kotlinSrc)

	m := baseModel()
	m.view = viewComments
	m.comments.reset("A-1", "PR #1", wt)
	c := cmt("T1", "alice")
	c.Path, c.Line = "app/Login.kt", 999 // past the end of the file
	c.DiffHunk = "@@ -38,7 +38,11 @@\n-        emit(Error)"
	m.comments.rows = []store.Comment{c}
	m.renderFocused()

	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "@@ -38,7 +38,11 @@") {
		t.Errorf("want the diff hunk when the line is not in the worktree\n--- got ---\n%s", out)
	}
	// The hunk is rendered as a diff - rail, sign column, washed +/- lines - so
	// it reads as one without a caption underneath saying so.
	if !strings.Contains(out, "┆") {
		t.Errorf("the fallback should render as a diff, not as plain text\n--- got ---\n%s", out)
	}

	// Now a line that IS in the file: the real source wins.
	m.comments.rows[0].Line = 9
	m.renderFocused()
	out = ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "catch") {
		t.Errorf("want real source from the worktree\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "@@ -38,7") {
		t.Error("the diff hunk should give way to the real file")
	}
}

// An outdated comment points at a line later commits moved, so reading the
// worktree there would show something confidently wrong.
func TestOutdatedCommentPrefersTheDiffHunk(t *testing.T) {
	wt := worktreeWith(t, "app/Login.kt", kotlinSrc)
	m := baseModel()
	m.view = viewComments
	m.comments.reset("A-1", "PR #1", wt)
	c := cmt("T1", "alice")
	c.Path, c.Line, c.Outdated = "app/Login.kt", 9, true
	c.DiffHunk = "@@ -1,3 +1,3 @@\n-old line"
	m.comments.rows = []store.Comment{c}
	m.renderFocused()

	if m.comments.code != nil {
		t.Fatal("an outdated comment must not read the worktree at its stale line")
	}
	out := ansiRe.ReplaceAllString(m.renderComments(100), "")
	if !strings.Contains(out, "old line") {
		t.Errorf("an outdated comment should still show the diff it was written against\n--- got ---\n%s", out)
	}
}
