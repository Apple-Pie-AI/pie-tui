package agent

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// canned stream lines mirroring the real shapes captured from claude 2.1.220.
const streamWithDenials = `{"type":"system","subtype":"init","session_id":"sess1234","model":"m"}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"cd App && ./gradlew testDebugUnitTest --tests \"com.example.LongEnoughThatTruncationWouldCorruptTheRemediationRule\""}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"This command requires approval"}]}}
{"type":"result","subtype":"success","result":"blocked","session_id":"sess1234","permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"cd App && ./gradlew testDebugUnitTest --tests \"com.example.LongEnoughThatTruncationWouldCorruptTheRemediationRule\""}},{"tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"gradle help"}},{"tool_name":"Bash","tool_use_id":"t3","tool_input":{"command":"gradle help"}}]}
`

func TestParseStreamExtractsPermissionDenials(t *testing.T) {
	var lines []string
	logf := func(f string, a ...interface{}) { lines = append(lines, fmt.Sprintf(f, a...)) }
	res := parseStream(strings.NewReader(streamWithDenials), logf, nil, nil, nil, nil, nil)

	if res.SessionID != "sess1234" {
		t.Errorf("SessionID = %q", res.SessionID)
	}
	want := []Denial{
		{Tool: "Bash", Command: `cd App && ./gradlew testDebugUnitTest --tests "com.example.LongEnoughThatTruncationWouldCorruptTheRemediationRule"`},
		{Tool: "Bash", Command: "gradle help"},
	}
	if !reflect.DeepEqual(res.Denials, want) {
		t.Errorf("Denials = %#v\nwant deduped verbatim %#v", res.Denials, want)
	}
	// The refusal is now visible live in the ticket log.
	if all := strings.Join(lines, "\n"); !strings.Contains(all, "✗ tool error: This command requires approval") {
		t.Errorf("tool_result error not logged:\n%s", all)
	}
}

func TestParseStreamCapturesAPIError(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"s1","model":"m"}
{"type":"result","subtype":"error_during_execution","is_error":true,"result":"API Error: Request rejected (429) - ExceededBudget","session_id":"s1"}
`
	res := parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil, nil, nil, nil)
	if !strings.Contains(res.ErrorText, "ExceededBudget") {
		t.Errorf("ErrorText = %q, want the API error captured", res.ErrorText)
	}
}

func TestParseStreamCleanRunHasNoDenialsOrError(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"s2","model":"m"}
{"type":"result","subtype":"success","result":"done","session_id":"s2","permission_denials":[]}
`
	res := parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil, nil, nil, nil)
	if res.SessionID != "s2" || len(res.Denials) != 0 || res.ErrorText != "" {
		t.Errorf("clean run: %+v", res)
	}
}

