// The permissions-hardening regressions: prose must never become a denial,
// garbage must never become an allowlist rule, and each permission mode must
// spawn claude with exactly the flags that mode means.
package agent

import (
	"reflect"
	"strings"
	"testing"
)

// A field user's verify agent wrote sentences after BLOCKED:, and each one
// became a fabricated Bash denial whose "head" (Unable, The, ...) was then
// suggested - and written - into extra_allowed_tools.
func TestParseBlockedLinesRejectsProse(t *testing.T) {
	log := strings.Join([]string{
		"BLOCKED: Unable to execute the test script due to permission restrictions.",
		"BLOCKED: The command was refused by the allowlist,",
		"BLOCKED: " + strings.Repeat("x", 250),
		"BLOCKED: ./gradlew testDebugUnitTest",
		"BLOCKED: JAVA_HOME=/opt/jdk ./gradlew build",
		"BLOCKED: cd compassKMP && ./gradlew check",
	}, "\n")
	got := ParseBlockedLines(log)
	want := []Denial{
		{Tool: "Bash", Command: "./gradlew testDebugUnitTest"},
		{Tool: "Bash", Command: "JAVA_HOME=/opt/jdk ./gradlew build"},
		{Tool: "Bash", Command: "cd compassKMP && ./gradlew check"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Round 3 in the field re-suggested Bash(git diff:*) even though the default
// allowlist carries it: strings.Fields split the two-word rule into halves
// that matched nothing.
func TestSuggestAllowRulesDedupsTwoWordRules(t *testing.T) {
	// The two-word head rule is recognized as already present (the tokenizer
	// keeps "Bash(git diff:*)" whole), so no duplicate head rule appears -
	// the suggestion escalates to the exact command instead, and a second
	// round with that exact rule present converges to nil.
	got := SuggestAllowRules(
		[]Denial{{Tool: "Bash", Command: "git diff main...HEAD"}},
		"Bash(git diff:*) Bash(./gradlew:*)")
	if want := []string{"Bash(git diff main...HEAD)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want the exact-rule escalation %v", got, want)
	}
	got = SuggestAllowRules(
		[]Denial{{Tool: "Bash", Command: "git diff main...HEAD"}},
		"Bash(git diff:*) Bash(./gradlew:*) Bash(git diff main...HEAD)")
	if got != nil {
		t.Errorf("exact rule already present, want nil (convergence), got %v", got)
	}
}

// Non-Bash denials may only suggest tools pie actually knows; anything else in
// the Tool field is a parse artifact.
func TestSuggestAllowRulesKnownToolsOnly(t *testing.T) {
	if got := SuggestAllowRules([]Denial{{Tool: "Skill", Command: "gradle-build"}}, ""); !reflect.DeepEqual(got, []string{"Skill"}) {
		t.Errorf("Skill is a known tool, want [Skill], got %v", got)
	}
	if got := SuggestAllowRules([]Denial{{Tool: "Unable", Command: "whatever"}}, ""); got != nil {
		t.Errorf("unknown tool must not be suggested, got %v", got)
	}
}

// Heads that could only have come from prose or a machine-local path must not
// become permanent rules, even if a fabricated denial slips through.
func TestSuggestAllowRulesRejectsImplausibleHeads(t *testing.T) {
	for _, cmd := range []string{
		"Unable to execute the script",
		"/Users/someone/project/run-tests.sh --all",
	} {
		if got := SuggestAllowRules([]Denial{{Tool: "Bash", Command: cmd}}, ""); got != nil {
			t.Errorf("%q: want no suggestion, got %v", cmd, got)
		}
	}
}

func TestBuildArgsPermissionModes(t *testing.T) {
	base := Options{AllowedTools: "Bash(./gradlew:*)"}

	auto := base
	auto.PermissionMode = "auto"
	args := strings.Join(buildArgs("p", auto), " ")
	if !strings.Contains(args, "--permission-mode auto") {
		t.Errorf("auto: --permission-mode auto missing: %s", args)
	}
	if strings.Contains(args, "--allowed-tools") {
		t.Errorf("auto: --allowed-tools must be omitted: %s", args)
	}

	byp := base
	byp.PermissionMode = "bypass"
	args = strings.Join(buildArgs("p", byp), " ")
	if !strings.Contains(args, "--permission-mode bypassPermissions") {
		t.Errorf("bypass: bypassPermissions missing: %s", args)
	}

	args = strings.Join(buildArgs("p", base), " ")
	if !strings.Contains(args, "--permission-mode acceptEdits") ||
		!strings.Contains(args, "--allowed-tools Bash(./gradlew:*)") {
		t.Errorf("default: acceptEdits+allowlist expected: %s", args)
	}

	plan := base
	plan.PermissionMode = "plan"
	args = strings.Join(buildArgs("p", plan), " ")
	if !strings.Contains(args, "--permission-mode plan") {
		t.Errorf("plan: --permission-mode plan missing: %s", args)
	}
	if strings.Contains(args, "--allowed-tools") {
		t.Errorf("plan: --allowed-tools must be omitted - the CLI itself refuses every edit tool: %s", args)
	}
}

// Options.Inbox switches the prompt from a -p argument to the first stdin
// frame (the CLI ignores -p under --input-format stream-json).
func TestBuildArgsInboxUsesStdinNotDashP(t *testing.T) {
	args := buildArgs("do the thing", Options{Inbox: func() []string { return nil }})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--input-format stream-json") {
		t.Errorf("Inbox set: --input-format stream-json missing: %s", joined)
	}
	if strings.Contains(joined, "do the thing") {
		t.Errorf("Inbox set: the prompt must not appear as a -p argument: %s", joined)
	}

	without := strings.Join(buildArgs("do the thing", Options{}), " ")
	if strings.Contains(without, "--input-format") {
		t.Errorf("no Inbox: --input-format must be omitted: %s", without)
	}
	if !strings.Contains(without, "do the thing") {
		t.Errorf("no Inbox: the prompt must still be a -p argument: %s", without)
	}
}

// The prompts must tell the agent where the build lives and which command
// shapes survive the allowlist - the field failure was an agent that knew
// neither and spiraled through unmatched variations.
func TestPromptsNameProjectDirAndSanctionedForms(t *testing.T) {
	verify := BuildVerifyPrompt("T-1", "s", "/wt", false, true, "Bash(./gradlew:*) Bash(compassKMP/gradlew:*)", "compassKMP", "")
	for _, marker := range []string{
		"compassKMP",
		"-Dorg.gradle.java.home",
		// The session runs at the WORKTREE ROOT (a subdir cwd killed the CLI at
		// launch on PLEX-57773). The sanctioned monorepo shape is the wrapper
		// by relative path with -p, which Claude Code's prefix matcher honors
		// via the derived Bash(compassKMP/gradlew:*) rule. A `cd X && ./gradlew`
		// compound is refused even by a rule that quotes it verbatim.
		"compassKMP/gradlew -p compassKMP",
		"Bash(compassKMP/gradlew:*)",
	} {
		if !strings.Contains(verify, marker) {
			t.Errorf("verify prompt: %q missing", marker)
		}
	}
	for _, retired := range []string{
		"working directory IS the Gradle project root",
		"cd compassKMP && ./gradlew",
	} {
		if strings.Contains(verify, retired) {
			t.Errorf("verify prompt still carries the retired guidance %q", retired)
		}
	}
	if strings.Contains(verify, "checked per segment") {
		t.Error("verify prompt still carries the false per-segment claim")
	}

	impl := BuildImplPrompt("T-1", "s", "d", &Plan{Plan: "p"}, "Bash(./gradlew:*)", "compassKMP")
	if !strings.Contains(impl, "compassKMP") {
		t.Error("impl prompt: project dir missing")
	}

	// Root-level project: no stray subdirectory instruction.
	rootVerify := BuildVerifyPrompt("T-1", "s", "/wt", false, true, "Bash(./gradlew:*)", "", "")
	if strings.Contains(rootVerify, "subdirectory of this worktree") {
		t.Error("root project must not get a subdirectory instruction")
	}
}

// The comment-fix stage shipped with no permissions guidance at all; its
// agent discovered the boundary by burning denials.
func TestCommentFixPromptCarriesPermissionsBlock(t *testing.T) {
	p := BuildCommentFixPrompt("T-1", "s", "main", "Bash(./gradlew:*)", []ReviewComment{{ID: "c1", Body: "fix it"}})
	if !strings.Contains(p, "TOOL PERMISSIONS") || !strings.Contains(p, "Bash(./gradlew:*)") {
		t.Error("comment-fix prompt must carry the permissions block and the allowlist")
	}
}

// Auto mode passes a blank allowlist; the block must describe the classifier,
// not render an empty list the agent would read as "nothing is allowed".
func TestPermissionsBlockAutoMode(t *testing.T) {
	b := toolPermissionsBlock("")
	if !strings.Contains(b, "safety classifier") {
		t.Errorf("blank allowlist must produce the classifier text, got: %s", b)
	}
	if strings.Contains(b, "allowlist") {
		t.Errorf("auto-mode block must not mention an allowlist: %s", b)
	}
}
