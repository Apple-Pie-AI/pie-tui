package approve

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

const prodAllowlist = "Edit Write Read Glob Grep MultiEdit TodoWrite " +
	"Bash(./gradlew:*) Bash(gradle:*) Bash(cd:*) Bash(chmod +x:*) " +
	"Bash(adb:*) Bash(java:*) Bash(ls:*)"

// T3.1 - the PLEX-61263 shape: the denied command matches pie's allowlist, so
// under the default policy it is approved with no human and no store row.
func TestDecideAutoApprovesAllowlisted(t *testing.T) {
	st := testStore(t)
	s := &Server{Ticket: "T", Allowed: prodAllowlist, Store: st,
		Poll: time.Millisecond}
	d := s.Decide("Bash", map[string]interface{}{
		"command": "cd Compass && ./gradlew :compass:compileAgentStagingDebugKotlin"})
	if d.Behavior != "allow" {
		t.Fatalf("decision = %+v, want allow", d)
	}
	if pending, _ := st.PendingApprovals(""); len(pending) != 0 {
		t.Errorf("auto-approve wrote a pending row: %+v", pending)
	}
}

// T3.2 - always-ask prompts even for allowlisted commands, and a human allow
// from the store resolves it.
func TestDecideAlwaysAskWaitsForHuman(t *testing.T) {
	st := testStore(t)
	s := &Server{Ticket: "T", Allowed: prodAllowlist, Policy: "always-ask",
		Store: st, Poll: time.Millisecond}
	go func() {
		for i := 0; i < 5000; i++ {
			time.Sleep(time.Millisecond)
			if pending, _ := st.PendingApprovals("T"); len(pending) == 1 {
				st.DecideApproval(pending[0].ID, store.ApprovalAllowed, false)
				return
			}
		}
	}()
	d := s.Decide("Bash", map[string]interface{}{"command": "ls -la"})
	if d.Behavior != "allow" {
		t.Fatalf("decision = %+v, want allow after the human's verdict", d)
	}
}

// T3.3 - a command outside the allowlist prompts, and a human deny denies.
func TestDecideHumanDeny(t *testing.T) {
	st := testStore(t)
	s := &Server{Ticket: "T", Allowed: prodAllowlist, Store: st,
		Poll: time.Millisecond}
	go func() {
		for i := 0; i < 5000; i++ {
			time.Sleep(time.Millisecond)
			if pending, _ := st.PendingApprovals("T"); len(pending) == 1 {
				if pending[0].Command != "rm -rf build" {
					t.Errorf("pending command = %q", pending[0].Command)
				}
				st.DecideApproval(pending[0].ID, store.ApprovalDenied, false)
				return
			}
		}
	}()
	d := s.Decide("Bash", map[string]interface{}{"command": "rm -rf build"})
	if d.Behavior != "deny" || !strings.Contains(d.Message, "dashboard") {
		t.Fatalf("decision = %+v, want dashboard deny", d)
	}
}

// There is NO timeout: an unanswered prompt waits for the human. The only
// structural exits are the asking session dying (ParentAlive) and the run
// being stopped (the row expired externally); both deny without pretending
// the human answered, and neither leaves a pending row behind.
func TestDecideWaitsUntilParentDies(t *testing.T) {
	st := testStore(t)
	calls := 0
	s := &Server{Ticket: "T", Allowed: "", Store: st,
		Poll: 5 * time.Millisecond,
		// Alive for a few polls - long past where the old 30ms timeout would
		// have fired - then the claude session "dies".
		ParentAlive: func() bool { calls++; return calls < 10 }}
	d := s.Decide("Bash", map[string]interface{}{"command": "./gradlew test"})
	if d.Behavior != "deny" || !strings.Contains(d.Message, "session that asked is gone") {
		t.Fatalf("decision = %+v, want the orphan deny", d)
	}
	if calls < 10 {
		t.Errorf("gave up after %d liveness checks - a deadline crept back in", calls)
	}
	if pending, _ := st.PendingApprovals(""); len(pending) != 0 {
		t.Errorf("row still pending after the parent died: %+v", pending)
	}
}

// A row expired externally (run stopped/restarted) resolves the wait as a
// deny that names the stop, not the human.
func TestDecideResolvesWhenRowExpiredExternally(t *testing.T) {
	st := testStore(t)
	s := &Server{Ticket: "T", Allowed: "", Store: st, Poll: time.Millisecond}
	go func() {
		for i := 0; i < 5000; i++ {
			time.Sleep(time.Millisecond)
			if pending, _ := st.PendingApprovals("T"); len(pending) == 1 {
				st.ExpirePendingApprovals("T")
				return
			}
		}
	}()
	d := s.Decide("Bash", map[string]interface{}{"command": "./gradlew test"})
	if d.Behavior != "deny" || !strings.Contains(d.Message, "was stopped") {
		t.Fatalf("decision = %+v, want the stopped-run deny", d)
	}
}

