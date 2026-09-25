// Answering code review on an open PR: the agent applies the reviewers'
// comments in the branch's existing worktree, the gate re-runs, and the push
// updates the PR that is already open.
//
// The comments themselves arrive through the store, not through Task: the hub
// spawns this as a detached `pie run --address-comments` and the two processes
// share nothing but the database. Whatever the human ticked is queued=1 there.
package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/vcs"
)

// syncAction is how the worktree should be reconciled with origin before the
// agent edits it.
type syncAction int

const (
	syncNone syncAction = iota
	syncFastForward
	syncBlocked
)

// syncPlan decides how to reconcile a reused worktree with origin.
//
// Pure so the decision is testable without a git fixture. Rebase is
// deliberately not an option: this runs headless, and a conflicted rebase would
// strand the worktree mid-operation with nobody to resolve it. Divergence on a
// Apple Pie-owned branch means a human already stepped in, so it goes back to
// them intact.
func syncPlan(behind, ahead int, dirty bool) (syncAction, string) {
	if behind == 0 {
		// Ahead or dirty is normal and fine: finishShip commits the worktree and
		// force-pushes, and the lease is valid because nothing moved underneath.
		return syncNone, ""
	}
	if ahead == 0 && !dirty {
		return syncFastForward, ""
	}
	if ahead > 0 {
		return syncBlocked, fmt.Sprintf(
			"the branch has diverged from origin: %d commit(s) here that origin doesn't have, "+
				"and %d commit(s) on origin that this worktree doesn't", ahead, behind)
	}
	return syncBlocked, fmt.Sprintf(
		"origin has %d new commit(s) and this worktree has uncommitted changes, "+
			"so it cannot be fast-forwarded safely", behind)
}

// addressComments is the LOCAL half of answering code review: the agent edits
// the ticket's existing worktree and drafts a reply per comment, then the run
// parks at fix-review for the human to read the diff and the drafts in the
// dashboard. Nothing is committed, pushed, or posted here - that is
// shipComments, which runs only after the human approves.
func addressComments(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()

	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	sess, pout, ok := commentsPreflight(t, h, logf)
	if !ok {
		return pout
	}
	branch := branchFor(t)
	worktree, out, ok := worktreeForComments(t, h, branch, logf, setState)
	if !ok {
		return out
	}
	if h.OnField != nil {
		h.OnField(branch, worktree, "")
	}
	ensureProjectFiles(t, worktree, logf)

	if out, done := syncWithOrigin(t, h, worktree, branch, logf, setState); done {
		return out
	}

	cmts, err := t.Store.QueuedComments(t.Ticket)
	if err != nil {
		return needsYou(t, h, fmt.Sprintf("address-comments: reading the queue: %v", err),
			"Apple Pie could not read which review comments to address.")
	}
	if len(cmts) == 0 {
		logf("[%s] no review comments queued - nothing to do", t.Ticket)
		return Outcome{State: store.StateReview, PRURL: sess.PRURL}
	}
	if t.CommentFeedback != "" {
		logf("[%s] revising the local fixes per your feedback", t.Ticket)
	} else {
		logf("[%s] addressing %d review comment(s) on %s - locally, nothing ships yet",
			t.Ticket, len(cmts), sess.PRURL)
	}
	for _, c := range cmts {
		logf("[%s]   %s @%s %s", t.Ticket, c.Kind, c.Author, c.Location())
		// The owner's instruction, verbatim: whether it reached the prompt
		// must be answerable from the log (PLEX-56803).
		if note := strings.TrimSpace(c.UserNote); note != "" {
			logf("[%s]     your instruction: %s", t.Ticket, oneLine(note, 300))
		}
	}

	base := prBase(t, sess)
	fixRes, agentErr := runCommentFix(ctx, t, worktree, base, cmts, sess.SessionID, logf, setState)
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	persistDrafts(t, worktree, logf)

	// Denials during the fix used to be thrown away entirely - which is how a
	// user was shown a never-compiled snapshot test as "fixes ready for
	// approval". They are logged with the verify stage's vocabulary and drive
	// the disposition below.
	for _, d := range fixRes.Denials {
		logf("[%s] ✗ permission denied: %s(%s)", t.Ticket, d.Tool, d.Command)
	}

	switch commentFixDisposition(len(fixRes.Denials), fixEvidence(worktree, cmts), agentErr) {
	case fixParkDenied:
		// Blocked before it could produce anything reviewable: the honest state
		// is the flavor-diagnosed denial park, not an empty "fixes ready".
		return denialNeedsYou(t, h, worktree, fixRes.Denials)
	case fixParkDead:
		// A dead agent must not park a victory state. "Exited badly" is
		// tolerable only when there is something to review - a contract entry
		// for this batch or a change in the worktree; with neither, fix-review
		// would render an empty preview claiming the fixes were drafted, which
		// is a lie with a green checkmark on it.
		return needsYou(t, h,
			fmt.Sprintf("address-comments: the fix agent failed before producing anything: %v", agentErr),
			"The fix agent exited without writing any fixes or draft replies, so there is nothing to review. "+
				"The session log shows what it hit (`pie logs "+t.Ticket+"`) - a misconfigured model reads "+
				"\"There's an issue with the selected model\". Fix the cause, then run the fix again from the comments screen.")
	}

	if len(fixRes.Denials) > 0 && t.Store != nil {
		// Fixes exist but some commands were refused: park at fix-review as
		// usual, with the denials stored so the why-pane warns the human they
		// may be approving unverified work.
		rules := agent.SuggestAllowRules(fixRes.Denials, t.Cfg.EffectiveAllowedTools())
		_ = t.Store.SetDenials(t.Ticket, agent.MarshalDenials(fixRes.Denials), len(rules) > 0)
	}
	logf("[%s] fix-review: the fixes are in the worktree and the replies are drafted - "+
		"review and approve in the dashboard; nothing has been committed, pushed, or posted", t.Ticket)
	setState(store.StateFixReview, 0)
	return Outcome{State: store.StateFixReview, PRURL: sess.PRURL}
}

