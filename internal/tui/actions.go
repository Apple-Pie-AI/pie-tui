package tui

import (
	"fmt"
	"os/exec"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// runArg is the argument `pie run` needs to re-resolve a session. Local
// tickets must be re-run from their .md file - the ticket id is only a derived
// label, and passing it instead makes run(1) fall through to the Jira path and
// fail. Every caller that spawns a run must go through this.
func runArg(s store.Session) string {
	if s.SourcePath != "" {
		return s.SourcePath
	}
	return s.Ticket
}

// agentActions returns the menu items that make sense for one agent's state and
// whether its worktree still exists. Shown when the user presses Enter on it.
func agentActions(s store.Session) []paletteItem {
	var items []paletteItem
	hasWT := worktreeExists(s.Repo, s.Ticket)
	active := store.IsActive(s.State)
	stuck := s.State == "needs-you" || s.State == "failed" || s.State == store.StateStopped || isStopped(s)
	done := s.State == "review" || s.State == "merged" || s.State == "closed"

	// A hand checkout leads with the change screen - review the branch's
	// local changes, annotate them, have the agent rework them, or ship a PR
	// from it. Checking for an existing PR is the secondary forward action.
	if s.State == store.StateCheckedOut {
		items = append(items,
			paletteItem{actViewChange, "Review and make changes",
				"read the diff, give feedback or revert files, then create the PR"},
			paletteItem{actDetectPR, "Check for a pull request",
				"ask GitHub whether this branch has a PR; if so, track its review"})
	}
	if s.State == "awaiting-answer" {
		items = append(items, paletteItem{actAnswer, "Answer the questions", "reply, then the agent resumes"})
	}
	// Plan-review is an idle state (neither active nor stuck): the plan is ready
	// and the human approves it or sends it back for a re-plan.
	if s.State == store.StatePlanReview {
		items = append(items,
			paletteItem{actViewPlan, "View the plan", "read the full plan in a scrollable view"},
			paletteItem{actApprovePlan, "Approve plan & implement", "continue: implement → verify → PR"},
			paletteItem{actPlanFeedback, "Give feedback & re-plan", "tell it what to change; it plans again"},
		)
	}
	// Fix-review: the local fixes and drafted replies are waiting for approval.
	if s.State == store.StateFixReview {
		items = append(items, paletteItem{actViewComments, "Review the local fixes",
			"read the diff and the draft replies, then approve or ask for changes"})
	}
	// Change-review: the verified change is waiting for its review-before-PR.
	if s.State == store.StateChangeReview {
		hint := "read the diff, give feedback or revert files, then create the PR"
		if s.PRURL != "" {
			hint = "read the diff, give feedback or revert files, then push to the PR"
		}
		items = append(items, paletteItem{actViewChange, "Review and make changes", hint})
	}
	// Any other idle row with a worktree is just as reviewable - a needs-you
	// park, a failed run, an open PR: the screen is the row's most important
	// action, so it leads there too. (--resume and --ship were always
	// available ungated from these states; the screen is a stricter path,
	// not a new exposure.) A PR with open review comments is the exception:
	// addressing what the reviewers actually asked for is the row's real
	// reason for being red, so it leads instead - "Review and make changes"
	// (the local diff, independent of any PR comments) follows right behind.
	addressedFeedbackLeads := false
	if hasWT && (stuck || s.State == store.StateReview) {
		hint := "read the local diff, give feedback or revert files, then create the PR"
		if s.PRURL != "" {
			hint = "read the local diff, give feedback or revert files, then push to the PR"
		}
		lead := []paletteItem{{actViewChange, "Review and make changes", hint}}
		if s.State == store.StateReview && s.PRURL != "" && s.OpenComments > 0 {
			lead = append([]paletteItem{{actViewComments,
				fmt.Sprintf("Address PR feedback (%d)", s.OpenComments),
				"read what the reviewers asked for and fix it"}}, lead...)
			addressedFeedbackLeads = true
		}
		items = append(lead, items...)
	}
	// A PR that is open and has been reviewed: reading the comments is the whole
	// reason this row is red, so it leads - unless the block above already put
	// it ahead of "Review and make changes".
	if s.State == store.StateReview && s.PRURL != "" {
		if s.OpenComments > 0 && !addressedFeedbackLeads {
			items = append(items, paletteItem{actViewComments,
				fmt.Sprintf("Address PR feedback (%d)", s.OpenComments),
				"read what the reviewers asked for and fix it"})
		}
		items = append(items, paletteItem{actOpenPR, "Open the pull request",
			"open it in the browser"})
		items = append(items, paletteItem{actFetchComments, "Check for PR updates",
			"fetch for new comments or status updates from Github"})
	}
	// Parked at fix-review the PR is just as open: the human may have resolved
	// a queued thread on GitHub while deciding, and this is how they tell pie
	// to notice without waiting for the background poll (ticket 2).
	if s.State == store.StateFixReview && s.PRURL != "" {
		items = append(items, paletteItem{actFetchComments, "Check for PR updates",
			"drop threads already resolved on GitHub from the batch"})
	}
	// A resolved PR is still worth reading - the link outlives the review. The
	// comment actions stay review-only: there is nothing left to address.
	if (s.State == store.StateMerged || s.State == store.StateClosed) && s.PRURL != "" {
		items = append(items, paletteItem{actOpenPR, "Open the pull request",
			"open it in the browser"})
	}
	// The plan .md (archived in ~/.pie/plans/, or derivable from the
	// worktree) opens externally in whatever handles markdown.
	if planAvailable(s) {
		items = append(items, paletteItem{actPlanMD, "View Plan", "open the plan .md file"})
	}
	// Pause a live run: stop the agent but keep the worktree, dropping the ticket
	// to NEEDS YOU so you can take over by hand (then Resume / Open a PR).
	if active {
		items = append(items, paletteItem{actPause, "Pause (move to NEEDS YOU)", "stop the running agent but keep the worktree so you can take over"})
	}
	if hasWT {
		// A run blocked by the allowlist gets its one-keystroke fix FIRST - it is
		// almost always the right action, and everything else on this menu is a
		// detour for that failure mode. Offered only when allowing would actually
		// change something (non-empty rule suggestions).
		if stuck && allowRerunAvailable(s) {
			items = append(items, paletteItem{actAllowRerun, "Allow denied commands & re-run",
				"add them to extra_allowed_tools and re-verify"})
		}
		// Order most-used first: hand-off (Claude / Studio), then Open a PR, Stop,
		// and finally Resume (the least common recovery path).
		// "Open in Claude Code" resumes the agent's session. Only offer it when no
		// run is active - otherwise the headless agent and this interactive session
		// would both write to the same session on disk and diverge.
		if !active {
			items = append(items, paletteItem{actClaude, "Open in Claude Code", "resume the agent's session in a new terminal"})
		}
		items = append(items, paletteItem{actStudio, "Open Android Studio", "open the worktree as a project"})
		if stuck {
			items = append(items, paletteItem{actShip, "Open a PR", "skip verification - commit + push + PR (when the gate itself is unreliable here)"})
		}
	} else if stuck || done {
		// Worktree is gone — only a fresh run is possible.
		items = append(items, paletteItem{actRerun, "Run again (fresh)", "no worktree left - start this ticket from scratch"})
	}
	// Stop is available for live/stuck rows (kills the process, removes the
	// worktree, and clears it into STOPPED) - but not for finished or already-
	// stopped rows.
	if !done && s.State != store.StateStopped {
		items = append(items, paletteItem{actStop, "Stop & clean up", "kill the process and remove the worktree"})
	}
	// Resume re-runs verification on a hand-fixed worktree; least-common, so last.
	if hasWT && stuck {
		items = append(items, paletteItem{actResume, "Resume from last stage", "re-run verification on your fix, then open the PR"})
	}
	return items
}

// doAction runs an action by id. The Enter menu and the keyboard shortcuts both
// route here so they stay in sync. Every arm is an actionID constant (see
// actionid.go), and the default arm makes an id nothing handles visible instead
// of letting the menu row do nothing at all.
func (m monitorModel) doAction(id actionID) (tea.Model, tea.Cmd) {
	switch id {
	// --- per-agent ---
	case actAnswer:
		if s := m.selected(); s != nil && s.State == "awaiting-answer" {
			m.answer = answerState{open: true, ticket: s.Ticket}
			m.notice = ""
		}
	case actApprove:
		if s := m.selected(); s != nil && m.approvalsFor(s.Ticket) > 0 {
			m.openApprovals(s.Ticket)
		}
	case actViewPlan:
		if s := m.selected(); s != nil {
			return m.openPlanView(*s)
		}
	case actPlanMD:
		if s := m.selected(); s != nil {
			m.notice = "opening plan for " + s.Ticket + "…"
			return m, openPlanMD(*s)
		}
	case actApprovePlan:
		if s := m.selected(); s != nil {
			m.view = viewDashboard // leave the plan viewer; the run resumes now
			m.notice = "approving plan for " + s.Ticket + " - implementing…"
			return m, m.spawnRun(runArg(*s), "--from-plan")
		}
	case actPlanFeedback:
		// Feedback lives inside the full-screen plan viewer so the plan stays
		// visible while the user types corrections.
		if s := m.selected(); s != nil {
			return m.openPlanFeedback(*s)
		}
	case actOpenPR:
		if s := m.selected(); s != nil && s.PRURL != "" {
			return m, openCommentURL(s.PRURL)
		}
	case actViewComments:
		if s := m.selected(); s != nil {
			return m.openCommentsView(*s)
		}
	case actViewChange:
		if s := m.selected(); s != nil {
			return m.openChangeView(*s)
		}
	case actFetchComments:
		if s := m.selected(); s != nil {
			cmd := m.fetchComments(*s, true) // before setting fetching; see updateComments
			if cmd != nil {
				m.comments.fetching = true
				m.notice = "checking GitHub for PR updates on " + s.Ticket + "…"
			}
			return m, cmd
		}
	case actStudio:
		if s := m.selected(); s != nil {
			wt := git.ProjectDirIn(paths.WorktreeFor(s.Repo, s.Ticket), s.Repo)
			// Prefer the JetBrains `studio` launcher (opens the dir as a project);
			// fall back to the macOS app.
			if _, err := exec.LookPath("studio"); err == nil {
				_ = exec.Command("studio", wt).Start()
			} else {
				_ = exec.Command("open", "-a", "Android Studio", wt).Start()
			}
			m.notice = "opening Android Studio in the worktree…"
		}
	case actClaude:
		if s := m.selected(); s != nil {
			return m, m.openClaude(*s)
		}
	case actResume:
		if s := m.selected(); s != nil {
			m.notice = "resuming " + s.Ticket + " from the last stage…"
			return m, m.spawnRun(retryArgs(*s)...)
		}
	case actAllowRerun:
		if s := m.selected(); s != nil {
			return m.doAllowRerun(*s)
		}
	case actShip:
		if s := m.selected(); s != nil {
			m.notice = "opening a PR for " + s.Ticket + " without re-verifying…"
			return m, m.spawnRun(runArg(*s), "--ship")
		}
	case actRerun:
		if s := m.selected(); s != nil {
			m.notice = "starting " + s.Ticket + " fresh…"
			return m, m.spawnRun(runArg(*s))
		}
	case actPause:
		if s := m.selected(); s != nil {
			m.notice = "pausing " + s.Ticket + " - moving to NEEDS YOU…"
			return m, m.pauseAgent(*s)
		}
	case actStop:
		if s := m.selected(); s != nil {
			m.confirm = confirmState{ticket: s.Ticket} // cursor 0 = "Yes, stop"
			m.notice = ""
		}
	// --- global ---
	case actRun:
		// One reset, not a hand-listed subset. This site cleared nine of the
		// wizard's fields and left runStackPrompt armed, so re-entering "Start
		// new" after backing out of the stack picker reopened it on a chip that
		// no longer existed.
		m.run.reset()
		m.notice = ""
		// With a single input method (no Jira configured) the picker is just an
		// extra keystroke - jump straight into that method's input.
		if methods := m.runMethods(); len(methods) == 1 {
			m.run.mode = methods[0].mode
			m.view = viewRunInput
			if m.run.mode == "content" {
				m = m.withContentEditor()
				return m, textarea.Blink
			}
		} else {
			m.view = viewRunMethod
		}
	case actRunFromBranch:
		m.run.reset()
		m.notice = ""
		m.view = viewRunInput // the picker only; no editor follows a checkout
		return m.openInitialBranchPick()
	case actDetectPR:
		if s := m.selected(); s != nil && s.State == store.StateCheckedOut && s.Worktree != "" {
			m.notice = "checking GitHub for a PR on " + s.Branch + "…"
			return m, detectPRCmd(m.store, s.Ticket, s.Repo, s.Branch, s.Worktree)
		}
	case actDoctor:
		m.notice = "running doctor…"
		return m, m.runDoctor()
	case actConfig:
		m.openEditConfig()
	case actSetup:
		m.openSetupForm()
	case actDaemonStart:
		m.notice = "starting daemon…"
		return m, m.startDaemon()
	case actDaemonStop:
		m.notice = "stopping daemon…"
		return m, m.stopDaemon()
	case actMenu: // the ":" command menu - agent actions + global commands
		m.view = viewPalette
		m.paletteCursor = 0
		m.paletteAgentOnly = false
		m.notice = ""
	case actAgentMenu: // Enter on an agent row - only that agent's actions
		m.view = viewPalette
		m.paletteCursor = 0
		m.paletteAgentOnly = true
		m.notice = ""
	case actRefresh:
		m.reload()
	case actQuit:
		return m, tea.Quit
	default:
		// Nothing produces an id this switch doesn't handle - but "nothing" is
		// exactly what a menu row used to do when something did, and a silent
		// no-op is indistinguishable from a hung UI. Say so instead.
		//
		// (Two arms lived here for "confirm-yes"/"confirm-no", which no menu, key
		// or button ever produced. They were removed rather than kept as a third
		// path into the stop-confirmation dialog.)
		m.notice = "unknown action: " + string(id)
	}
	return m, nil
}

// allowRerunAvailable, doAllowRerun and the flow-aware retry helper live in
// actions_denials.go - one concern (denial recovery), and this file was at
// the size guideline.
