package cmd

import (
	"io"
	"strings"
	"testing"
)

// editLine reads runes from an io.RuneReader; strings.Reader satisfies it, so we
// can feed synthetic keystrokes (including escape sequences) and assert the
// edited result. \x1b[D = left, \x1b[C = right, \x7f = backspace,
// \x1b[3~ = delete, \x1b[H = home, \x1b[F = end.
func TestEditLine(t *testing.T) {
	esc := "\x1b"
	cases := []struct {
		name    string
		initial string
		in      string
		want    string
	}{
		{"plain", "", "hello\r", "hello"},
		{"trims spaces", "", "  hi  \r", "hi"},
		{"backspace", "", "helllo" + "\x7f" + "\r", "helll"},
		{"left then insert", "", "ac" + esc + "[D" + "b\r", "abc"},
		{"two lefts then insert", "", "cd" + esc + "[D" + esc + "[D" + "ab\r", "abcd"},
		{"left then right then insert", "", "ac" + esc + "[D" + esc + "[C" + "d\r", "acd"},
		{"home then insert", "", "bc" + esc + "[H" + "a\r", "abc"},
		{"end after home then insert", "", "ab" + esc + "[H" + esc + "[F" + "c\r", "abc"},
		{"delete key", "", "axbc" + esc + "[H" + esc + "[C" + esc + "[3~" + "\r", "abc"},
		{"newline terminates", "", "hi\n", "hi"},
		{"unicode insert with left", "", "cafe" + esc + "[D" + "é\r", "cafée"},
		// Pre-filled buffer: Enter accepts it unchanged.
		{"prefill accept", "{ticket}-{slug}", "\r", "{ticket}-{slug}"},
		// Pre-filled buffer: append at the (end) cursor.
		{"prefill append", "feature/", "{ticket}\r", "feature/{ticket}"},
		// Pre-filled buffer: cursor starts at end, backspace deletes from the end.
		{"prefill backspace", "abc", "\x7f\r", "ab"},
		// Pre-filled buffer: Home then edit the front.
		{"prefill home insert", "bc", esc + "[H" + "a\r", "abc"},
	}
	for _, c := range cases {
		got, abort := editLine(strings.NewReader(c.in), io.Discard, "> ", c.initial)
		if abort {
			t.Errorf("%s: unexpected abort", c.name)
		}
		if got != c.want {
			t.Errorf("%s: editLine(initial=%q, in=%q) = %q, want %q", c.name, c.initial, c.in, got, c.want)
		}
	}
}

func TestEditLineCtrlC(t *testing.T) {
	got, abort := editLine(strings.NewReader("hello\x03"), io.Discard, "> ", "")
	if !abort {
		t.Error("ctrl-c should signal abort")
	}
	if got != "" {
		t.Errorf("aborted line should be empty, got %q", got)
	}
}

// EOF (reader drained without a terminator) returns what was typed so far.
func TestEditLineEOF(t *testing.T) {
	got, abort := editLine(strings.NewReader("partial"), io.Discard, "> ", "")
	if abort {
		t.Error("EOF is not an abort")
	}
	if got != "partial" {
		t.Errorf("EOF result = %q, want %q", got, "partial")
	}
}
