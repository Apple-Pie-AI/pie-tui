// Integration test for the allow-&-re-run remediation loop, replaying the
// field failure (PLEX-59443: KMP monorepo, project in compassKMP/, three
// rounds of "Allow denied commands & re-run" that never converged).
//
// It drives the real pipeline the TUI action drives - verifyLog/stream ->
// ParseBlockedLines/MergeDenials -> SuggestAllowRules against the effective
// allowlist -> append to extra_allowed_tools -> config Save/Load -> next
// round - with no claude subprocess, so it is fully deterministic.
//
// Deliberately written against APIs that exist both before and after the
// permissions hardening, and asserting only loop invariants (suggestions are
// actionable, never redundant, and the loop converges), so the SAME file
// compiles and runs on both versions: red on the old code is the bug
// reproduced, green on the new code is the fix proven.
package runner

import (
	"regexp"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// remediationRound is one needs-you round as the field user saw it: what the
// stream reported denied, and what the verify agent left in verifyLog.
type remediationRound struct {
	name   string
	stream []agent.Denial
	log    string
}

// fieldRounds reconstructs the three rounds from the field logs. Each round
// the agent - taught by the previous refusal - invents a new command shape.
var fieldRounds = []remediationRound{
	{
		name: "round 1: sanctioned compound refused, agent narrates",
		stream: []agent.Denial{
			{Tool: "Skill", Command: "company-gradle-build"},
		},
		log: "Verification could not complete because the build was not runnable.\n" +
			"BLOCKED: cd compassKMP && ./gradlew :composeApp:testDebugUnitTest\n" +
			"BLOCKED: Unable to execute the test script due to permission restrictions.\n",
	},
	{
		name: "round 2: env prefix and tee",
		log: "BLOCKED: JAVA_HOME=/opt/jdk17 ./gradlew test\n" +
			"BLOCKED: ./run-tests.sh 2>&1 | tee /tmp/build-output.log\n",
	},
	{
		name: "round 3: git diff (already allowed) and a wrapper script",
		log: "BLOCKED: git diff main...HEAD\n" +
			"BLOCKED: /Users/field/compass/compassKMP/run-tests.sh --unit\n",
	},
}

// allowToken tokenizes an allowlist the way the permission system reads it:
// a Bash(...) rule is one token even when its head has a space.
var allowToken = regexp.MustCompile(`Bash\([^)]*\)|\S+`)

var (
	bashHead   = regexp.MustCompile(`^Bash\(([^):]+)`)
	headWordOK = regexp.MustCompile(`^[a-z0-9_./~+-][A-Za-z0-9_./~+=-]*$`)
)

var knownToolNames = map[string]bool{
	"Skill": true, "WebFetch": true, "WebSearch": true, "Task": true,
	"NotebookEdit": true,
}

// ruleWellFormed is the syntax-level bar every token in a stored config must
// meet: something Claude Code's --allowed-tools can act on at all. Prefix
// rules validate their head (prose like "Unable" fails); exact-command rules
// - Bash(<verbatim command>), the escalation shape for commands whose heads
// are already allowed - validate each segment's leading binary the same way.
func ruleWellFormed(rule string) bool {
	if strings.HasPrefix(rule, "Bash(") && strings.HasSuffix(rule, ")") {
		inner := rule[len("Bash(") : len(rule)-1]
		if head, ok := strings.CutSuffix(inner, ":*"); ok {
			if i := strings.IndexByte(head, ':'); i >= 0 {
				head = head[:i]
			}
			return headWordsOK(head)
		}
		return exactRuleSegmentsOK(inner, false)
	}
	return knownToolNames[rule]
}

// ruleSuggestible is the higher bar for what the remediation flow may
// RECOMMEND writing into a config forever: well-formed, and no machine-local
// absolute path as any segment's executable.
func ruleSuggestible(rule string) bool {
	if !ruleWellFormed(rule) {
		return false
	}
	if strings.HasPrefix(rule, "Bash(") && strings.HasSuffix(rule, ")") {
		inner := rule[len("Bash(") : len(rule)-1]
		if head, ok := strings.CutSuffix(inner, ":*"); ok {
			return !strings.HasPrefix(head, "/")
		}
		return exactRuleSegmentsOK(inner, true)
	}
	return true
}

// headWordsOK validates a prefix-rule head word by word ("git diff" is two).
func headWordsOK(head string) bool {
	if strings.TrimSpace(head) == "" {
		return false
	}
	for _, w := range strings.Fields(head) {
		if w == "+x" {
			continue
		}
		if !headWordOK.MatchString(w) {
			return false
		}
	}
	return true
}

// exactRuleSegmentsOK checks an exact-command rule's inner text: every
// compound segment must start with a binary-shaped token (skipping VAR=
// prefixes), optionally banning absolute paths as executables.
func exactRuleSegmentsOK(cmd string, banAbsolute bool) bool {
	segs := strings.FieldsFunc(cmd, func(r rune) bool { return r == '|' || r == ';' })
	var flat []string
	for _, s := range segs {
		flat = append(flat, strings.Split(s, "&&")...)
	}
	sawExec := false
	for _, seg := range flat {
		fields := strings.Fields(seg)
		i := 0
		for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "=") {
			i++
		}
		if i >= len(fields) {
			continue // an operator-split remnant like "2>&1"
		}
		head := fields[i]
		if banAbsolute && strings.HasPrefix(head, "/") {
			return false
		}
		if !headWordOK.MatchString(head) {
			return false
		}
		sawExec = true
	}
	return sawExec
}

