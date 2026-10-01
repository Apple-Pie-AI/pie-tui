// The hub's action vocabulary. Menu rows (agentActions, paletteItems) and
// keyboard shortcuts (dispatchKey) both name an action instead of calling the
// handler directly, which is what keeps keys and clicks in sync - and is why the
// names themselves need to be a closed set rather than free-floating strings.
package tui

// actionID names one thing the hub can do. It is a distinct type so the three
// places that produce ids and the one place that consumes them share a
// vocabulary the compiler helps enforce: `paletteItem{actStudio, …}` cannot be
// spelled `actStduio`. A bare string literal still converts implicitly (Go's
// untyped constants), so doAction also carries a default arm - together they
// close the hole where a typo produced a menu row that did nothing at all.
type actionID string

const (
	// ---- per-agent ----
	actAnswer       actionID = "answer"
	actApprove      actionID = "approve-prompt"
	actViewPlan     actionID = "view-plan"
	actPlanMD       actionID = "plan-md"
	actApprovePlan  actionID = "approve-plan"
	actPlanFeedback actionID = "plan-feedback"
	actStudio       actionID = "studio"
	actClaude       actionID = "claude"
	actResume       actionID = "resume"
	actAllowRerun   actionID = "allow-rerun"
	actShip         actionID = "ship"
	actRerun        actionID = "rerun"
	actPause        actionID = "pause"
	actStop         actionID = "stop"

	actOpenPR        actionID = "open-pr"
	actViewComments  actionID = "view-comments"
	actFetchComments actionID = "fetch-comments"
	actViewChange    actionID = "view-change" // the review-before-PR screen

	// ---- global ----
	actRun           actionID = "run"
	actRunFromBranch actionID = "run-from-branch"
	actDetectPR      actionID = "detect-pr"
	actDoctor        actionID = "doctor"
	actConfig        actionID = "config"
	actModels        actionID = "models"
	actSetup         actionID = "setup"
	actDaemonStart   actionID = "daemon-start"
	actDaemonStop    actionID = "daemon-stop"
	actMenu          actionID = "menu"       // the ":" command menu: agent actions + globals
	actAgentMenu     actionID = "agent-menu" // Enter on a row: that agent's actions only
	actRefresh       actionID = "refresh"
	actQuit          actionID = "quit"
)

// allActions is every declared action. It exists so a test can assert that each
// one has an arm in doAction, which is a stronger guarantee than walking the
// menus: a menu only emits an action under the right session state and daemon
// state, so a walk quietly skips the ones it fails to provoke.
//
// Adding a constant above without adding it here is itself caught, by the count
// check in TestEveryActionIsHandled.
var allActions = []actionID{
	actAnswer, actApprove, actViewPlan, actPlanMD, actApprovePlan, actPlanFeedback,
	actStudio, actClaude, actResume, actAllowRerun, actShip, actRerun, actPause, actStop,
	actOpenPR, actViewComments, actFetchComments, actViewChange,
	actRun, actRunFromBranch, actDetectPR, actDoctor, actConfig, actModels, actSetup, actDaemonStart, actDaemonStop,
	actMenu, actAgentMenu, actRefresh, actQuit,
}