// The three ways a comment-fix run can land.
const (
	fixReady      = "fix-review"   // something to review (denials at most annotate it)
	fixParkDenied = "denial-park"  // blocked by permissions with nothing to show
	fixParkDead   = "agent-failed" // died with nothing to show, no denials to blame
)

// commentFixDisposition decides where a finished fix run parks. Pure so the
// truth table is testable without a worktree: denials with no evidence park as
// a denial (even when the process exited cleanly - a blocked agent often
// does); a bare failure with no evidence parks as a dead agent; anything with
// evidence is reviewable.
func commentFixDisposition(denialCount int, evidence bool, agentErr error) string {
	switch {
	case denialCount > 0 && !evidence:
		return fixParkDenied
	case agentErr != nil && !evidence:
		return fixParkDead
	default:
		return fixReady
	}
}

// commentsPreflight is the shared entry guard for both halves: the store is
// the only channel to the selection, the session names the PR, and the PR must
// still be open - pushing to a merged branch would fail after all the work,
// and OpenPR's fallback would cheerfully report the merged PR's URL.
func commentsPreflight(t Task, h Hooks, logf logFn) (*store.Session, Outcome, bool) {
	if t.Store == nil {
		return nil, needsYou(t, h, "address-comments: no state database",
			"Addressing review comments needs Apple Pie's state database, which this run has no access to."), false
	}
	sess, err := t.Store.Get(t.Ticket)
	if err != nil || sess == nil {
		return nil, needsYou(t, h, fmt.Sprintf("address-comments: no session for %s (%v)", t.Ticket, err),
			"Apple Pie has no record of this ticket, so it cannot tell which PR the comments belong to."), false
	}
	if sess.PRURL == "" {
		return nil, needsYou(t, h, "address-comments: no PR recorded for this ticket",
			"This ticket has no pull request on record, so there are no review comments to address."), false
	}
	if state, serr := vcs.PRState(t.Repo.Path, sess.PRURL); serr == nil {
		if state == "MERGED" || state == "CLOSED" {
			logf("[%s] the PR is already %s - nothing to address", t.Ticket, strings.ToLower(state))
			_ = t.Store.ClearQueued(t.Ticket)
			// File the row where it now belongs. Returning review here (as this
			// used to) left the row in the launcher's queued reset, reading as a
			// dead RUNNING agent until the stale-check caught it - and the daemon
			// would only re-discover the resolution a poll later.
			next := store.StateClosed
			if state == "MERGED" {
				next = store.StateMerged
			}
			h.setState()(next, sess.Retries)
			// Reclaim the worktree + local branch too (dirty ones spared);
			// the daemon's sweep retries anything this best-effort pass misses.
			if removed, rerr := git.ReclaimResolved(t.Repo.Path, sess.Worktree, sess.Branch); rerr == nil && removed {
				_ = t.Store.ClearWorktree(t.Ticket)
			}
			return nil, Outcome{State: next, PRURL: sess.PRURL}, false
		}
	} else {
		// Not fatal: gh may be briefly unreachable, and refusing to fix review
		// comments because a status check failed would be worse than proceeding.
		logf("[%s] (warn) could not read PR state: %v", t.Ticket, serr)
	}
	return sess, Outcome{}, true
}

