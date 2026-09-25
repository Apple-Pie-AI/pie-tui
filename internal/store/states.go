// The lifecycle vocabulary: the states a ticket and the shared emulator move
// through (Decision 9). Everything else in the tree compares against these.
package store

// Lifecycle states (Decision 9).
const (
	StateQueued    = "queued"
	StatePlanning  = "planning"
	StateAwaiting  = "awaiting-answer"
	StateWorking   = "working"
	StateReviewing = "reviewing"
	StateBuilding  = "building"
	StateTesting   = "testing"
	StateReview    = "review"
	StateNeedsYou  = "needs-you"
	StateFailed    = "failed"
	// StatePlanReview is the opt-in pause after the plan stage: the plan is ready
	// but the human hasn't approved it yet. It's an idle state (NOT in IsActive)
	// so the driver process has exited and a re-run is allowed, like awaiting-answer.
	StatePlanReview = "plan-review"
	// StateFixReview is the pause after a comment-fix run: the agent has edited
	// the worktree and drafted its replies, but nothing is committed, pushed, or
	// posted until the human approves in the dashboard. Idle for the same reason
	// as plan-review: the driver has exited, and both the approve run
	// (--ship-comments) and a revise round (--address-comments again) are
	// legitimate next drivers.
	StateFixReview = "fix-review"
	// StateChangeReview is the review-before-PR gate: the ticket's change is
	// built and verified locally, but nothing is committed, pushed, or opened
	// as a PR until the human reviews it in the change screen and approves.
	// Idle for the same reason as plan-review and fix-review: the driver has
	// exited, and the approve run (--ship-change) and a feedback rework
	// (--rework) are both legitimate next drivers.
	StateChangeReview = "change-review"
	// Terminal post-review states set when the PR is resolved (Decision 7 cleanup).
	StateMerged = "merged"
	StateClosed = "closed"
	// StateCheckedOut is a branch checked out into a pie worktree by hand from
	// the hub - no agent has run and none is running. Idle on purpose: it is
	// never in IsActive, so a later `pie run` on the ticket is allowed and
	// resets it to queued. A checked-out branch WITH a pull request never
	// carries this state - it goes straight to review, which is what wires it
	// into comment polling and merge detection.
	StateCheckedOut = "checked-out"

	// StateStopped is set when a session is manually cancelled from the monitor
	// (process killed + worktree removed).
	StateStopped = "stopped"
)

// IsActive reports whether state means a run is actively in progress (a driver
// process should be alive). Terminal/idle states - review, needs-you, failed,
// merged, closed, stopped, awaiting-answer - return false: the driving process
// has already exited, so its recorded PID is stale and a new run is allowed.
func IsActive(state string) bool {
	switch state {
	case StateQueued, StatePlanning, StateWorking, StateReviewing, StateBuilding, StateTesting:
		return true
	}
	return false
}

// Emulator lifecycle states.
const (
	EmulatorIdle    = "idle"
	EmulatorBooting = "booting"
	EmulatorReady   = "ready"
	EmulatorBusy    = "busy"
)
