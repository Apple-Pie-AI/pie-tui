package vcs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRenderPRBody(t *testing.T) {
	out, err := RenderPRBody(PRData{
		Ticket:       "PROJ-388",
		Summary:      "Remove deprecated lint baseline entries",
		Branch:       "ai/proj-388-lint",
		FilesChanged: []string{"app/lint-baseline.xml"},
		Tests:        "✅ compile + unit tests",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[PROJ-388]", "ai/proj-388-lint", "app/lint-baseline.xml", "compile + unit"} {
		if !strings.Contains(out, want) {
			t.Errorf("PR body missing %q\n%s", want, out)
		}
	}
}

func TestRenderJiraComment(t *testing.T) {
	out, err := RenderJiraComment(PRData{PRURL: "https://gh/x/pull/1", Tests: "pass", Attempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "pull/1") || !strings.Contains(out, "3 attempt") {
		t.Errorf("unexpected comment: %s", out)
	}
}

func TestMissingSections(t *testing.T) {
	tmpl := "## Summary\nDescribe the change.\n\n## Changes\n- list files\n\n## Checks\n- [ ] tests\n\n## Notes\nOptional.\n"

	// All sections present → nil.
	full := "## Summary\nDid X.\n\n## Changes\n- foo.kt\n\n## Checks\n- [x] tests\n\n## Notes\nNone.\n"
	if got := MissingSections(tmpl, full); got != nil {
		t.Errorf("full body: expected nil, got %v", got)
	}

	// Two sections absent.
	partial := "## Summary\nDid X.\n\n## Checks\n- [x] tests\n"
	got := MissingSections(tmpl, partial)
	if len(got) != 2 {
		t.Fatalf("partial body: want 2 missing, got %v", got)
	}
	missing := map[string]bool{got[0]: true, got[1]: true}
	if !missing["Changes"] || !missing["Notes"] {
		t.Errorf("wrong missing sections: %v", got)
	}

	// Case-insensitive: "checks" matches "## Checks".
	caseBody := "## summary\nDid X.\n## changes\n- foo\n## checks\n- ok\n## notes\n- none\n"
	if got := MissingSections(tmpl, caseBody); got != nil {
		t.Errorf("case-insensitive: expected nil, got %v", got)
	}
}

func TestRenderPRBodyEmptyFallbacks(t *testing.T) {
	// Use a temp home so the disk template (if any) doesn't shadow the default.
	t.Setenv("PIE_HOME", t.TempDir())

	// Empty FilesChanged → fallback text, not a bare heading.
	out, err := RenderPRBody(PRData{Ticket: "T-1", Summary: "fix", Branch: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(not reported)") {
		t.Errorf("empty FilesChanged: expected fallback text, got:\n%s", out)
	}
	if !strings.Contains(out, "## Changes") {
		t.Errorf("Changes heading missing:\n%s", out)
	}

	// Non-empty Tests field renders normally.
	out2, err := RenderPRBody(PRData{
		Ticket:       "T-2",
		Summary:      "fix",
		Branch:       "b",
		FilesChanged: []string{"a.kt"},
		Tests:        "✅ pass",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out2, "(not reported)") {
		t.Errorf("non-empty fields should not show fallback:\n%s", out2)
	}
	if !strings.Contains(out2, "a.kt") {
		t.Errorf("expected file in output:\n%s", out2)
	}
}

func TestRepairSections(t *testing.T) {
	tmpl := "## Summary\nDescribe.\n\n## Changes\n- list files\n\n## Notes\nOptional.\n"
	body := "## Summary\nDid X.\n"

	missing := MissingSections(tmpl, body)
	if len(missing) != 2 {
		t.Fatalf("pre-repair: want 2 missing, got %v", missing)
	}
	repaired := RepairSections(tmpl, body, missing)

	// Existing content untouched.
	if !strings.Contains(repaired, "## Summary\nDid X.") {
		t.Errorf("original body modified: %q", repaired)
	}
	// Missing sections appended verbatim.
	if !strings.Contains(repaired, "## Changes") || !strings.Contains(repaired, "## Notes") {
		t.Errorf("missing sections not appended:\n%s", repaired)
	}
	// After repair, MissingSections should return nil.
	if got := MissingSections(tmpl, repaired); got != nil {
		t.Errorf("still missing after repair: %v", got)
	}
}

func TestReadRepoTemplate(t *testing.T) {
	dir := t.TempDir()

	// No template → empty string.
	if got := ReadRepoTemplate(dir); got != "" {
		t.Errorf("no template: expected \"\", got %q", got)
	}

	// .github/PULL_REQUEST_TEMPLATE.md → found.
	ghDir := filepath.Join(dir, ".github")
	if err := os.MkdirAll(ghDir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := "## Summary\n- list here\n"
	if err := os.WriteFile(filepath.Join(ghDir, "PULL_REQUEST_TEMPLATE.md"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadRepoTemplate(dir); got != want {
		t.Errorf("ReadRepoTemplate = %q, want %q", got, want)
	}
}

// PRs are created as drafts (the pipeline stops at review by design); the
// argv builder is the testable seam since OpenPR shells out to gh.
func TestPRCreateArgsDraft(t *testing.T) {
	got := strings.Join(prCreateArgs("main", "[feat][T-1] x", "/tmp/b.md", true), " ")
	want := "pr create --base main --title [feat][T-1] x --body-file /tmp/b.md --draft"
	if got != want {
		t.Errorf("draft argv = %q, want %q", got, want)
	}
	if plain := strings.Join(prCreateArgs("main", "t", "/tmp/b.md", false), " "); strings.Contains(plain, "--draft") {
		t.Errorf("non-draft argv must not carry --draft: %q", plain)
	}
}

// GitHub Free private repos reject --draft; the exact phrasing gh surfaces
// must be recognized so OpenPR can fall back to a regular PR.
func TestDraftUnsupported(t *testing.T) {
	if !draftUnsupported([]byte("pull request create failed: GraphQL: Draft pull requests are not supported in this repository. (createPullRequest)")) {
		t.Error("gh draft-unsupported error not recognized")
	}
	for _, benign := range []string{
		"a pull request for branch \"x\" already exists",
		"GraphQL: was submitted too quickly",
		"",
	} {
		if draftUnsupported([]byte(benign)) {
			t.Errorf("false positive on %q", benign)
		}
	}
}

// writeFakeGH puts a fake `gh` first on PATH for the running test only, and
// returns the path to a log file it appends one line to per invocation (the
// args it was called with) - so a test can assert exactly which gh
// subcommands ran, not just the end result. `gh pr view` answers with
// viewOutput/viewExit (simulating whether a PR already exists for the
// branch); `gh pr create` answers with createOutput/createExit.
func writeFakeGH(t *testing.T, viewOutput string, viewExit int, createOutput string, createExit int) (logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	script := "#!/bin/sh\n" +
		"echo \"$*\" >> " + shellQuote(logPath) + "\n" +
		"case \"$1 $2\" in\n" +
		"\"pr view\") cat <<'PIE_EOF'\n" + viewOutput + "\nPIE_EOF\n" +
		"exit " + itoa(viewExit) + " ;;\n" +
		"\"pr create\") cat <<'PIE_EOF'\n" + createOutput + "\nPIE_EOF\n" +
		"exit " + itoa(createExit) + " ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
func itoa(n int) string          { return strconv.Itoa(n) }

// OpenPR must check for an existing PR BEFORE ever calling `gh pr create` -
// a --from-branch ticket continuing someone else's open PR (or a second
// resume/ship on this one) must never get a second, competing PR opened.
func TestOpenPRReusesExistingPRWithoutCreating(t *testing.T) {
	logPath := writeFakeGH(t, "https://github.com/x/y/pull/9", 0, "SHOULD NOT BE CALLED", 0)
	url, existed, err := OpenPR(t.TempDir(), "main", "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if !existed || url != "https://github.com/x/y/pull/9" {
		t.Errorf("existed=%v url=%q, want existed=true url=the existing PR", existed, url)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "pr create") {
		t.Errorf("must not call `gh pr create` when a PR already exists, log:\n%s", log)
	}
}

// When no PR exists yet, OpenPR creates one - and only then.
func TestOpenPRCreatesWhenNoneExists(t *testing.T) {
	logPath := writeFakeGH(t, "", 1, "https://github.com/x/y/pull/10", 0)
	url, existed, err := OpenPR(t.TempDir(), "main", "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if existed || url != "https://github.com/x/y/pull/10" {
		t.Errorf("existed=%v url=%q, want existed=false url=the new PR", existed, url)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "pr create") {
		t.Errorf("must call `gh pr create` when no PR exists yet, log:\n%s", log)
	}
}
