// The key and message router. Every keystroke in the hub enters through
// dispatchKey, which is why cmd/dispatch_test.go pins its routing table: a
// dropped case here breaks a whole screen while every other test stays green.
package tui

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func (m monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		// Re-wrap the open plan for the new width (glamour bakes the wrap in).
		if m.view == viewPlan {
			if s := m.sessionByTicket(m.plan.ticket); s != nil {
				if md, ok := planMarkdownDoc(*s); ok {
					m.plan.lines = renderMarkdownLines(md, m.paneWidth())
				}
			}
		}
		// Same for the focused review comment's rendered body.
		if m.view == viewComments {
			m.renderFocused()
		}
		// Same for the pasted ticket shown behind the id/branch prompts.
		promptIdx := m.run.idPrompt
		if promptIdx < 0 {
			promptIdx = m.run.branchPrompt
		}
		if promptIdx >= 0 && promptIdx < len(m.run.tickets) {
			m.run.ticketLines = renderMarkdownLines(m.run.tickets[promptIdx].body, m.paneWidth())
			m.clampTicketScroll()
		}
		// And the content editor / its markdown preview.
		if m.run.content != nil {
			m.sizeContentEditor()
			if m.run.preview {
				m.run.ticketLines = renderMarkdownLines(m.run.content.Value(), m.paneWidth())
				m.clampTicketScroll()
			}
		}
		// The change screen's feedback box.
		if m.view == viewChange {
			m.sizeChangeBox()
		}
	case tea.MouseMsg:
		// Mouse reporting is only ever on inside a full-screen diff viewer;
		// the wheel scrolls it and nothing else.
		if m.view == viewComments && m.comments.cardMode == cardDiff {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.comments.diffScroll = max(0, m.comments.diffScroll-3)
			case tea.MouseButtonWheelDown:
				m.comments.diffScroll += 3 // the renderer clamps the far end
			}
		}
		// The change screen's full-file view: the wheel scrolls regardless of
		// the keyboard mode (chFull's ↑↓ switch files; a mouse has no such
		// mode, so it always scrolls the content on screen - the fix for "I
		// can't scroll without pressing enter first"). In chScroll the line
		// cursor rides along with it, same as pressing ↑↓ would.
		if m.view == viewChange && (m.change.mode == chFull || m.change.mode == chScroll) {
			c := &m.change
			n := len(c.diff[c.currentDiffPath()])
			delta := changeScrollStep
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				delta = -delta
			case tea.MouseButtonWheelDown:
				// delta stays positive
			default:
				delta = 0
			}
			if c.mode == chScroll {
				c.lineCur += delta
				c.keepLineInView(n, m.changeBodyHeight())
			} else {
				c.yOff += delta
				c.clampYOff(n, m.changeBodyHeight())
			}
		}
		// The full-screen Plan-mode chat: the wheel scrolls the transcript,
		// the same notch-sized step (changeScrollStep) the diff viewer above
		// uses - mouse capture is enabled for the whole time the box is open
		// (openChangeBox), not just once discussFullScreen() is true, so
		// this only takes effect once there's actually a conversation to
		// scroll; otherwise it's a no-op notch that does nothing.
		if m.view == viewChange && m.change.mode == chBox && m.change.discussFullScreen() {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.scrollChangeTranscriptBy(-changeScrollStep)
			case tea.MouseButtonWheelDown:
				m.scrollChangeTranscriptBy(changeScrollStep)
			}
		}
		return m, nil
	case tickMsg:
		m.reload()
		if m.view == viewChange {
			// A live --discuss round updates the transcript/error/liveness in
			// the store as it streams - the diff/PR-description panes don't
			// need this (a discuss turn can't touch the change itself).
			m.syncChangeFromSession()
		}
		if m.view == viewComments {
			m.loadComments()
			m.tickCards()
			// Entering the approve phase (a fix run just parked at fix-review,
			// or the screen was opened onto one) wants the worktree diff once.
			if cmd := m.approveDiffIfNeeded(); cmd != nil {
				return m, tea.Batch(tick(), cmd)
			}
			if cmd := m.dispatchRedos(); cmd != nil {
				return m, tea.Batch(tick(), cmd)
			}
		}
		return m, tick()
	case approveDiffMsg:
		if msg.ticket != m.comments.ticket {
			return m, nil // a stale read for a screen we've left
		}
		m.comments.diffFetching = false
		m.comments.diffLoaded = true
		if msg.err != nil {
			m.comments.diffErr = msg.err.Error()
			return m, nil
		}
		m.comments.diffErr = ""
		m.comments.diff = msg.files
		return m, nil
	case changeDiffMsg:
		if msg.ticket != m.change.ticket {
			return m, nil // a stale read for a screen that moved on
		}
		m.change.diffFetching = false
		m.change.diffLoaded = true
		if msg.err != nil {
			m.change.diffErr = msg.err.Error()
			return m, nil
		}
		m.change.diffErr = ""
		m.change.files, m.change.diff = msg.files, msg.diff
		return m, nil
	case modelCheckMsg:
		if m.modelChecks == nil {
			m.modelChecks = map[string]modelCheckState{}
		}
		m.modelChecks[msg.model] = modelCheckState{result: msg.result}
		return m, nil
	case changeRepoFilesMsg:
		rfl := newRepoFileList(msg.files)
		if m.repoFileCache == nil {
			m.repoFileCache = map[string]repoFileList{}
		}
		m.repoFileCache[msg.worktree] = rfl
		if msg.ticket == m.change.ticket {
			m.change.repoFiles = rfl.files
			m.change.repoFilesLower = rfl.lower
			m.change.repoFilesLoaded = true
		}
		return m, nil
	case reviewTickMsg:
		// The slow GitHub poll. The network work itself is in the returned
		// command, never in this handler - the 1Hz render loop must stay local.
		return m, tea.Batch(reviewTick(), m.refreshAllComments())
	case threadResolvedMsg:
		return m.handleThreadResolved(msg)
	case commentsFetchedMsg:
		// fetching is only ever set by a fetch the user asked for, so it also
		// tells us whether to report the result: the 2-minute background poll
		// must stay silent.
		manual := m.comments.fetching
		m.comments.fetching = false
		m.comments.lastFetch = time.Now()
		m.comments.syncFailed = msg.err != nil
		if msg.err != nil {
			if errors.Is(msg.err, review.ErrNoAuth) {
				m.notice = review.ErrNoAuth.Error()
			} else {
				m.notice = "checking review comments: " + msg.err.Error()
			}
			return m, nil
		}
		if msg.resolved != "" {
			// The check found the PR merged/closed and filed the row under
			// CLOSED. Say so even for the background poll - a row silently
			// leaving the review section is otherwise a small mystery.
			m.notice = fmt.Sprintf("%s: the PR is %s - moved to CLOSED", msg.ticket, msg.resolved)
		} else if msg.unparked {
			m.notice = fmt.Sprintf("%s: %d queued thread(s) resolved on GitHub - nothing left to approve, back to review", msg.ticket, msg.dequeued)
		} else if msg.dequeued > 0 {
			m.notice = fmt.Sprintf("%s: %d queued thread(s) resolved on GitHub - dropped from the batch", msg.ticket, msg.dequeued)
		} else if manual && m.view != viewComments {
			m.notice = fmt.Sprintf("%s: %d review thread(s) on the PR", msg.ticket, msg.n)
		}
		m.reload()
		if m.view == viewComments {
			m.loadComments()
			m.renderFocused()
		}
		return m, nil
	case answerDoneMsg:
		if msg.err != nil {
			m.notice = "answer failed: " + msg.err.Error()
		} else {
			m.notice = "answer sent to " + msg.ticket + " - resuming…"
		}
		return m, nil
	case cancelDoneMsg:
		m.notice = "stopped " + msg.ticket + " - process killed, worktree removed"
		m.reload()
		return m, nil
	case pauseDoneMsg:
		m.notice = "paused " + msg.ticket + " - agent stopped, worktree kept (NEEDS YOU)"
		m.reload()
		return m, nil
	case stackBranchesMsg:
		// Stale guard: apply only if the picker this fetch was for is still
		// the one open - the user may have closed it, or opened a different
		// chip's, before the fetch resolved.
		if msg.branchFirst {
			if m.run.branchFirstPick {
				m.run.stackBranches, m.run.stackLoading = msg.branches, false
			}
			return m, nil
		}
		if m.run.stackPrompt == msg.chipIndex {
			m.run.stackBranches, m.run.stackDefault, m.run.stackLoading = msg.branches, msg.defaultBranch, false
		}
		return m, nil
	case discussCancelledMsg:
		m.notice = "cancelled - " + msg.ticket
		m.reload()
		if m.change.ticket == msg.ticket {
			m.syncChangeFromSession()
		}
		return m, nil
	case doctorDoneMsg:
		m.outputTitle = "doctor - connectivity check"
		m.outputText = msg.out
		m.view = viewOutput
		return m, nil
	case formDoneMsg:
		if msg.err != nil {
			m.notice = "save failed: " + msg.err.Error()
		} else {
			m.notice = msg.notice
		}
		m.reload()
		return m, nil
	case consentDoneMsg:
		m.tel.Configure(msg.enabled, msg.deviceID, m.version)
		if msg.enabled {
			m.tel.Track("tui_opened", nil)
			m.notice = "telemetry enabled - thanks for helping!"
		}
		m.view = viewDashboard
		return m, nil
	case cloneDoneMsg:
		m.repofix.cloning = false
		m.repofix.urlMode = false
		if msg.err != nil {
			// Stay on the repo-fix screen so the user can retry or pick another
			// option; the URL they typed is left intact.
			m.notice = "clone failed: " + msg.err.Error()
			return m, nil
		}
		m.notice = "cloned into " + tildify(msg.dir) + " - launch the run again"
		m.repofix.reset()
		m.view = viewDashboard
		m.reload()
		return m, nil
	case ticketEditedMsg:
		return m.applyTicketEdit(msg)
	case checkoutDoneMsg:
		if msg.err != nil {
			m.notice = "checkout failed: " + msg.err.Error()
		} else if msg.prURL != "" {
			switch {
			case msg.threads > 0:
				m.notice = fmt.Sprintf("checked out %s - PR found, %s on it",
					msg.branch, plural(msg.threads, "review thread"))
			case msg.threads < 0:
				m.notice = "checked out " + msg.branch + " - PR found; comment fetch will retry"
			default:
				m.notice = "checked out " + msg.branch + " - PR found, no review threads yet"
			}
		} else {
			m.notice = "checked out " + msg.branch + " - no pull request yet"
		}
		m.reload()
		return m, nil
	case prDetectedMsg:
		switch {
		case msg.err != nil:
			m.notice = "checking for a PR: " + msg.err.Error()
		case msg.prURL == "":
			m.notice = "no pull request for " + msg.branch + " yet"
		default:
			m.notice = "found a PR for " + msg.branch + " - now tracked for review"
		}
		m.reload()
		return m, nil
	case runLaunchedMsg:
		if msg.err != nil {
			m.notice = "run failed: " + msg.err.Error()
		} else {
			m.notice = "launched run: " + msg.text
		}
		m.reload()
		return m, nil
	case daemonMsg:
		if msg.err != nil {
			m.notice = msg.action + " daemon: " + msg.err.Error()
		} else {
			m.notice = "daemon " + msg.action + "ed"
		}
		return m, nil
	case planOpenedMsg:
		if msg.err != nil {
			m.notice = "view plan: " + msg.err.Error()
		} else {
			m.notice = "opened " + msg.path
		}
		return m, nil
	case claudeClosedMsg:
		if msg.err != nil {
			m.notice = "open Claude Code: " + msg.err.Error()
		} else {
			m.notice = "opened Claude Code in a new window"
		}
		return m, nil
	case tea.KeyMsg:
		return m.dispatchKey(msg)
	}
	return m, nil
}

