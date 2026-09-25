// Package approve is pie's permission-prompt handler: a minimal stdio MCP
// server that claude spawns (via --permission-prompt-tool mcp__pie__approve)
// whenever a headless run hits an ask rule or an allowlist gap. Instead of
// the CLI hard-denying with no human to ask, the question lands here:
// commands the user already allowlisted in pie are approved on the spot, and
// everything else becomes a pending row in the SQLite store that the TUI
// shows the human - allow/deny by keystroke, the agent continues in-session.
//
// The protocol surface is deliberately tiny (initialize, tools/list,
// tools/call over line-delimited JSON-RPC) - not worth an SDK dependency for
// three methods, matching the repo's shell-out-over-wrap taste.
package approve

import (
	"encoding/json"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Server answers permission questions for one ticket's agent session.
//
// There is deliberately NO timeout on a pending question: pie never answers
// a permission prompt on the user's behalf - an unanswered prompt waits
// until the human decides, however long that takes. The user drives every
// grant and refusal, and owns the consequences; a timeout-deny would be the
// tool acting behind their intentions. The only exits besides a human
// verdict are structural: the claude session that asked dies (ParentAlive),
// or the run is stopped/restarted (the TUI and launcher expire the ticket's
// pending prompts).
type Server struct {
	Ticket  string
	Allowed string        // EffectiveAllowedTools() - the auto-approve set
	Policy  string        // "" / "auto" = auto-approve allowlisted; "always-ask" = never auto
	Poll    time.Duration // store poll cadence (tests shrink it)
	Store   *store.Store
	Logf    func(format string, args ...interface{})
	// Refresh, when set, re-reads (allowed, policy) before each decision. The
	// server lives as long as its claude session, and "Allow & remember"
	// writes its rule to the config MID-session - without a refresh, the
	// remembered shape kept prompting until the next stage spawned a fresh
	// server with a fresh config load.
	Refresh func() (allowed, policy string)
	// ParentAlive reports whether the claude session this server answers for
	// is still running. When it dies mid-wait (crash, kill), the pending row
	// is expired and the poll loop exits instead of leaking a process that
	// waits forever for a question nobody can see anymore. nil = always alive.
	ParentAlive func() bool

	created []int64 // pending rows this server opened, for orphan cleanup
}

// Decision is the verdict payload Claude Code expects from a permission
// prompt tool, serialized as the tool result's text content.
type Decision struct {
	Behavior     string                 `json:"behavior"` // "allow" | "deny"
	UpdatedInput map[string]interface{} `json:"updatedInput,omitempty"`
	Message      string                 `json:"message,omitempty"`
}

// Decide answers one permission question. It never returns an error - a
// broken store or timeout degrades to a deny with a reason, which flows into
// the existing denial/park path instead of wedging the agent.
func (s *Server) Decide(tool string, input map[string]interface{}) Decision {
	command, _ := input["command"].(string)
	allow := Decision{Behavior: "allow", UpdatedInput: input}

	if s.Refresh != nil {
		s.Allowed, s.Policy = s.Refresh()
	}
	if s.Policy != "always-ask" && agent.AllowlistPermits(s.Allowed, tool, command) {
		s.logf("approve: auto-approved by pie allowlist: %s(%s)", tool, command)
		return allow
	}
	if s.Store == nil {
		return Decision{Behavior: "deny", Message: "pie could not reach its approval store"}
	}
	id, err := s.Store.CreateApproval(s.Ticket, tool, command)
	if err != nil {
		return Decision{Behavior: "deny", Message: "pie could not record the approval request: " + err.Error()}
	}
	s.created = append(s.created, id)
	s.logf("approve: awaiting human decision in the pie dashboard: %s(%s)", tool, command)
	poll := s.Poll
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	// No deadline: the wait ends only with a human verdict or the death of
	// the session that asked (see the Server doc comment).
	for {
		time.Sleep(poll)
		switch state, err := s.Store.ApprovalState(id); {
		case err != nil:
			return Decision{Behavior: "deny", Message: "pie lost its approval store: " + err.Error()}
		case state == store.ApprovalAllowed:
			s.logf("approve: allowed from the dashboard: %s(%s)", tool, command)
			return allow
		case state == store.ApprovalDenied:
			// agent.Flavor classifies parks by these exact strings.
			return Decision{Behavior: "deny", Message: agent.CallbackDeniedText}
		case state == store.ApprovalExpired:
			// The run was stopped or restarted out from under the prompt.
			return Decision{Behavior: "deny", Message: "the run this approval belonged to was stopped"}
		}
		if s.ParentAlive != nil && !s.ParentAlive() {
			_ = s.Store.DecideApproval(id, store.ApprovalExpired, false)
			return Decision{Behavior: "deny", Message: "the claude session that asked is gone"}
		}
	}
}

// ExpirePending expires any question this server opened that is still
// pending - called when the session's stdin closes (claude exited), so a
// dead session never leaves a prompt on the dashboard that no agent is
// waiting behind.
func (s *Server) ExpirePending() {
	if s.Store == nil {
		return
	}
	for _, id := range s.created {
		if state, err := s.Store.ApprovalState(id); err == nil && state == store.ApprovalPending {
			_ = s.Store.DecideApproval(id, store.ApprovalExpired, false)
		}
	}
}

func (s *Server) logf(format string, args ...interface{}) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

// decisionText serializes a Decision the way the CLI reads it back: as the
// text content of the tool result.
func decisionText(d Decision) string {
	b, err := json.Marshal(d)
	if err != nil {
		return `{"behavior":"deny","message":"pie could not encode its decision"}`
	}
	return string(b)
}
