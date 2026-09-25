package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.log")
	if err := os.WriteFile(p, []byte("a\n\nb\n  \nc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := tailLines(p, 2)
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("tailLines = %#v, want [b c] (blank lines dropped)", got)
	}
	if tailLines(filepath.Join(dir, "missing.log"), 3) != nil {
		t.Error("missing file should return nil")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("no truncation when it fits")
	}
	if got := truncate("hello world", 6); got != "hello…" {
		t.Errorf("truncate = %q, want hello…", got)
	}
}

// ansiRe strips terminal color codes so tests can match glamour's plain text.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// renderMarkdownLines produces styled, non-empty rows and never returns nil for
// real content (falls back to raw markdown on any renderer error).
func TestRenderMarkdownLines(t *testing.T) {
	lines := renderMarkdownLines("# Title\n\n- alpha item\n- bravo item\n", 60)
	if len(lines) == 0 {
		t.Fatal("expected rendered rows")
	}
	plain := ansiRe.ReplaceAllString(strings.Join(lines, "\n"), "")
	for _, want := range []string{"Title", "alpha item", "bravo item"} {
		if !strings.Contains(plain, want) {
			t.Errorf("rendered output missing %q in:\n%s", want, plain)
		}
	}
}

// The log pane broke words in half: "unconventi / onal", "del / eted". Wrapping
// happens on spaces now, and only a word too long for the line is ever cut.
func TestWrapWordsNeverBreaksMidWord(t *testing.T) {
	const w = 24
	in := "the placement here is unconventional and it deleted the wrong file"
	got := wrapWords(in, w)
	for _, ln := range got {
		if lipWidth(ln) > w {
			t.Errorf("segment is %d cells, want <= %d: %q", lipWidth(ln), w, ln)
		}
	}
	// Every word survives whole and in order.
	if rejoined := strings.Join(strings.Fields(strings.Join(got, " ")), " "); rejoined != in {
		t.Errorf("words changed:\n got %q\nwant %q", rejoined, in)
	}
	// A word longer than the line still has to be cut - the alternative overflows.
	long := wrapWords("https://github.com/acme/android/pull/482#discussion_r5511xyz", 20)
	for _, ln := range long {
		if lipWidth(ln) > 20 {
			t.Errorf("long word not cut to width: %q", ln)
		}
	}
	// An indent belongs to every segment, or continuations read as new entries.
	for i, ln := range wrapWords("    a bb ccc dddd eeeee ffffff", 12) {
		if i > 0 && !strings.HasPrefix(ln, "    ") {
			t.Errorf("continuation lost the indent: %q", ln)
		}
	}
}

// The agent narrates a finding twice - once as the message, again as a summary -
// and the tail is short enough that one repeat costs a third of it.
func TestDedupeAdjacent(t *testing.T) {
	in := []string{
		"Duplicate VideoItem class", "Duplicate VideoItem class",
		"  duplicate   videoitem   CLASS  ",              // same line, squashed and cased differently
		"Wrote report.json", "Duplicate VideoItem class", // far apart: a loop, keep it
	}
	got := dedupeAdjacent(in)
	if len(got) != 3 {
		t.Errorf("got %d lines, want 3: %q", len(got), got)
	}
	if got[len(got)-1] != "Duplicate VideoItem class" {
		t.Errorf("a non-adjacent repeat was dropped; that is a loop worth seeing: %q", got)
	}
}

// The log pane is not a markdown renderer, so the markers arrived on screen.
func TestStripEmphasis(t *testing.T) {
	cases := map[string]string{
		"**Duplicate VideoItem class**":       "Duplicate VideoItem class",
		"*Apple Pie needs your help on [14]*": "Apple Pie needs your help on [14]",
		"a `code` span":                       "a code span",
		"2 * 3 is not emphasis":               "2 * 3 is not emphasis", // one marker, left alone
		"plain":                               "plain",
	}
	for in, want := range cases {
		if got := stripEmphasis(in); got != want {
			t.Errorf("stripEmphasis(%q) = %q, want %q", in, got, want)
		}
	}
}

// The regression that forced the word-boundary rewrite: the permission park's
// copy-paste TOML line was mangled by the pane - extra_allowed_tools lost its
// underscores and the Bash rules their asterisks. Content that merely
// CONTAINS marker characters must pass through untouched; only markers that
// wrap a phrase on word boundaries are emphasis.
func TestStripEmphasisPreservesConfigContent(t *testing.T) {
	cases := map[string]string{
		`extra_allowed_tools = "Bash(./gradlew:*) Bash(cd:*)"`:   `extra_allowed_tools = "Bash(./gradlew:*) Bash(cd:*)"`,
		`│ extra_allowed_tools = "Bash(./gradlew:*)" │`:          `│ extra_allowed_tools = "Bash(./gradlew:*)" │`,
		`(if extra_allowed_tools already exists, append inside)`: `(if extra_allowed_tools already exists, append inside)`,
		`allowlist: 30 rule(s): Bash(cd:*) Bash(java:*)`:         `allowlist: 30 rule(s): Bash(cd:*) Bash(java:*)`,
		"snake_case_name stays":                                  "snake_case_name stays",
		// ...while real emphasis still strips, even on the same kinds of line:
		"🤖 *Configuration issue on [X] - one keystroke fixes it*": "🤖 Configuration issue on [X] - one keystroke fixes it",
		"run `pie logs T-1` for details":                          "run pie logs T-1 for details",
	}
	for in, want := range cases {
		if got := stripEmphasis(in); got != want {
			t.Errorf("stripEmphasis(%q) = %q, want %q", in, got, want)
		}
	}
}