func TestSuggestAllowRules(t *testing.T) {
	cases := []struct {
		name    string
		denials []Denial
		allowed string
		want    []string
	}{
		{"compound command, one segment already allowed",
			[]Denial{{Tool: "Bash", Command: "cd App && ./gradlew test"}},
			"Bash(./gradlew:*)",
			[]string{"Bash(cd:*)"}},
		{"pipe segments",
			[]Denial{{Tool: "Bash", Command: "./gradlew test 2>&1 | tail -5"}},
			"",
			[]string{"Bash(./gradlew:*)", "Bash(tail:*)"}},
		{"env prefix skipped to the binary",
			[]Denial{{Tool: "Bash", Command: "JAVA_HOME=/x ./gradlew help"}},
			"",
			[]string{"Bash(./gradlew:*)"}},
		{"chmod keeps its two-token shape",
			[]Denial{{Tool: "Bash", Command: "chmod +x gradlew"}},
			"",
			[]string{"Bash(chmod +x:*)"}},
		{"git keeps its subcommand",
			[]Denial{{Tool: "Bash", Command: "git fetch origin"}},
			"",
			[]string{"Bash(git fetch:*)"}},
		{"git state mutation is never suggested",
			[]Denial{{Tool: "Bash", Command: "git checkout e5087fc"}},
			"",
			nil},
		{"guarded segment dropped, safe segment kept",
			[]Denial{{Tool: "Bash", Command: "cd App && git reset --hard"}},
			"",
			[]string{"Bash(cd:*)"}},
		{"rm and sudo are never suggested",
			[]Denial{{Tool: "Bash", Command: "rm -rf build"}, {Tool: "Bash", Command: "sudo gradle build"}},
			"",
			nil},
		{"non-Bash tool suggests the tool",
			[]Denial{{Tool: "WebSearch", Command: "query"}},
			"",
			[]string{"WebSearch"}},
		{"everything already allowed escalates to an exact rule",
			// The head rule exists yet the CLI refused anyway (the PLEX-61263
			// class): the exact-command fallback re-arms the one-key fix.
			[]Denial{{Tool: "Bash", Command: "gradle help"}},
			"Bash(gradle:*)",
			[]string{"Bash(gradle help)"}},
		{"duplicate heads collapse",
			[]Denial{{Tool: "Bash", Command: "gradle help"}, {Tool: "Bash", Command: "gradle build"}},
			"",
			[]string{"Bash(gradle:*)"}},
		{"quoted operator stays one segment",
			[]Denial{{Tool: "Bash", Command: `echo 'a && b'`}},
			"",
			[]string{"Bash(echo:*)"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SuggestAllowRules(tc.denials, tc.allowed)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseBlockedLinesRoundTrip(t *testing.T) {
	log := "Ran discovery.\nBLOCKED: ./App/gradlew test\nsome other line\n  BLOCKED: adb devices\nBLOCKED:\n"
	got := ParseBlockedLines(log)
	want := []Denial{
		{Tool: "Bash", Command: "./App/gradlew test"},
		{Tool: "Bash", Command: "adb devices"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	// And both signal paths converge through MergeDenials without duplicates.
	merged := MergeDenials(got, []Denial{{Tool: "Bash", Command: "adb devices"}, {Tool: "Bash", Command: "gradle help"}})
	if len(merged) != 3 {
		t.Errorf("merged = %#v, want 3 unique", merged)
	}
}

func TestMarshalUnmarshalDenials(t *testing.T) {
	in := []Denial{{Tool: "Bash", Command: `cd App && ./gradlew "quoted"`}}
	if got := UnmarshalDenials(MarshalDenials(in)); !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip: %#v", got)
	}
	if MarshalDenials(nil) != "" {
		t.Error("nil should marshal to the store's empty value")
	}
	if UnmarshalDenials("") != nil || UnmarshalDenials("not json") != nil {
		t.Error("empty/garbage should unmarshal to nil")
	}
}

func TestPromptsCarryPermissionGuidance(t *testing.T) {
	review := BuildReviewPrompt("T-1", "s", &Plan{Plan: "p"})
	if !strings.Contains(review, ReviewTools) || !strings.Contains(review, "STATIC review") {
		t.Error("review prompt must name its allowlist and forbid running builds")
	}
	plan := BuildPlanPrompt("T-1", "s", "d", PlanTools)
	impl := BuildImplPrompt("T-1", "s", "d", &Plan{Plan: "p"}, "Bash(./gradlew:*)", "")
	verify := BuildVerifyPrompt("T-1", "s", "/wt", false, true, "Bash(./gradlew:*)", "", "")

	for name, c := range map[string]struct{ prompt, marker string }{
		"plan carries its allowlist":        {plan, PlanTools},
		"impl carries its allowlist":        {impl, "Bash(./gradlew:*)"},
		"verify carries its allowlist":      {verify, "Bash(./gradlew:*)"},
		"verify defines BLOCKED convention": {verify, `prefixed exactly "BLOCKED: "`},
		"verify bans build-config patches":  {verify, "gradle-daemon-jvm.properties"},
		"verify steers KMP to unit tasks":   {verify, "Multiplatform"},
		"env-prefix warning present":        {verify, "VAR=value"},
	} {
		if !strings.Contains(c.prompt, c.marker) {
			t.Errorf("%s: %q missing", name, c.marker)
		}
	}
}

// Malformed or unusual permission_denials entries must never panic or corrupt
// the result: missing tool_input, null entries, non-Bash tools, empty command.
func TestParseStreamDenialEdgeCases(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"sX","model":"m"}
{"type":"result","subtype":"success","result":"done","session_id":"sX","permission_denials":[null,{"tool_name":"Bash"},{"tool_name":"","tool_input":{"command":"ghost"}},{"tool_name":"WebSearch","tool_use_id":"t","tool_input":{"query":"q"}},{"tool_name":"Bash","tool_input":{"command":""}}]}
`
	res := parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil, nil, nil, nil)
	if res.SessionID != "sX" {
		t.Errorf("SessionID = %q", res.SessionID)
	}
	// Survivors: Bash-without-input (empty command), WebSearch (detail from
	// fallback field), Bash-empty-command. Tool=="" entries are dropped.
	for _, d := range res.Denials {
		if d.Tool == "" {
			t.Errorf("empty-tool denial kept: %#v", d)
		}
	}
}

// An is_error result with an EMPTY result string must not fabricate ErrorText,
// and a benign result containing the words "API Error" mid-sentence must not
// either (only a prefix counts when is_error is unset).
func TestParseStreamErrorTextEdges(t *testing.T) {
	const empty = `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"","session_id":"s"}
`
	if res := parseStream(strings.NewReader(empty), func(string, ...interface{}) {}, nil, nil, nil, nil, nil); res.ErrorText != "" {
		t.Errorf("empty error result fabricated ErrorText %q", res.ErrorText)
	}
	const benign = `{"type":"result","subtype":"success","result":"wrote docs about an API Error message","session_id":"s"}
`
	if res := parseStream(strings.NewReader(benign), func(string, ...interface{}) {}, nil, nil, nil, nil, nil); res.ErrorText != "" {
		t.Errorf("benign mention classified as error: %q", res.ErrorText)
	}
}

// A command that is ONLY an env assignment has no head to suggest; the result
// must be empty rather than a nonsense rule like Bash(FOO=bar:*).
func TestGuardedCommands(t *testing.T) {
	got := GuardedCommands([]Denial{
		{Tool: "Bash", Command: "git checkout e5087fc"},
		{Tool: "Bash", Command: "./gradlew test"},
		{Tool: "Bash", Command: "cd App && git reset --hard"},
		{Tool: "WebSearch", Command: "query"},
	})
	want := []string{"git checkout e5087fc", "cd App && git reset --hard"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if g := GuardedCommands([]Denial{{Tool: "Bash", Command: "gradle build"}}); g != nil {
		t.Errorf("safe commands must not be reported as guarded, got %v", g)
	}
}

func TestSuggestAllowRulesPureEnvAssignment(t *testing.T) {
	got := SuggestAllowRules([]Denial{{Tool: "Bash", Command: "JAVA_HOME=/x"}}, "")
	if got != nil {
		t.Errorf("pure env assignment suggested %v, want nothing", got)
	}
}

// The PLEX-61263 dead end: every generalized head is already on the allowlist,
// yet the CLI refused the commands. The fallback suggests exact-command rules
// so the one-key fix re-arms - except for guarded compounds, junk, and the
// case where head rules were produced (the generalization is the better fix).
func TestSuggestAllowRulesExactFallback(t *testing.T) {
	allowed := "Bash(cd:*) Bash(./gradlew:*) Bash(head:*)"
	cases := []struct {
		name    string
		denials []Denial
		allowed string
		want    []string
	}{
		{"heads already allowed -> exact rules",
			[]Denial{
				{Tool: "Bash", Command: "cd Compass && ./gradlew --version"},
				{Tool: "Bash", Command: "cd Compass && ./gradlew :compass:testAgentStagingDebugUnitTest 2>&1 | head -50"},
			},
			allowed,
			[]string{
				"Bash(cd Compass && ./gradlew --version)",
				"Bash(cd Compass && ./gradlew :compass:testAgentStagingDebugUnitTest 2>&1 | head -50)",
			}},
		{"exact rule already present -> zero (loop terminates)",
			[]Denial{{Tool: "Bash", Command: "cd Compass && ./gradlew --version"}},
			allowed + " Bash(cd Compass && ./gradlew --version)",
			nil},
		{"guarded compound -> no exact rule",
			[]Denial{{Tool: "Bash", Command: "cd Compass && git checkout main"}},
			allowed,
			nil},
		{"head rules produced -> no exact fallback",
			[]Denial{{Tool: "Bash", Command: "cd Compass && adb devices"}},
			"Bash(cd:*)",
			[]string{"Bash(adb:*)"}},
		{"paren-bearing command -> skipped (would corrupt rule tokenizing)",
			[]Denial{{Tool: "Bash", Command: "cd Compass && echo $(date)"}},
			"Bash(cd:*) Bash(echo:*)",
			nil},
		{"non-Bash denial -> untouched by the fallback",
			[]Denial{{Tool: "SomethingWeird", Command: "x"}},
			allowed,
			nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SuggestAllowRules(tc.denials, tc.allowed); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("rules = %#v, want %#v", got, tc.want)
			}
		})
	}
}
