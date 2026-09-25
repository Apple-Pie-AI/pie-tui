package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sampleComments() []ReviewComment {
	return []ReviewComment{
		{
			ID:       "PRRT_1",
			Author:   "alice",
			Path:     "app/src/main/java/com/x/LoginViewModel.kt",
			Line:     42,
			Body:     "Don't swallow the exception here.",
			DiffHunk: "@@ -38,7 +38,11 @@\n-        emit(Error)",
		},
		{
			ID:       "PRRT_2",
			Author:   "bob",
			Path:     "build.gradle.kts",
			Line:     12,
			Body:     "Bump the compose plugin.",
			UserNote: "also update the unit test",
			Outdated: true,
		},
		{
			ID:     "PRR_3",
			Author: "carol",
			Body:   "Use the repository pattern throughout.",
		},
	}
}

func TestBuildCommentFixPromptCarriesEveryComment(t *testing.T) {
	p := BuildCommentFixPrompt("KAN-12", "Add login", "develop", "Bash(./gradlew:*)", sampleComments())

	// Every id must reach the agent - it has to echo them back, and an id it
	// never saw is an id it cannot report on.
	for _, id := range []string{"PRRT_1", "PRRT_2", "PRR_3"} {
		if !strings.Contains(p, id) {
			t.Errorf("prompt is missing comment id %q", id)
		}
	}
	for _, want := range []string{
		"KAN-12", "Add login",
		"alice", "bob", "carol",
		"app/src/main/java/com/x/LoginViewModel.kt:42",
		"build.gradle.kts:12",
		"Don't swallow the exception here.",
		"Bump the compose plugin.",
		"Use the repository pattern throughout.",
		"@@ -38,7 +38,11 @@",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing %q", want)
		}
	}
	// The base branch drives the diff that re-establishes context.
	if !strings.Contains(p, "git diff develop...HEAD") {
		t.Error("prompt should tell the agent to diff against the PR base")
	}
}

func TestBuildCommentFixPromptGuardrails(t *testing.T) {
	p := BuildCommentFixPrompt("KAN-12", "Add login", "main", "Bash(./gradlew:*)", sampleComments())

	// The orchestrator owns the git and GitHub side. An agent that commits or
	// pushes here races finishShip and corrupts the run.
	for _, want := range []string{
		"Do NOT git commit",
		"do NOT git push",
		"do NOT reply on GitHub",
		".agent/comment_fixes.json",
		".agent/report.json",
		"one entry for EVERY comment listed above",
		"SMALLEST change",
		"SKIP it and say why",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt is missing the guardrail %q", want)
		}
	}
}

// The outdated warning must appear only where it applies: attaching it to a
// current comment would tell the agent to distrust an accurate line number.
func TestBuildCommentFixPromptOutdatedIsScoped(t *testing.T) {
	p := BuildCommentFixPrompt("KAN-12", "Add login", "main", "Bash(./gradlew:*)", sampleComments())
	if n := strings.Count(p, "OUTDATED diff"); n != 1 {
		t.Errorf("OUTDATED warning appears %d times, want exactly 1 (only PRRT_2 is outdated)", n)
	}
	// It must land inside the outdated comment's block, i.e. after its id.
	idIdx := strings.Index(p, "PRRT_2")
	warnIdx := strings.Index(p, "OUTDATED diff")
	if idIdx < 0 || warnIdx < idIdx {
		t.Error("the OUTDATED warning should be inside the outdated comment's own block")
	}
}

// A user note is optional, and its absence must not leave a dangling label.
func TestBuildCommentFixPromptUserNoteIsOptional(t *testing.T) {
	p := BuildCommentFixPrompt("KAN-12", "Add login", "main", "Bash(./gradlew:*)", sampleComments())
	if n := strings.Count(p, "OWNER INSTRUCTION"); n != 1 {
		t.Errorf("user-note label appears %d times, want 1", n)
	}
	if !strings.Contains(p, "also update the unit test") {
		t.Error("the user's note must reach the agent")
	}
}