// acceptRules is what the TUI's "Allow denied commands & re-run" does with a
// suggestion list: append it to extra_allowed_tools verbatim.
func acceptRules(cfg *config.Config, rules []string) {
	joined := strings.TrimSpace(cfg.ExtraAllowedTools + " " + strings.Join(rules, " "))
	cfg.ExtraAllowedTools = joined
}

// TestRemediationLoopConverges replays the field user's three rounds through
// the escalation ladder (generalized head rules first, exact-command rules
// when the heads are already allowed, nothing after that), accepting every
// suggestion like they did, and asserts the invariants a usable remediation
// loop must hold:
//
//  1. every suggested rule is actionable - command-shaped, never derived
//     from prose, never a machine-local absolute path as an executable;
//  2. no lap suggests a rule the effective allowlist already carries
//     (accepting it would change nothing - the loop's wheels spin);
//  3. the ladder TERMINATES: within two accept-laps per failure set the
//     suggester goes quiet, instead of running forever.
func TestRemediationLoopConverges(t *testing.T) {
	cfg := &config.Config{}

	// Two accept-laps: the first generalizes, the second may escalate a
	// still-denied command to its exact rule. A third lap must be silent.
	for lap := 1; lap <= 2; lap++ {
		for _, round := range fieldRounds {
			allowed := cfg.EffectiveAllowedTools()
			denials := agent.MergeDenials(round.stream, agent.ParseBlockedLines(round.log))
			rules := agent.SuggestAllowRules(denials, allowed)

			have := map[string]bool{}
			for _, tok := range allowToken.FindAllString(allowed, -1) {
				have[tok] = true
			}
			for _, r := range rules {
				if !ruleSuggestible(r) {
					t.Errorf("lap %d, %s: suggested garbage rule %q - accepting it writes junk into extra_allowed_tools", lap, round.name, r)
				}
				if have[r] {
					t.Errorf("lap %d, %s: re-suggested %q which the allowlist already carries - accepting it changes nothing and the loop never converges", lap, round.name, r)
				}
			}
			acceptRules(cfg, rules)
		}
	}

	// Both laps accepted. The same failures must now produce zero suggestions
	// - anything still denied needs a different fix (guarded, prose, or a
	// settings-layer rule) and the UI must say so instead of offering another
	// lap.
	for _, round := range fieldRounds {
		denials := agent.MergeDenials(round.stream, agent.ParseBlockedLines(round.log))
		if again := agent.SuggestAllowRules(denials, cfg.EffectiveAllowedTools()); len(again) != 0 {
			t.Errorf("%s: after accepting two laps, replay still suggests %v - the ladder does not terminate", round.name, again)
		}
	}
}

// TestRemediationConfigRoundTripStaysActionable saves a config whose extras
// hold what three field rounds actually wrote (valid rules mixed with
// prose-derived garbage) and reloads it through the real Save/Load path. Every
// token that comes back must be one --allowed-tools can act on: a config that
// re-serves garbage degrades every later run.
func TestRemediationConfigRoundTripStaysActionable(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	in := &config.Config{
		ExtraAllowedTools: "Skill Bash(tee:*) Bash(Unable:*) Bash(/Users/field/compass/run-tests.sh:*)",
	}
	if err := config.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range allowToken.FindAllString(got.ExtraAllowedTools, -1) {
		if !ruleWellFormed(tok) {
			t.Errorf("loaded config still serves unactionable token %q", tok)
		}
	}
	for _, want := range []string{"Skill", "Bash(tee:*)"} {
		if !strings.Contains(got.ExtraAllowedTools, want) {
			t.Errorf("valid rule %q lost in the round trip (extras: %q)", want, got.ExtraAllowedTools)
		}
	}
}

// TestRemediationHappyPath pins the full escalation ladder on one clean case:
// a genuinely missing rule is generalized first (lap 1), a repeat denial with
// the head present escalates to the exact command (lap 2 - the CLI-drift
// remedy), and a repeat with both present suggests nothing (the flavor-aware
// message and the approval callback own whatever remains).
func TestRemediationHappyPath(t *testing.T) {
	cfg := &config.Config{}
	denials := []agent.Denial{{Tool: "Bash", Command: "xcodebuild -scheme App build"}}

	rules := agent.SuggestAllowRules(denials, cfg.EffectiveAllowedTools())
	if len(rules) != 1 || rules[0] != "Bash(xcodebuild:*)" {
		t.Fatalf("lap 1: want [Bash(xcodebuild:*)], got %v", rules)
	}
	acceptRules(cfg, rules)

	rules = agent.SuggestAllowRules(denials, cfg.EffectiveAllowedTools())
	if len(rules) != 1 || rules[0] != "Bash(xcodebuild -scheme App build)" {
		t.Fatalf("lap 2: want the exact-rule escalation, got %v", rules)
	}
	acceptRules(cfg, rules)

	if again := agent.SuggestAllowRules(denials, cfg.EffectiveAllowedTools()); len(again) != 0 {
		t.Errorf("after both laps, replay still suggests %v", again)
	}
}
