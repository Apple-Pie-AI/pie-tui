package agent

import (
	"strings"
	"testing"
)

// T1.2 - each fingerprint observed live on claude 2.1.232 (repro 2026-08-19)
// classifies to its flavor; anything else, or no captured text, is Unknown.
func TestClassifyFingerprints(t *testing.T) {
	cases := []struct {
		name string
		text string
		want DenialFlavor
	}{
		{"allowlist gap, compound",
			"This Bash command contains multiple operations. The following part requires approval: ./gradlew --version",
			FlavorAllowlistGap},
		{"allowlist gap, single",
			"This command requires approval",
			FlavorAllowlistGap},
		{"deny rule",
			"Permission to use Bash with command cd Compass && ./gradlew --version has been denied.",
			FlavorDenyRule},
		{"ask rule (the PLEX-61263 killer)",
			"Claude requested permissions to use Bash, but you haven't granted it yet.",
			FlavorAskRule},
		{"cd containment",
			"cd in '/Users/x/.pie/worktrees' was blocked. For security, Claude Code may only change directories to the allowed working directories for this session",
			FlavorCdContainment},
		{"human denied from the dashboard",
			"denied from the pie dashboard",
			FlavorHumanDenied},
		{"unrecognized text", "Exit code 1 ls: ./gradlew: No such file or directory", FlavorUnknown},
		{"no captured text", "", FlavorUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Denial{Tool: "Bash", Command: "x", ErrorText: c.text}).Flavor(); got != c.want {
				t.Errorf("Flavor(%q) = %s, want %s", c.text, got, c.want)
			}
		})
	}
}

// T1.1 - the raw tool_result error text is joined onto the denial that shares
// its tool_use_id, so the park message can name the real refusal instead of
// guessing "command allowlist".
func TestParseStreamJoinsErrorTextByToolUseID(t *testing.T) {
	const stream = `{"type":"system","subtype":"init","session_id":"s9","model":"m"}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"Claude requested permissions to use Bash, but you haven't granted it yet."}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","is_error":true,"content":[{"type":"text","text":"Permission to use Bash with command rm -rf / has been denied."}]}]}}
{"type":"result","subtype":"success","result":"blocked","session_id":"s9","permission_denials":[{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"cd Compass && ./gradlew --version"}},{"tool_name":"Bash","tool_use_id":"t2","tool_input":{"command":"rm -rf /"}},{"tool_name":"Bash","tool_use_id":"missing","tool_input":{"command":"adb devices"}}]}
`
	res := parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil, nil, nil, nil)
	if len(res.Denials) != 3 {
		t.Fatalf("Denials = %d, want 3", len(res.Denials))
	}
	if got := res.Denials[0].ErrorText; !strings.Contains(got, "haven't granted it yet") {
		t.Errorf("denial[0].ErrorText = %q, want the ask-rule refusal joined by tool_use_id", got)
	}
	if res.Denials[0].Flavor() != FlavorAskRule {
		t.Errorf("denial[0].Flavor() = %s, want ask-rule", res.Denials[0].Flavor())
	}
	if got := res.Denials[1].ErrorText; !strings.Contains(got, "has been denied") {
		t.Errorf("denial[1].ErrorText = %q, want the deny-rule text from a typed-block tool_result", got)
	}
	if res.Denials[1].Flavor() != FlavorDenyRule {
		t.Errorf("denial[1].Flavor() = %s, want deny-rule", res.Denials[1].Flavor())
	}
	// A denial with no matching tool_result keeps working, just unclassified.
	if res.Denials[2].ErrorText != "" || res.Denials[2].Flavor() != FlavorUnknown {
		t.Errorf("denial[2] = %+v, want empty ErrorText and unknown flavor", res.Denials[2])
	}
}

// T1.3 - denials round-trip through the store codec with their error text, and
// JSON written by older binaries (no errorText field) still unmarshals.
func TestMarshalDenialsRoundTripsErrorText(t *testing.T) {
	in := []Denial{{Tool: "Bash", Command: "cd Compass && ./gradlew test",
		ErrorText: "Claude requested permissions to use Bash, but you haven't granted it yet."}}
	out := UnmarshalDenials(MarshalDenials(in))
	if len(out) != 1 || out[0].ErrorText != in[0].ErrorText {
		t.Errorf("round trip = %+v, want ErrorText preserved", out)
	}
	if out[0].Flavor() != FlavorAskRule {
		t.Errorf("Flavor after round trip = %s, want ask-rule", out[0].Flavor())
	}
	old := UnmarshalDenials(`[{"tool":"Bash","command":"gradle help"}]`)
	if len(old) != 1 || old[0].ErrorText != "" || old[0].Flavor() != FlavorUnknown {
		t.Errorf("legacy JSON = %+v, want empty ErrorText, unknown flavor", old)
	}
}
