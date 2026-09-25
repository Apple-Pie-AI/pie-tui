package agent

import (
	"strings"
	"testing"
)

// The verify prompt must name adb's absolute path when an emulator is up:
// the session that motivated this ran `adb devices`, got exit 127, and
// misdiagnosed a booted emulator as "no emulator in environment".
func TestVerifyPromptNamesADB(t *testing.T) {
	const adb = "/sdk/platform-tools/adb"

	got := BuildVerifyPrompt("T-1", "sum", "/wt", true, true, "", "", adb)
	if !strings.Contains(got, adb) {
		t.Error("emulator=true: prompt should name the adb path")
	}

	// No emulator: unit tests only, the adb path would be a false promise.
	got = BuildVerifyPrompt("T-1", "sum", "/wt", false, true, "", "", adb)
	if strings.Contains(got, adb) {
		t.Error("emulator=false: prompt must not mention adb")
	}

	// Emulator up but no resolved path: the generic wording still stands.
	got = BuildVerifyPrompt("T-1", "sum", "/wt", true, true, "", "", "")
	if !strings.Contains(got, "emulator is booted") {
		t.Error("emulator=true without a path: booted-emulator wording missing")
	}
}

// ASKME-TEST: a verify agent allowed to fix its own red build reverted the
// change under verification to get tests green, then certified the unfixed
// code. The allowFix prompt must forbid exactly that; the read-only variant
// already forbids all edits and needs no such rule.
func TestVerifyPromptForbidsRevertingTheDeliverable(t *testing.T) {
	const rule = "THE CHANGE UNDER VERIFICATION IS THE DELIVERABLE"

	got := BuildVerifyPrompt("T-1", "sum", "/wt", false, true, "", "", "")
	if !strings.Contains(got, rule) {
		t.Error("allowFix=true: the never-revert rule is missing")
	}
	if !strings.Contains(got, `set "verified": false and explain the conflict`) {
		t.Error("allowFix=true: the conflict escape hatch is missing")
	}

	got = BuildVerifyPrompt("T-1", "sum", "/wt", false, false, "", "", "")
	if strings.Contains(got, rule) {
		t.Error("allowFix=false already forbids edits - the rule does not belong there")
	}
}

// The change-review rework prompt carries the feedback, names the round,
// tells the agent @ mentions need no further resolution, forbids the
// outward verbs, and preserves the PR body.
func TestChangeReworkPromptContract(t *testing.T) {
	got := BuildChangeReworkPrompt("@a.kt:9 rename the thing", 2)
	for _, want := range []string{
		"rename the thing",
		"round 2",
		"need no further resolution",
		"no commit, no push, no pull request",
		`"prBody"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rework prompt missing %q", want)
		}
	}
}

// The discuss prompt must tell the agent plainly this turn is read-only (so
// it doesn't narrate edits it isn't allowed to make) and carry the human's
// message and @path resolution note, same as the rework prompt.
func TestChangeDiscussPromptContract(t *testing.T) {
	got := BuildChangeDiscussPrompt("@a.kt:9 what does this do?")
	for _, want := range []string{
		"what does this do?",
		"read-only",
		"cannot",
		"need no further resolution",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("discuss prompt missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "no commit, no push") {
		t.Error("discuss prompt must not talk about shipping - it can't edit at all this turn")
	}
}