// dispatchKey routes a keystroke (real or synthesized by a button click) to the
// active sub-view, else the dashboard keymap. Button-backed actions go through
// doAction so keys and clicks stay in sync.
func (m monitorModel) dispatchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// ctrl+c quits from anywhere, and is handled before any screen sees it.
	// It used to be re-added by hand in each of a dozen handlers, which meant a
	// new screen only got the universal quit key if its author remembered - and
	// the stop-confirmation dialog didn't, so ctrl+c did nothing while it was up.
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	// View-specific handlers take precedence over the dashboard keymap.
	if m.answer.open {
		return m.updateAnswering(msg)
	}
	if m.confirm.ticket != "" {
		return m.updateConfirming(msg)
	}
	if m.approval.open {
		return m.updateApprovals(msg)
	}
	switch m.view {
	case viewConsent:
		return m.updateConsent(msg)
	case viewPalette:
		return m.updatePalette(msg)
	case viewRunMethod:
		return m.updateRunMethod(msg)
	case viewRunInput:
		return m.updateRunInput(msg)
	case viewOutput:
		return m.updateOutput(msg)
	case viewPlan:
		return m.updatePlan(msg)
	case viewForm:
		return m.updateForm(msg)
	case viewRepoFix:
		return m.updateRepoFix(msg)
	case viewComments:
		return m.updateComments(msg)
	case viewEditConfig:
		return m.updateEditConfig(msg)
	case viewPermissions:
		return m.updatePermissions(msg)
	case viewEditModels:
		return m.updateEditModels(msg)
	case viewChange:
		return m.updateChange(msg)
	}
	switch msg.String() {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.rows)+2 { // 0=Start-new 1=Start-from-branch 2=Edit-config list=3..
			m.cursor++
		}
	case ":":
		return m.doAction(actMenu)
	case "n":
		return m.doAction(actRun)
	case "r":
		return m.doAction(actRefresh)
	case "o":
		return m.doAction(actStudio)
	case "c":
		return m.doAction(actClaude)
	case "enter":
		if m.cursor == 0 {
			return m.doAction(actRun)
		}
		if m.cursor == 1 {
			return m.doAction(actRunFromBranch)
		}
		if m.cursor == 2 {
			return m.doAction(actConfig)
		}
		if m.cursor == 3 {
			return m.doAction(actModels)
		}
		// A collapsible section header folds/unfolds on Enter. The cursor stays
		// put: the header's own index never moves, since toggling only inserts
		// or removes rows after it.
		if g := m.headerUnderCursor(); g != nil {
			if m.expanded == nil {
				m.expanded = make(map[string]bool)
			}
			m.expanded[g.label] = !m.expanded[g.label]
			m.rows = buildRows(m.groups, m.expanded)
			return m, nil
		}
		// A PR with review comments used to open straight into the triage
		// screen - which blocked the menu, so Android Studio, the PR link and
		// Claude Code were unreachable on exactly the rows that need them.
		// Enter opens the menu like every other row; "Address PR feedback"
		// leads it (see agentActions).
		// A ticket paused for plan review opens straight into the full-screen
		// plan viewer; any other agent row opens its own actions menu (scoped to
		// that agent, no global commands).
		if s := m.selected(); s != nil && s.State == store.StatePlanReview {
			return m.doAction(actViewPlan)
		}
		// Fix-review used to jump straight into the cards screen the same way
		// change-review once did (below) - but if that screen ever misbehaves
		// (a stuck card, an unreadable comment body), the row's only way out
		// was however that screen's own esc/nav worked, with no menu to fall
		// back to. Enter opens the menu like any other row; "Review the local
		// fixes" leads it (see agentActions) - one extra keystroke buys a
		// guaranteed-responsive screen to retreat to if the cards screen
		// doesn't.
		// Change-review used to jump straight into the screen the same way -
		// but unlike plan-review it's reachable from several other idle
		// states too (NEEDS YOU, failed, PR-ready), where the row has other
		// live reasons to open its menu instead (Pause, Open Android Studio,
		// Stop & clean up...). Enter opens the menu like any other row;
		// "Review and make changes" leads it (see agentActions).
		// A row whose agent is paused on a permission question opens its menu
		// like any other row - with "Approve pending command" leading it (see
		// paletteItems). Enter used to hijack straight into the overlay, which
		// made Stop/Pause unreachable except by letter shortcuts, violating
		// the arrows/Enter/Esc convention; the global A key keeps the
		// one-keystroke path for answering.
		return m.doAction(actAgentMenu)
	case "a":
		return m.doAction(actAnswer)
	case "A":
		// Approve pending commands, oldest first, across all sessions.
		if len(m.pendingApprovals) > 0 {
			m.openApprovals("")
		}
		return m, nil
	case "x":
		return m.doAction(actStop)
	case "R":
		return m.doAction(actResume)
	}
	return m, nil
}