// ExpirePending (the stdin-closed janitor) expires exactly the questions this
// server opened and left pending.
func TestExpirePendingCleansOwnRows(t *testing.T) {
	st := testStore(t)
	s := &Server{Ticket: "T", Allowed: "", Store: st, Poll: time.Millisecond}
	go func() {
		for i := 0; i < 5000; i++ {
			time.Sleep(time.Millisecond)
			if pending, _ := st.PendingApprovals("T"); len(pending) == 1 {
				st.DecideApproval(pending[0].ID, store.ApprovalDenied, false)
				return
			}
		}
	}()
	s.Decide("Bash", map[string]interface{}{"command": "a"}) // resolved by the "human" above
	// A second question left truly pending, created behind Decide's back so
	// the server owns it without blocking the test:
	id, _ := st.CreateApproval("T", "Bash", "b")
	s.created = append(s.created, id)
	s.ExpirePending()
	if state, _ := st.ApprovalState(id); state != store.ApprovalExpired {
		t.Fatalf("state = %q, want the orphaned question expired on exit", state)
	}
	if pending, _ := st.PendingApprovals(""); len(pending) != 0 {
		t.Errorf("rows still pending after ExpirePending: %+v", pending)
	}
}

// T3.5 - the JSON-RPC surface: initialize, tools/list, tools/call round-trip,
// and the tool result carries the serialized decision.
func TestServeJSONRPC(t *testing.T) {
	s := &Server{Ticket: "T", Allowed: "Bash(ls:*)",
		Poll: time.Millisecond}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-01-01"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"approve","arguments":{"tool_name":"Bash","input":{"command":"ls -la"}}}}`,
	}, "\n") + "\n"
	var out strings.Builder
	if err := s.Serve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("replies = %d (%q), want 3 (notification consumed silently)", len(lines), out.String())
	}
	var init struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &init); err != nil ||
		init.Result.ProtocolVersion != "2025-01-01" || init.Result.ServerInfo.Name != "pie" {
		t.Errorf("initialize reply = %s (%v)", lines[0], err)
	}
	if !strings.Contains(lines[1], `"approve"`) {
		t.Errorf("tools/list reply = %s", lines[1])
	}
	var call struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &call); err != nil || len(call.Result.Content) != 1 {
		t.Fatalf("tools/call reply = %s (%v)", lines[2], err)
	}
	var dec Decision
	if err := json.Unmarshal([]byte(call.Result.Content[0].Text), &dec); err != nil || dec.Behavior != "allow" {
		t.Errorf("decision text = %q (%v), want allow (ls is allowlisted)", call.Result.Content[0].Text, err)
	}
}

// "Allow & remember" writes its rule to the config MID-session, and the
// server outlives that write: with Refresh set, the very next decision must
// see the new rule and auto-approve instead of prompting again.
func TestDecideRefreshPicksUpRememberedRules(t *testing.T) {
	st := testStore(t)
	allowed := "" // nothing allowlisted at spawn time
	s := &Server{Ticket: "T", Allowed: "STALE", Store: st,
		Poll:    time.Millisecond,
		Refresh: func() (string, string) { return allowed, "" }}

	// First decision: rule absent -> prompts; the human denies it.
	go func() {
		for i := 0; i < 5000; i++ {
			time.Sleep(time.Millisecond)
			if pending, _ := st.PendingApprovals("T"); len(pending) == 1 {
				st.DecideApproval(pending[0].ID, store.ApprovalDenied, false)
				return
			}
		}
	}()
	d := s.Decide("Bash", map[string]interface{}{"command": "./gradlew test"})
	if d.Behavior != "deny" {
		t.Fatalf("first decision = %+v, want the human's deny", d)
	}

	// The human pressed Allow & remember elsewhere: the config now carries
	// the rule, and the very next decision must see it without a respawn.
	allowed = "Bash(./gradlew:*)"
	d = s.Decide("Bash", map[string]interface{}{"command": "./gradlew test"})
	if d.Behavior != "allow" {
		t.Fatalf("post-remember decision = %+v, want auto-allow via refreshed config", d)
	}
	if pending, _ := st.PendingApprovals("T"); len(pending) != 0 {
		t.Errorf("post-remember decision still prompted: %+v", pending)
	}
}

// The no-timeout guarantee's tripwire: the human takes their time (far past
// any deadline a regression would plausibly reintroduce) and the wait must
// still end in THEIR verdict, not an expiry made on their behalf.
func TestDecideOutwaitsSlowHuman(t *testing.T) {
	st := testStore(t)
	s := &Server{Ticket: "T", Allowed: "", Store: st, Poll: 5 * time.Millisecond}
	go func() {
		time.Sleep(400 * time.Millisecond) // slower than any crept-in deadline
		for i := 0; i < 5000; i++ {
			if pending, _ := st.PendingApprovals("T"); len(pending) == 1 {
				st.DecideApproval(pending[0].ID, store.ApprovalAllowed, false)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	d := s.Decide("Bash", map[string]interface{}{"command": "./gradlew test"})
	if d.Behavior != "allow" {
		t.Fatalf("decision = %+v, want the slow human's allow - nothing may decide for them", d)
	}
}
