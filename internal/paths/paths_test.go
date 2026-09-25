package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootUsesPie(t *testing.T) {
	t.Setenv("PIE_HOME", "")
	root := Root()
	if !strings.HasSuffix(root, ".pie") {
		t.Errorf("Root() = %q, want path ending in .pie", root)
	}
}

func TestRootEnvOverride(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIE_HOME", tmp)
	if got := Root(); got != tmp {
		t.Errorf("Root() = %q, want %q", got, tmp)
	}
}

func TestWorktreeForAlwaysUnderRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIE_HOME", tmp)

	// non-empty repo: still uses Apple Pie home
	got := WorktreeFor("/some/android/repo", "PROJ-1")
	want := filepath.Join(tmp, "worktrees", "PROJ-1")
	if got != want {
		t.Errorf("WorktreeFor(repo, ticket) = %q, want %q", got, want)
	}

	// empty repo: same result
	got2 := WorktreeFor("", "PROJ-1")
	if got2 != want {
		t.Errorf("WorktreeFor('', ticket) = %q, want %q", got2, want)
	}
}

func TestValidTicketID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"PROJ-1", true},
		{"KAN-123", true},
		{"A", true},
		{"feature.branch-2", true},
		{"", false},
		{" ", false},
		{"  PROJ-1  ", false}, // callers must normalize before validating
		{".", false},
		{"..", false},
		{"../..", false},
		{"../../../etc/passwd", false},
		{"A/B", false},
		{`A\B`, false},
		{"/absolute", false},
		{"PROJ-1/../../..", false},
		{"PROJ\x00-1", false},
		{"PROJ\n-1", false},
	}
	for _, c := range cases {
		if got := ValidTicketID(c.id); got != c.want {
			t.Errorf("ValidTicketID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// The paths built from a ticket id are handed to os.RemoveAll (git.CreateWorktree),
// so no id - however hostile - may resolve outside the Apple Pie home. This is the
// last line of defense behind ValidTicketID.
func TestTicketPathsNeverEscapeRoot(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIE_HOME", tmp)

	hostile := []string{
		"../../../etc", "..", ".", "../sibling", "a/../../b",
		"/etc/passwd", `..\..\windows`, "", "   ",
	}
	for _, id := range hostile {
		for name, got := range map[string]string{
			"WorktreeFor": WorktreeFor("/repo", id),
			"LogFor":      LogFor(id),
			"ReportFor":   ReportFor(id),
			"PlanMDFor":   PlanMDFor(id),
			"PlanJSONFor": PlanJSONFor(id),
		} {
			clean := filepath.Clean(got)
			rel, err := filepath.Rel(tmp, clean)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				t.Errorf("%s(%q) = %q, which escapes root %q", name, id, clean, tmp)
			}
		}
	}
}

func TestUnderWorktrees(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("PIE_HOME", tmp)
	wt := filepath.Join(tmp, "worktrees")

	yes := []string{
		filepath.Join(wt, "KAN-1"),
		filepath.Join(wt, "KAN-1", "app"),
		filepath.Join(wt, "Mike's Repo"),
	}
	for _, p := range yes {
		if !UnderWorktrees(p) {
			t.Errorf("UnderWorktrees(%q) = false, want true", p)
		}
	}

	no := []string{
		"",                         // empty
		"relative/path",            // not absolute
		wt,                         // the root itself is not a worktree
		tmp,                        // Apple Pie home, but not a worktree
		"/Users/someone",           // the apostrophe-truncation result
		"/",                        // filesystem root
		filepath.Join(tmp, "logs"), // sibling dir inside Apple Pie home
		filepath.Dir(tmp),          // parent of Apple Pie home
	}
	for _, p := range no {
		if UnderWorktrees(p) {
			t.Errorf("UnderWorktrees(%q) = true, want false", p)
		}
	}
}

// WriteLocalProperties must never clobber an existing file - a seeded or
// hand-placed copy carries the machine's real SDK path.
func TestWriteLocalPropertiesNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "local.properties")
	if err := os.WriteFile(p, []byte("sdk.dir=/theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteLocalProperties(dir, "/mine"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "sdk.dir=/theirs\n" {
		t.Errorf("existing file was overwritten: %q", got)
	}

	// And still writes when absent.
	fresh := t.TempDir()
	if err := WriteLocalProperties(fresh, "/mine"); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(fresh, "local.properties"))
	if string(got) != "sdk.dir=/mine\n" {
		t.Errorf("fresh write failed: %q", got)
	}
}
