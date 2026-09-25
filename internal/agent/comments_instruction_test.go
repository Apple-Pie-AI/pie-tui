package agent

import (
	"strings"
	"testing"
)

// PLEX-56803: the owner wrote an instruction for a comment and the fix agent
// went its own way. The instruction was one trailing line under the diff
// hunk, labeled "additional" - a footnote. It is the human's decision about
// this comment: it leads the block under its own heading, and the rules say
// it outranks the reviewer's wording when the two differ.
func TestBuildCommentFixPromptOwnerInstructionLeadsAndOutranks(t *testing.T) {
	cs := []ReviewComment{{
		ID: "PRRT_9", Author: "greptile-apps", Path: "a/B.kt", Line: 278,
		Body:     "The route arguments are discarded here.",
		DiffHunk: "@@ -270,3 +270,5 @@\n+ navigateTo(Tab.ME)",
		UserNote: "Only pass the contact id through; do not touch the nav graphs.",
	}}
	p := BuildCommentFixPrompt("PLEX-56803", "ff removal", "main", "Bash(./gradlew:*)", cs)

	label := "OWNER INSTRUCTION"
	li, hi := strings.Index(p, label), strings.Index(p, "The diff this comment is anchored to")
	if li < 0 {
		t.Fatalf("no %q section in the block:\n%s", label, p)
	}
	if hi < 0 || li > hi {
		t.Errorf("the owner instruction must come BEFORE the diff hunk (instruction at %d, hunk at %d)", li, hi)
	}
	if !strings.Contains(p, "do not touch the nav graphs") {
		t.Error("the instruction text is missing")
	}
	if !strings.Contains(p, "takes precedence over the reviewer") {
		t.Error("the rules must say the owner's instruction outranks the reviewer's wording")
	}
}