// A review summary has no file anchor; saying so beats an empty "—:0".
func TestBuildCommentFixPromptReviewSummaryHasNoAnchor(t *testing.T) {
	p := BuildCommentFixPrompt("KAN-12", "Add login", "main", "Bash(./gradlew:*)", sampleComments())
	if !strings.Contains(p, "overall review comment (no specific file)") {
		t.Error("a comment with no path should be labelled as an overall review comment")
	}
	if strings.Contains(p, " — :0") || strings.Contains(p, "—:") {
		t.Error("a pathless comment must not render an empty file:line anchor")
	}
}

func TestBuildCommentFixPromptDefaultsBase(t *testing.T) {
	p := BuildCommentFixPrompt("KAN-12", "Add login", "", "Bash(./gradlew:*)", sampleComments())
	if !strings.Contains(p, "git diff main...HEAD") {
		t.Error("an empty base should fall back to main rather than emit a broken diff command")
	}
}

func TestReadCommentFixes(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"fixes":[
	  {"id":"PRRT_1","action":"fixed","note":"wrapped in runCatching"},
	  {"id":"PRRT_2","action":"skipped","note":"the suggested API does not exist"},
	  {"id":"PRR_3","action":"fixed","note":"extracted a repository"}
	]}`
	if err := os.WriteFile(filepath.Join(dir, ".agent", "comment_fixes.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cf, err := ReadCommentFixes(dir)
	if err != nil {
		t.Fatalf("ReadCommentFixes: %v", err)
	}
	fixed := cf.Fixed()
	if len(fixed) != 2 || fixed[0] != "PRRT_1" || fixed[1] != "PRR_3" {
		t.Fatalf("Fixed() = %v, want the two fixed ids", fixed)
	}
	skipped := cf.Skipped()
	if len(skipped) != 1 || skipped[0].ID != "PRRT_2" {
		t.Fatalf("Skipped() = %+v, want PRRT_2", skipped)
	}
	if got := cf.NoteFor("PRRT_2"); got != "the suggested API does not exist" {
		t.Errorf("NoteFor = %q", got)
	}
	if got := cf.NoteFor("nope"); got != "" {
		t.Errorf("NoteFor(unknown) = %q, want empty", got)
	}
}

// An unrecognized action must not be counted as fixed: treating "partially" or
// a typo as done would hide a request nobody answered.
func TestCommentFixesUnknownActionIsNotFixed(t *testing.T) {
	var cf CommentFixes
	if err := json.Unmarshal([]byte(`{"fixes":[{"id":"A","action":"partially"}]}`), &cf); err != nil {
		t.Fatal(err)
	}
	if got := cf.Fixed(); len(got) != 0 {
		t.Errorf("Fixed() = %v, want empty for an unknown action", got)
	}
	if got := cf.Skipped(); len(got) != 1 {
		t.Errorf("Skipped() = %+v, want the unknown action treated as not-fixed", got)
	}
}

func TestReadCommentFixesMissing(t *testing.T) {
	if _, err := ReadCommentFixes(t.TempDir()); err == nil {
		t.Fatal("want an error when comment_fixes.json is absent")
	}
}

// The agent not writing comment_fixes.json is a normal degraded path: the
// orchestrator warns and assumes the batch was handled, then still tries to
// reply on each thread. A nil receiver panicking there would crash a run whose
// fix is already committed and pushed.
func TestCommentFixesNilReceiverIsSafe(t *testing.T) {
	var cf *CommentFixes
	if got := cf.Fixed(); got != nil {
		t.Errorf("Fixed() on nil = %v, want nil", got)
	}
	if got := cf.Skipped(); got != nil {
		t.Errorf("Skipped() on nil = %v, want nil", got)
	}
	if got := cf.NoteFor("anything"); got != "" {
		t.Errorf("NoteFor() on nil = %q, want empty", got)
	}
}