// persistDrafts copies the agent's contract verdicts into the store, which is
// what the approve screen renders: a draft reply for each fixed comment, the
// agent's reason for each declined one. A missing or empty contract degrades
// to "no drafts", never to an error - the human still sees the diff.
func persistDrafts(t Task, worktree string, logf logFn) {
	fixes, err := agent.ReadCommentFixes(worktree)
	if err != nil || fixes == nil || len(fixes.Fixes) == 0 {
		logf("[%s] (warn) no comment_fixes.json - the approve screen will show the diff without drafts", t.Ticket)
		return
	}
	for _, f := range fixes.Fixes {
		if f.Action == agent.FixActionFixed {
			reply := strings.TrimSpace(f.Reply)
			if reply == "" {
				reply = strings.TrimSpace(f.Note)
			}
			_ = t.Store.SetDraftReply(f.ID, reply)
			_ = t.Store.SetCommentAgentNote(f.ID, "") // clear a decline from an earlier round
		} else {
			_ = t.Store.SetCommentAgentNote(f.ID, f.Note)
			_ = t.Store.SetDraftReply(f.ID, "")
		}
	}
}

// worktreeForComments returns the ticket's worktree, recreating it from origin
// when it has been reclaimed.
//
// "Worktree gone, PR open" is a normal state, not an error: the daemon removes
// worktrees when a PR resolves and the hub's Stop removes them on request. For
// an open PR the remote branch is the source of truth, and `worktree add -B`
// resets the local branch to it - so recovering is both safe and what keeps the
// feature from feeling brittle.
func worktreeForComments(t Task, h Hooks, branch string, logf logFn,
	setState func(string, int)) (worktree string, out Outcome, ok bool) {
	worktree = paths.WorktreeFor(t.Repo.Path, t.Ticket)
	if paths.WorktreeReady(worktree) {
		return worktree, Outcome{}, true
	}
	if !git.RefExists(t.Repo.Path, "origin/"+branch) {
		return "", needsYou(t, h,
			fmt.Sprintf("address-comments: no worktree at %s and no origin/%s to restore it from", worktree, branch),
			fmt.Sprintf("The worktree for this ticket is gone and there is no `origin/%s` branch to rebuild it from.", branch)), false
	}
	logf("[%s] worktree was reclaimed - restoring it from origin/%s", t.Ticket, branch)
	if err := git.CreateWorktree(t.Repo.Path, worktree, branch, "origin/"+branch); err != nil {
		setState(store.StateFailed, 0)
		return "", Outcome{State: store.StateFailed,
			Err: fmt.Errorf("restore worktree for %s: %w", t.Ticket, err)}, false
	}
	return worktree, Outcome{}, true
}

