package ticket

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTicketIDFromPath(t *testing.T) {
	cases := map[string]string{
		"a/b/ticket_1.md":   "TICKET-1",
		"ticket_1.md":       "TICKET-1",
		"fix bug.md":        "FIX-BUG",
		"feature.spec.md":   "FEATURE-SPEC",
		"/tmp/Add-File.txt": "ADD-FILE",
		"___.md":            "LOCAL",
		"UPPER.md":          "UPPER",
	}
	for in, want := range cases {
		if got := IDFromPath(in); got != want {
			t.Errorf("IDFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoadLocalTicket(t *testing.T) {
	dir := t.TempDir()

	h1 := filepath.Join(dir, "ticket_1.md")
	if err := os.WriteFile(h1, []byte("# Add a file\n\nCreate one.txt with content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err := LoadLocal(h1)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Ticket != "TICKET-1" || sp.Summary != "Add a file" {
		t.Errorf("h1: got ticket=%q summary=%q", sp.Ticket, sp.Summary)
	}
	if sp.Desc != "Create one.txt with content." {
		t.Errorf("h1: body = %q", sp.Desc)
	}
	if !sp.Local {
		t.Error("h1: expected local=true")
	}

	// No H1 → first non-blank line is the title, full content is the body.
	noH1 := filepath.Join(dir, "plain.md")
	if err := os.WriteFile(noH1, []byte("\nDo the thing\nmore detail\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err = LoadLocal(noH1)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Summary != "Do the thing" {
		t.Errorf("noH1: summary = %q", sp.Summary)
	}

	// First H1 is a generic section header → skip it; title comes from the first
	// real prose line, not "Description".
	section := filepath.Join(dir, "PLEX-1.md")
	if err := os.WriteFile(section, []byte("# Description\n\nUpdate tour_nav.xml to add destinations.\n\n# Acceptance Criteria\n\n- updated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err = LoadLocal(section)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Summary != "Update tour_nav.xml to add destinations." {
		t.Errorf("section: summary = %q", sp.Summary)
	}
	// Body keeps its structure (the "# Description" heading is not consumed).
	if !strings.HasPrefix(sp.Desc, "# Description") {
		t.Errorf("section: body should retain structure, got %q", sp.Desc)
	}

	// Long opening sentence is truncated to a sane title length.
	long := filepath.Join(dir, "PLEX-2.md")
	bigLine := "Update Compass/compass/src/main/res/navigation/tour_nav.xml to add all new tours fragment destinations and change the start destination"
	if err := os.WriteFile(long, []byte("# Description\n\n"+bigLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sp, err = LoadLocal(long)
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(sp.Summary)) > maxDerivedTitleLen+1 || !strings.HasSuffix(sp.Summary, "…") {
		t.Errorf("long: summary not truncated: %q (len %d)", sp.Summary, len([]rune(sp.Summary)))
	}

	// Empty file → error.
	empty := filepath.Join(dir, "empty.md")
	if err := os.WriteFile(empty, []byte("\n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLocal(empty); err == nil {
		t.Error("empty file: expected an error, got nil")
	}
}

func TestParseTicketContent(t *testing.T) {
	// H1 title → title + body after it.
	title, body, err := ParseContent("# Add a file\n\nCreate one.txt.\n")
	if err != nil || title != "Add a file" || body != "Create one.txt." {
		t.Errorf("h1: title=%q body=%q err=%v", title, body, err)
	}

	// Generic section header is skipped; title comes from first prose line and
	// the "# Description" heading stays in the body.
	title, body, err = ParseContent("# Description\n\nUpdate the nav.\n")
	if err != nil || title != "Update the nav." {
		t.Errorf("section: title=%q err=%v", title, err)
	}
	if !strings.HasPrefix(body, "# Description") {
		t.Errorf("section: body should retain heading, got %q", body)
	}

	// No H1 → first non-blank line is the title.
	title, _, err = ParseContent("\nDo the thing\nmore\n")
	if err != nil || title != "Do the thing" {
		t.Errorf("noH1: title=%q err=%v", title, err)
	}

	// Empty content → error.
	if _, _, err := ParseContent("\n  \n"); err == nil {
		t.Error("empty: expected an error")
	}
}

func TestDetectTicketID(t *testing.T) {
	found := map[string]string{
		"Please fix PROJ-123 today":                 "PROJ-123",
		"[ABC-45] Add pull to refresh":              "ABC-45",
		"see https://x.atlassian.net/browse/PLEX-7": "PLEX-7",
		"lower proj-9 works":                        "PROJ-9",
		"# TICKET-4 - Empty State for Saved Papers": "TICKET-4",
	}
	for in, want := range found {
		if got, ok := DetectID(in); !ok || got != want {
			t.Errorf("DetectID(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"Add pull to refresh", "no id here 123", "release v2 build"} {
		if got, ok := DetectID(in); ok {
			t.Errorf("DetectID(%q) unexpectedly matched %q", in, got)
		}
	}
}

func TestIsTicketKey(t *testing.T) {
	yes := []string{"PROJ-123", "proj-1", "  ABC-45  ", "A1-9"}
	no := []string{"Add pull to refresh", "PROJ123", "PROJ-", "-1", "", "line1\nPROJ-1"}
	for _, s := range yes {
		if !IsKey(s) {
			t.Errorf("IsKey(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsKey(s) {
			t.Errorf("IsKey(%q) = true, want false", s)
		}
	}
}

func TestStripIDPrefix(t *testing.T) {
	cases := []struct{ title, id, want string }{
		{"TICKET-3 - Add Pull-to-Refresh on Feed", "TICKET-3", "Add Pull-to-Refresh on Feed"},
		{"PROJ-1: Do the thing", "PROJ-1", "Do the thing"},
		{"PROJ-1 - fix bug", "PROJ-1", "fix bug"},
		{"ticket-3 - lower key", "TICKET-3", "lower key"},  // case-insensitive
		{"Add a file", "TICKET-1", "Add a file"},           // no prefix → unchanged
		{"TICKET-30 stuff", "TICKET-3", "TICKET-30 stuff"}, // no separator after id → unchanged
		{"TICKET-3", "TICKET-3", "TICKET-3"},               // title is only the id
		{"anything", "", "anything"},                       // no id
	}
	for _, c := range cases {
		if got := StripIDPrefix(c.title, c.id); got != c.want {
			t.Errorf("StripIDPrefix(%q, %q) = %q, want %q", c.title, c.id, got, c.want)
		}
	}
}

func TestParseDroppedPaths(t *testing.T) {
	cases := map[string][]string{
		"/tmp/a.png":                       {"/tmp/a.png"},
		"/tmp/a.png /tmp/b.jpg":            {"/tmp/a.png", "/tmp/b.jpg"},
		`/My\ Files/x.png`:                 {"/My Files/x.png"}, // backslash-escaped space
		`'/My Files/y.png'`:                {"/My Files/y.png"}, // single-quoted
		`"/a b/z.png"`:                     {"/a b/z.png"},      // double-quoted
		"/tmp/a.png\n":                     {"/tmp/a.png"},      // trailing newline
		`'/one two.png' "/three four.png"`: {"/one two.png", "/three four.png"},
	}
	for in, want := range cases {
		got := ParseDroppedPaths(in)
		if len(got) != len(want) {
			t.Errorf("ParseDroppedPaths(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("ParseDroppedPaths(%q)[%d] = %q, want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestIsImageExt(t *testing.T) {
	yes := []string{"a.png", "b.PNG", "c.jpg", "d.jpeg", "e.gif", "f.webp", "/x/y.Png"}
	no := []string{"a.txt", "b.md", "c", "d.pngx", "notes.png.md"}
	for _, s := range yes {
		if !IsImageExt(s) {
			t.Errorf("IsImageExt(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if IsImageExt(s) {
			t.Errorf("IsImageExt(%q) = true, want false", s)
		}
	}
}

func TestNormalizeNewlines(t *testing.T) {
	if got := NormalizeNewlines("a\r\nb\rc\nd"); got != "a\nb\nc\nd" {
		t.Errorf("NormalizeNewlines = %q", got)
	}
}

func TestTruncateWords(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"one two three", 5, "one two three"}, // under limit → unchanged
		{"one two three four five six", 3, "one two three"},
		{"hello world", 1, "hello"},
		{"", 5, ""},
		{"café résumé", 1, "café"}, // unicode
	}
	for _, c := range cases {
		if got := TruncateWords(c.in, c.n); got != c.want {
			t.Errorf("TruncateWords(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

func TestFirstSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Fix the login bug. More detail.", "Fix the login bug"},
		{"Add upload flow\nMore info here", "Add upload flow"},
		{"No terminator here", "No terminator here"},
		{"  spaces  ", "spaces"},
		{"", ""},
	}
	for _, c := range cases {
		if got := FirstSentence(c.in); got != c.want {
			t.Errorf("FirstSentence(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := FirstNonEmpty("", "  ", "hello", "world"); got != "hello" {
		t.Errorf("FirstNonEmpty = %q, want hello", got)
	}
	if got := FirstNonEmpty("", ""); got != "" {
		t.Errorf("all empty: got %q", got)
	}
	if got := FirstNonEmpty("first"); got != "first" {
		t.Errorf("single: got %q", got)
	}
}

// TestPastedContentCRLF reproduces the real bug: a terminal paste delivers
// newlines as carriage returns, so without normalization the whole ticket looks
// like one line and the title/render get corrupted.
func TestPastedContentCRLF(t *testing.T) {
	// The ticket the user pasted, with \r line endings as a terminal sends them.
	raw := "# TICKET-3 - Add Pull-to-Refresh on Feed\r\r## Summary\rAdd pull-to-refresh.\r\r## Priority\rLow\r"
	blob := NormalizeNewlines(raw)

	if n := LineCount(blob); n != 7 {
		t.Errorf("LineCount = %d, want 7 (paste was seen as 1 line before the fix)", n)
	}
	title, _, err := ParseContent(blob)
	if err != nil || title != "TICKET-3 - Add Pull-to-Refresh on Feed" {
		t.Errorf("title = %q, err = %v", title, err)
	}
	if id, ok := DetectID(blob); !ok || id != "TICKET-3" {
		t.Errorf("DetectID = %q, %v; want TICKET-3", id, ok)
	}
	if strings.ContainsRune(title, '\r') {
		t.Error("title still contains a carriage return")
	}
}

func TestLineCount(t *testing.T) {
	cases := map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n\n": 0}
	for in, want := range cases {
		if got := LineCount(in); got != want {
			t.Errorf("LineCount(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestIDFromBranch(t *testing.T) {
	cases := map[string]string{
		"feedback/pr-42":      "feedback-pr-42",
		"fix/plex-1-login":    "fix-plex-1-login",
		"main":                "main",
		"  release/v1.2.3  ":  "release-v1.2.3",
		"weird//double/slash": "weird-double-slash",
		"":                    "branch",
		"   ":                 "branch",
		"has space and\ttab":  "has-space-and-tab",
	}
	for in, want := range cases {
		if got := IDFromBranch(in); got != want {
			t.Errorf("IDFromBranch(%q) = %q, want %q", in, got, want)
		}
	}
	// Must always be a valid worktree/dashboard id (paths.ValidTicketID's rules).
	if id := IDFromBranch("a/b\\c d\te"); strings.ContainsAny(id, `/\`) || id != strings.TrimSpace(id) {
		t.Errorf("IDFromBranch produced an invalid id: %q", id)
	}
}

// The prefix guard in DiscoverPastedImages is the entire reason a user's own
// repo images are never swept into a ticket. Mutation testing showed it could
// be inverted without a single test failing - this is that test.
func TestDiscoverPastedImagesGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIE_HOME", home)

	// A ticket inside ~/.pie/pasted/<id>/ takes its sibling images.
	pasted := filepath.Join(home, "pasted", "T-1")
	if err := os.MkdirAll(pasted, 0o755); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(pasted, "ticket.md")
	for _, f := range []string{"ticket.md", "shot.png", "note.txt"} {
		if err := os.WriteFile(filepath.Join(pasted, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	imgs := DiscoverPastedImages(md)
	if len(imgs) != 1 || filepath.Base(imgs[0]) != "shot.png" {
		t.Errorf("pasted dir: images = %v, want exactly shot.png", imgs)
	}

	// A ticket anywhere else must return nil even when images sit beside it -
	// those are the user's repo files, not attachments.
	elsewhere := t.TempDir()
	md2 := filepath.Join(elsewhere, "ticket.md")
	for _, f := range []string{"ticket.md", "logo.png", "diagram.jpeg"} {
		if err := os.WriteFile(filepath.Join(elsewhere, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if imgs := DiscoverPastedImages(md2); imgs != nil {
		t.Errorf("user dir: images = %v, want nil - never sweep a user's own files", imgs)
	}

	// The boundary case the guard's + PathSeparator exists for: a sibling dir
	// that merely shares the pasted dir's name as a prefix ("pasted-evil").
	shadow := filepath.Join(home, "pasted-evil")
	if err := os.MkdirAll(shadow, 0o755); err != nil {
		t.Fatal(err)
	}
	md3 := filepath.Join(shadow, "ticket.md")
	for _, f := range []string{"ticket.md", "x.png"} {
		if err := os.WriteFile(filepath.Join(shadow, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if imgs := DiscoverPastedImages(md3); imgs != nil {
		t.Errorf("prefix-shadow dir: images = %v, want nil", imgs)
	}
}
