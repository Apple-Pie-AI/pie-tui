package agent

import (
	"strings"
	"testing"
)

// budgetResultEvent is the result line claude 2.1.295 printed for a session
// that hit --max-budget-usd, trimmed of fields the parser ignores. Note what
// it does NOT carry: a "result" text - which is why budget stops used to be
// invisible (parseStream only read errors out of that field).
const budgetResultEvent = `{"type":"result","subtype":"error_max_budget_usd","is_error":true,` +
	`"terminal_reason":"budget_exhausted","total_cost_usd":0.00400031,"num_turns":1,` +
	`"session_id":"18482888-7900-4f9f-8e51-85405cb1a38c","permission_denials":[],` +
	`"errors":["Reached maximum budget ($0.001)"]}`

func parse(t *testing.T, stream string) Result {
	t.Helper()
	return parseStream(strings.NewReader(stream), func(string, ...interface{}) {}, nil, nil, nil, nil, nil)
}

// The live-captured budget stop is recognized: flagged, costed, and given an
// ErrorText the stages can name - before this, ErrorText stayed empty and the
// stage blamed the ticket ("too ambiguous") or the build ("no report").
func TestParseStreamBudgetStop(t *testing.T) {
	res := parse(t, `{"type":"system","subtype":"init","session_id":"18482888-7900-4f9f-8e51-85405cb1a38c"}`+"\n"+budgetResultEvent+"\n")
	if !res.BudgetExceeded {
		t.Fatal("BudgetExceeded = false for an error_max_budget_usd result")
	}
	if res.ErrorText != "Reached maximum budget ($0.001)" {
		t.Errorf("ErrorText = %q, want the CLI's own budget message", res.ErrorText)
	}
	if res.CostUSD != 0.00400031 {
		t.Errorf("CostUSD = %v, want 0.00400031", res.CostUSD)
	}
	if res.SessionID != "18482888-7900-4f9f-8e51-85405cb1a38c" {
		t.Errorf("SessionID = %q - the runner needs it to resume", res.SessionID)
	}
	if !strings.HasPrefix(res.ErrorText, BudgetExceededPrefix) {
		t.Errorf("ErrorText %q must start with BudgetExceededPrefix - the runner's park routing keys on it", res.ErrorText)
	}
}

// A budget result with no errors array still produces a usable ErrorText, and
// terminal_reason alone is enough to recognize it (the subtype could be renamed
// by a future CLI before the reason is).
func TestParseStreamBudgetStopFallbacks(t *testing.T) {
	res := parse(t, `{"type":"result","subtype":"error_max_budget_usd","is_error":true,"session_id":"s"}`+"\n")
	if !res.BudgetExceeded || res.ErrorText != BudgetExceededPrefix {
		t.Errorf("no errors[]: BudgetExceeded=%v ErrorText=%q", res.BudgetExceeded, res.ErrorText)
	}
	res = parse(t, `{"type":"result","subtype":"error_something_new","terminal_reason":"budget_exhausted","is_error":true,"session_id":"s"}`+"\n")
	if !res.BudgetExceeded {
		t.Error("terminal_reason budget_exhausted alone should mark a budget stop")
	}
}

// Normal and API-error results are untouched: no budget flag, cost recorded,
// and an API error keeps its own text (it parks differently).
func TestParseStreamNotABudgetStop(t *testing.T) {
	res := parse(t, `{"type":"result","subtype":"success","result":"done","total_cost_usd":1.25,"session_id":"s"}`+"\n")
	if res.BudgetExceeded || res.ErrorText != "" {
		t.Errorf("success: BudgetExceeded=%v ErrorText=%q", res.BudgetExceeded, res.ErrorText)
	}
	if res.CostUSD != 1.25 {
		t.Errorf("CostUSD = %v, want 1.25", res.CostUSD)
	}
	res = parse(t, `{"type":"result","subtype":"error_during_execution","is_error":true,`+
		`"result":"API Error: Request rejected (429)","session_id":"s"}`+"\n")
	if res.BudgetExceeded {
		t.Error("an API 429 is not a --max-budget-usd stop")
	}
	if res.ErrorText != "API Error: Request rejected (429)" {
		t.Errorf("API error text = %q", res.ErrorText)
	}
}

// The ceiling reaches the CLI, and a resumed run carries both the session and
// its fresh ceiling - the pair a budget continuation depends on.
func TestBuildArgsBudgetAndResume(t *testing.T) {
	args := strings.Join(buildArgs("p", Options{MaxBudgetUSD: 2.5, ResumeID: "sess-1"}), " ")
	for _, want := range []string{"--max-budget-usd 2.5", "--resume sess-1"} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	if strings.Contains(strings.Join(buildArgs("p", Options{}), " "), "--max-budget-usd") {
		t.Error("a zero budget must omit the flag (unlimited), not pass 0")
	}
}