// syncWithOrigin refreshes the remote-tracking ref and reconciles the worktree
// with it. This is not optional: finishShip pushes with --force-with-lease, and
// the lease is that ref. On the full pipeline CreateWorktree fetches on the way
// in, but this path reuses an existing worktree - so without a fetch here, a
// branch someone else pushed to would fail the push AFTER the agent had already
// done all the work.
func syncWithOrigin(t Task, h Hooks, worktree, branch string, logf logFn,
	setState func(string, int)) (Outcome, bool) {
	if err := git.FetchBranch(worktree, branch); err != nil {
		// A fetch failure is usually transient (offline, auth). Carry on: the
		// push may still succeed, and if the lease is stale it fails loudly there.
		logf("[%s] (warn) could not fetch origin/%s: %v", t.Ticket, branch, err)
		return Outcome{}, false
	}
	behind, ahead, err := git.Divergence(worktree, branch)
	if err != nil {
		logf("[%s] (warn) could not compare with origin/%s: %v", t.Ticket, branch, err)
		return Outcome{}, false
	}
	action, why := syncPlan(behind, ahead, git.HasChanges(worktree))
	switch action {
	case syncFastForward:
		logf("[%s] origin/%s has %d new commit(s) - fast-forwarding before editing", t.Ticket, branch, behind)
		if err := git.FastForward(worktree, branch); err != nil {
			return needsYou(t, h,
				fmt.Sprintf("address-comments: fast-forward to origin/%s failed: %v", branch, err),
				fmt.Sprintf("Could not update the worktree to `origin/%s`:\n\n```\n%v\n```", branch, err)), true
		}
	case syncBlocked:
		logf("[%s] needs-you: %s", t.Ticket, why)
		return needsYou(t, h,
			fmt.Sprintf("address-comments: %s", why),
			fmt.Sprintf("Apple Pie did not touch the code, because %s.\n\nReconcile it by hand and try again:\n`cd %s && git status`",
				why, worktree)), true
	}
	return Outcome{}, false
}

// runCommentFix runs the agent over the queued comments and returns its full
// result - the caller reads Denials (a blocked fix must not park as "ready")
// alongside the session id.
//
// Deliberately NOT resuming the ticket's stored Claude session. The self-review
// fix round resumes because it runs seconds later with the context still hot;
// review comments arrive hours or days afterwards, by which time resuming can
// fail outright - and headless, that means no fix at all. The comments carry
// their own path, line and diff hunk, and the prompt tells the agent to re-read
// the diff, which is both more reliable than a stale transcript and starts with
// a clean context budget.
func runCommentFix(ctx context.Context, t Task, worktree, base string, cmts []store.Comment,
	prevSessionID string, logf logFn, setState func(string, int)) (agent.Result, error) {
	setState(store.StateWorking, 0)
	fixAllowed := t.Cfg.EffectiveAllowedTools()
	model := t.Cfg.EffectiveModelCommentFix()
	fixMode := t.Cfg.PermissionModeFor(model)
	if fixMode == "auto" {
		fixAllowed = "" // no allowlist under the classifier; the prompt says so
	}
	logf("[%s] stage:comment-fix model:%s permissions:%s", t.Ticket, model, fixMode)
	prompt := agent.BuildCommentFixPrompt(t.Ticket, t.Summary, base, fixAllowed, toReviewComments(cmts))
	opts := agent.Options{
		WorktreeDir:            worktree,
		AllowedTools:           fixAllowed,
		PermissionMode:         fixMode,
		PermissionPromptConfig: approvalCallbackConfig(t, worktree, fixMode, logf),
		Model:                  model,
		MaxBudgetUSD:           t.Cfg.MaxBudgetUSD,
		AnthropicKey:           t.AnthropicKey,
		SettingsJSON:           t.Cfg.SandboxSettingsJSON(),
		Logf:                   logf,
	}
	if fb := strings.TrimSpace(t.CommentFeedback); fb != "" {
		// A revise round: the fix session just produced these edits, so unlike
		// the days-later first round its context is fresh and worth resuming.
		if prevSessionID != "" {
			prompt = agent.BuildCommentRevisePrompt(fb)
			opts.ResumeID = prevSessionID
		} else {
			// No session to resume (e.g. the store lost it): fold the feedback
			// into a fresh fix run rather than dropping it on the floor.
			prompt += "\n\nAdditional instruction from the repository owner: " + fb
		}
	}
	res, err := agent.Run(ctx, prompt, opts)
	if err != nil {
		// Not fatal on its own: the agent may have written the files before
		// exiting badly. The caller decides - with evidence, not hope.
		logf("[%s] (warn) address-comments agent exited: %v", t.Ticket, err)
	}
	if res.SessionID != "" && t.Store != nil {
		_ = t.Store.SetSessionID(t.Ticket, res.SessionID)
	}
	return res, err
}

