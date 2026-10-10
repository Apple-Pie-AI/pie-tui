// The pipeline's vocabulary: what one ticket is (Task), how a caller observes it
// (Hooks), and the three ways a run can end (Outcome, needsYou, stopped).
//
// Split out of runner.go, which was carrying both this and the staged Run past
// the repo's ~400-line-per-file line. The seam is real: nothing here knows the
// order of the stages, and Run is nothing but that order.
package runner

import (
	"context"
	"fmt"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Task is one ticket to process.
type Task struct {
	Ticket      string
	Summary     string
	Description string
	Repo        *config.Repo
	Cfg         *config.Config
	DryRun      bool
	Resume      bool         // verify & ship: continue from the existing worktree, no re-plan/implement
	Ship        bool         // ship without verifying: trust the human's fix, commit + push + PR directly
	Store       *store.Store // optional; enables emulator coordination
	Images      []string     // image files to make the agent see (pasted-content flow)
	Base        string       // bare base branch name for stacked diffs; "" = default
	Branch      string       // exact branch name (user-confirmed at creation); "" = derive from the repo pattern
	FromBranch  bool         // Branch names an EXISTING branch to work on: checkout as-is, never reset (PR feedback, agent code review)
	ReviewPlan  bool         // pause after the plan stage for human approval (plan-review gate)
	FromPlan    bool         // skip planning: implement straight from the existing plan.json
	// AddressComments applies the PR review comments the hub queued in the store
	// to the existing worktree - LOCALLY only. The run stops at fix-review with
	// drafted replies; nothing is committed, pushed, or posted until the human
	// approves (ShipComments). The selection is not carried here because the hub
	// spawns this detached: the store is the only channel between the two
	// processes.
	AddressComments bool
	// CommentFeedback is the human's correction from the approve screen. Only
	// meaningful with AddressComments: the run resumes the fix session with
	// this feedback instead of starting a fresh fix.
	CommentFeedback string
	// ShipComments is the approved second half of AddressComments: verify the
	// local fixes, commit, push, and post the (possibly human-edited) replies.
	ShipComments bool
	// ReviewChange parks the pipeline after a green verify so the human
	// reviews the local change before any PR exists (the change-review gate).
	ReviewChange bool
	// ShipChange is the gate's approve run: apply pending reverts, re-verify
	// if the change moved since the park, then commit/push/PR.
	ShipChange bool
	// Rework resumes the ticket's session with the human's notes from the
	// change screen (ReworkFeedback, serialized by the hub), re-verifies, and
	// re-parks at the gate as the next round.
	Rework         bool
	ReworkFeedback string
	// Discuss is the gate's Plan-mode round: resume the session under
	// --permission-mode plan with the change screen's queued message (read
	// from ChangeNotes, the same field Rework's feedback reads - Plan mode and
	// a rework's feedback are never both pending at once), reply, and return
	// to change-review - no edits, no re-verify, no round bump.
	Discuss bool
	// PriorState is the session's state as the launcher found it, captured
	// before its immediate reset to `queued`. The ship-comments gate reads it:
	// the gate's question is "was this ticket parked at fix-review when the
	// ship was requested", and the launcher's reset - which exists so a re-run
	// clears a stale terminal state right away - destroys the row's own answer
	// before the pipeline can read it. "" means no launcher was involved (or an
	// old caller); the gate then falls back to the row.
	PriorState string
	// PriorFlow is the session's parked_flow as the launcher found it, captured
	// (like PriorState) before the reset clears it. It lets the ship-comments
	// gate recognize a parked retry: a needs-you whose park came from
	// ship-comments HAS been human-approved, and bouncing it to "approve first"
	// forced users to re-approve work they already approved.
	PriorFlow string
	// AnthropicKey is supplied by the caller, because cmd owns the keychain - the
	// same inversion that keeps Jira behind Hooks.Comment and telemetry consent
	// out of the pipeline entirely. Empty means the agent inherits whatever
	// ANTHROPIC_API_KEY is already in the environment.
	AnthropicKey string
}

// flowFor names the flow a Task runs, for the parked_flow record retries read.
func flowFor(t Task) string {
	switch {
	case t.ShipComments:
		return "ship-comments"
	case t.AddressComments:
		return "address-comments"
	case t.ShipChange:
		return "ship-change"
	case t.Rework:
		return "rework-change"
	case t.Discuss:
		return "discuss-change"
	default:
		return ""
	}
}

// logFn writes a progress line to the ticket's session log.
type logFn func(format string, args ...interface{})

// stateFn records a lifecycle transition (Decision 9).
type stateFn func(state string, retries int)

// Hooks let the caller observe and react. Any field may be nil.
type Hooks struct {
	Logf    logFn                             // progress lines
	OnState stateFn                           // lifecycle transitions
	OnField func(branch, worktree, pr string) // metadata as it's known
	Comment func(text string)                 // post back to the ticket
}

// logf returns Logf, or a no-op when the caller wired none, so the pipeline can
// log unconditionally. Each entrypoint used to re-derive this guard by hand.
func (h Hooks) logf() logFn {
	if h.Logf == nil {
		return func(string, ...interface{}) {}
	}
	return h.Logf
}

// setState returns a non-nil lifecycle-transition sink.
func (h Hooks) setState() stateFn {
	return func(state string, retries int) {
		if h.OnState != nil {
			h.OnState(state, retries)
		}
	}
}

// comment posts text back to the ticket when the caller supplied a sink. Jira
// I/O is inverted through this hook, which is what lets local .md tickets work
// with no Jira configuration and no branching inside the pipeline.
func (h Hooks) comment(text string) {
	if h.Comment != nil {
		h.Comment(text)
	}
}

// needsYou stops the ticket for a human: it logs the reason, records the
// transition, and posts the explanation back to the ticket. Six sites in the
// pipeline end exactly this way and each used to repeat these four lines.
func needsYou(t Task, h Hooks, logLine, explanation string) Outcome {
	h.logf()("[%s] needs-you: %s", t.Ticket, logLine)
	h.setState()(store.StateNeedsYou, 0)
	h.comment(fmt.Sprintf(
		"🤖 *Apple Pie needs your help on [%s]*\n\n%s\n\nCheck the session log: `pie logs %s`",
		t.Ticket, explanation, t.Ticket))
	return Outcome{State: store.StateNeedsYou}
}

// apiNeedsYou stops the ticket when a stage died on an API-level error
// (budget, auth, availability): the real error verbatim, never a guess about
// the ticket or the build. Four stages end here (plan, implement, verify,
// resume-verify).
func apiNeedsYou(t Task, h Hooks, stage, errText string) Outcome {
	if isBudgetStop(errText) {
		return budgetNeedsYou(t, h, stage, errText)
	}
	return needsYou(t, h,
		fmt.Sprintf("%s stage died on an API error: %s", stage, oneLine(errText, 160)),
		fmt.Sprintf("The %s stage could not run - the Claude API rejected the request:\n\n{code}\n%s\n{code}\n\nThis is an account/API problem (budget, auth, or availability), not a problem with the ticket or the change. Fix that, then re-run the ticket.", stage, errText))
}

// Outcome is the terminal result.
type Outcome struct {
	State string // review | needs-you | failed | stopped
	PRURL string
	Err   error
}

// stopped reports whether the run was cancelled, and supplies the Outcome to
// return when it was. It deliberately does NOT record a lifecycle state:
// whoever cancelled owns that write. The TUI's Pause sets NEEDS YOU and its
// Stop sets STOPPED, each immediately after signalling the process - a state
// write on the way out of the pipeline would race them and clobber the more
// specific one with a generic "stopped".
func stopped(ctx context.Context, t Task, logf logFn) (Outcome, bool) {
	if ctx.Err() == nil {
		return Outcome{}, false
	}
	logf("[%s] cancelled - stopping before the next stage", t.Ticket)
	return Outcome{State: store.StateStopped, Err: ctx.Err()}, true
}