// fixEvidence is whether the fix run left anything a human could review: a
// contract entry for one of the queued comments, or any change in the worktree.
// A stale contract from an earlier round does not count - its ids are not in
// this batch - which is exactly the case that made a dead agent look done.
func fixEvidence(worktree string, queued []store.Comment) bool {
	if git.HasChanges(worktree) {
		return true
	}
	fixes, err := agent.ReadCommentFixes(worktree)
	if err != nil || fixes == nil {
		return false
	}
	ids := make(map[string]bool, len(queued))
	for _, c := range queued {
		ids[c.ID] = true
	}
	for _, f := range fixes.Fixes {
		if ids[f.ID] {
			return true
		}
	}
	return false
}

// commentsNeedsYou parks the ticket when the gate is red after the agent's
// fixes. The comments stay queued and nothing is pushed, so trying again picks
// up exactly where this left off.
func commentsNeedsYou(t Task, h Hooks, setState func(string, int), logf logFn,
	worktree, reason string) Outcome {
	logf("[%s] needs-you: build/tests red after the review fixes - nothing pushed", t.Ticket)
	if t.Store != nil {
		// Record which flow parked here, so the retry re-enters ship-comments
		// (the gate recognizes a parked retry via PriorFlow) instead of the
		// bare resume - which re-verified the worktree but never shipped the
		// approved replies.
		_ = t.Store.SetParkedFlow(t.Ticket, flowFor(t))
	}
	setState(store.StateNeedsYou, 0)
	if h.Comment != nil {
		h.Comment(fmt.Sprintf(
			"🤖 *Apple Pie [%s] - review fixes are red*\n\nThe agent applied the review comments, but the build or tests failed, so nothing was pushed. The changes are waiting in the worktree.\n\n*What the agent saw (tail):*\n{code}\n%s\n{code}\n\nFix and retry:\n`open -a \"Android Studio\" %s`",
			t.Ticket, tail(reason, 1500), worktree))
	}
	return Outcome{State: store.StateNeedsYou}
}

// prBase resolves the branch the PR targets, for the diff the agent re-reads.
func prBase(t Task, sess *store.Session) string {
	if t.Base != "" {
		return t.Base
	}
	if sess != nil && sess.BaseBranch != "" {
		return sess.BaseBranch
	}
	if t.Repo.Base != "" {
		return t.Repo.Base
	}
	return git.DefaultBranch(t.Repo.Path)
}

// toReviewComments narrows stored rows to what the prompt needs, which is also
// what keeps the agent package free of any dependency on store or review.
func toReviewComments(cs []store.Comment) []agent.ReviewComment {
	out := make([]agent.ReviewComment, 0, len(cs))
	for _, c := range cs {
		out = append(out, agent.ReviewComment{
			ID:       c.ID,
			Author:   c.Author,
			Path:     c.Path,
			Line:     c.Line,
			Body:     c.Body,
			DiffHunk: c.DiffHunk,
			UserNote: c.UserNote,
			Outdated: c.Outdated,
		})
	}
	return out
}
