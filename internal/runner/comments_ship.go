// The approved half of the review-comment loop: what runs only after a human
// read the local fixes and pressed approve.
//
// Split from comments.go, which owns the half before the gate. The seam is the
// gate itself: nothing here may run without a session parked at fix-review, and
// nothing before it may commit, push, or say a word on GitHub.
package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/review"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// shipComments is the approved half: the human read the local diff and the
// draft replies and pressed approve. Only now: verify, commit, push (updating
// the open PR in place), and reply on GitHub - with whatever reply text is in
// the store at this moment, the human's edits included.
// shipGateOK is whether a ship-comments run has a human approval behind it:
// either the ticket sat at fix-review when the launcher picked it up, or it is
// a needs-you park FROM a previous ship-comments run (denials, red build, or
// timeout after the approval) - that park does not un-approve the batch, and
// bouncing its retry to "approve first" forced users to re-approve work they
// had already approved.
func shipGateOK(prior, priorFlow string) bool {
	return prior == store.StateFixReview ||
		(prior == store.StateNeedsYou && priorFlow == "ship-comments")
}

func shipComments(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()

	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	sess, pout, ok := commentsPreflight(t, h, logf)
	if !ok {
		return pout
	}
	// `--ship-comments` is deliberately not a shortcut around the preview. A
	// queued batch by itself only means that the agent was asked to work on it;
	// the fix-review state is the durable record that its local diff and drafts
	// were available for human approval.
	//
	// That record is read from PriorState, not the row: the launcher resets
	// every session to `queued` the moment it starts, so by the time this gate
	// runs the row always says an approval never happened - which is how every
	// approve ever pressed bounced to needs-you until PriorState carried the
	// answer past the reset.
	prior := t.PriorState
	if prior == "" {
		prior = sess.State
	}
	if !shipGateOK(prior, t.PriorFlow) {
		return needsYou(t, h, "ship-comments requested before local fixes were approved",
			"Review the local fixes and draft replies in the dashboard before approving this batch.")
	}

	branch := branchFor(t)
	// No restore-from-origin here, unlike the fix half: origin does not have the
	// local fixes, so a reclaimed worktree means the work to ship is gone.
	worktree := paths.WorktreeFor(t.Repo.Path, t.Ticket)
	if !paths.WorktreeReady(worktree) {
		return needsYou(t, h, "ship-comments: the worktree with the local fixes is gone",
			fmt.Sprintf("The local review fixes lived in `%s`, which no longer exists. Run the fix again from the comments screen.", worktree))
	}
	if h.OnField != nil {
		h.OnField(branch, worktree, "")
	}
	// Heal the worktree before the verify agent runs, like every other
	// entrypoint (fresh run, resume, ship, address-comments): re-seed anything
	// missing and guarantee local.properties in the project dir. This was the
	// one path that skipped it, so a worktree that predated seeding verified
	// against a broken environment exactly when the human had just approved.
	ensureProjectFiles(t, worktree, logf)

	if out, done := syncWithOrigin(t, h, worktree, branch, logf, setState); done {
		return out
	}

	cmts, err := t.Store.QueuedComments(t.Ticket)
	if err != nil {
		return needsYou(t, h, fmt.Sprintf("ship-comments: reading the queue: %v", err),
			"Apple Pie could not read which review comments this ship covers.")
	}
	if len(cmts) == 0 {
		logf("[%s] no review comments queued - nothing to ship", t.Ticket)
		return Outcome{State: store.StateReview, PRURL: sess.PRURL}
	}

	// The honesty guards (ASKME-TEST): the approved fixes are the deliverable,
	// and every claim downstream - the commit, the "Fixed in <sha>" reply, the
	// resolved thread - must be backed by them actually existing. Snapshot the
	// worktree here; the checks fire at each point the fixes could vanish.
	changedAtEntry := git.HasChanges(worktree)
	headAtEntry := git.HeadSHA(worktree)
	_, ahead, divErr := git.Divergence(worktree, branch)
	if nothingToShip(changedAtEntry, ahead, divErr) {
		if t.Store != nil {
			_ = t.Store.SetParkedFlow(t.Ticket, flowFor(t))
		}
		return needsYou(t, h, "ship-comments: the approved fixes are gone - worktree clean, origin current",
			fmt.Sprintf("The approved fixes are no longer in `%s` and nothing local is waiting to push, so there is nothing to ship. No reply was posted and the comments stay queued - run the fix again from the comments screen.", worktree))
	}

	// Verify with allowFix=true: the agent authored these edits and should be
	// allowed to iterate on its own red build. This is the once-per-approval
	// verify - the preview deliberately skipped it so feedback rounds stay fast.
	// A worktree rebuilt from origin has no .agent/plan.json - it is git-excluded
	// and never pushed - so the emulator decision silently falls back to "unit
	// tests only". Say so rather than letting a narrower verify pass for a full
	// one.
	plan, _ := agent.ReadPlan(worktree)
	if plan == nil {
		logf("[%s] no .agent/plan.json in the worktree - verifying without an emulator; "+
			"instrumented tests this ticket planned will not run", t.Ticket)
	}
	needsEmu, releaseEmu := acquireForRun(ctx, t, plan, logf)
	defer releaseEmu()

	logf("[%s] verify: agent will run build + tests on the approved fixes (emulator=%v)", t.Ticket, needsEmu)
	// Deliberately NOT resuming sess.SessionID: that session is the comment-fix
	// run, possibly days old and predating current prompts. A resumed model
	// repeats its own transcript's habits over fresh instructions - a field
	// verify resumed this way re-flailed absolute gradlew paths its history had
	// approval-prompted through, and certified off partial evidence. The
	// certification stage gets a clean context every time; the diff on disk is
	// the ground truth it needs, not the transcript.
	vreport, verified, _, denials, verifyErrText := verifyWithAgent(ctx, t, worktree, "", needsEmu, true, t.AnthropicKey, logf, setState)
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	if !verified {
		// Same classification as the other verify gates: a permissions refusal
		// is a config problem with a one-keystroke fix, an API error is its own
		// story, and only after those is it honestly "still red".
		if len(denials) > 0 {
			return denialNeedsYou(t, h, worktree, denials)
		}
		if vreport == nil {
			// Nothing ran, nothing certified - and the comments stay queued
			// with the fixes in the worktree, so park the flow: a retry must
			// re-enter ship-comments, exactly like the red-build park does.
			if t.Store != nil {
				_ = t.Store.SetParkedFlow(t.Ticket, flowFor(t))
			}
			return verifyDidNotRun(t, h, worktree, verifyErrText)
		}
		reason := "the build or tests are red on the approved fixes."
		if vreport != nil && strings.TrimSpace(vreport.VerifyLog) != "" {
			reason = vreport.VerifyLog
		}
		return commentsNeedsYou(t, h, setState, logf, worktree, reason)
	}
	logf("[%s] verify: approved fixes build + pass ✓", t.Ticket)
	if t.Store != nil {
		_ = t.Store.SetDenials(t.Ticket, "", false) // a green verify clears stale denials
	}

	// A green build that no longer contains the fixes verifies nothing: the
	// ASKME-TEST verify agent reverted the approved edit to get tests passing
	// and certified the unfixed code. That is a conflict for the human, never
	// a ship.
	if verifyRevertedFixes(changedAtEntry, git.HasChanges(worktree), headAtEntry, git.HeadSHA(worktree)) {
		if t.Store != nil {
			_ = t.Store.SetParkedFlow(t.Ticket, flowFor(t))
		}
		return needsYou(t, h, "ship-comments: verify reverted the approved fixes to get a green build",
			fmt.Sprintf("The verify agent undid the approved fixes to make the build pass - the fix conflicts with the build or tests. Nothing was committed, no reply was posted, and the comments stay queued. Read `pie logs %s` for the conflict, then rework the fix (\"Ask for a different fix\") or skip the comment.", t.Ticket))
	}

	preShipHead := git.HeadSHA(worktree)
	res := finishShip(ctx, t, h, worktree, branch,
		fmt.Sprintf("[%s] address review comments", t.Ticket), vreport, logf, setState)
	if res.State != store.StateReview || res.Err != nil {
		return res
	}
	// Belt and braces: a "Fixed in <sha>" reply may only cite a commit that
	// actually carries the fixes - a new commit from this ship, or local
	// commits the push just published. Unreachable if the guards above held.
	if nothingNewToCite(preShipHead, git.HeadSHA(worktree), ahead, divErr) {
		if t.Store != nil {
			_ = t.Store.SetParkedFlow(t.Ticket, flowFor(t))
		}
		return needsYou(t, h, "ship-comments: no new commit to cite - reply withheld",
			"The ship produced no new commit, so a \"Fixed in\" reply would name a commit that does not contain the fix. Nothing was posted and the comments stay queued - run the fix again from the comments screen.")
	}
	recordOutcome(t, h, worktree, cmts, sess.PRURL, logf)
	return res
}

// The three honesty predicates, pure so their truth tables are testable
// without a worktree.

// nothingToShip is the entry check: the worktree is clean AND origin already
// has everything local - the approved fixes exist nowhere, so there is nothing
// to verify, commit, push, or claim. An unknown divergence (offline, no
// origin) is not proof either way; the reply gate still backstops that path.
func nothingToShip(changed bool, ahead int, divErr error) bool {
	return !changed && divErr == nil && ahead == 0
}

// verifyRevertedFixes is the post-verify check: the fixes were in the worktree
// when the ship started and are gone now, with HEAD unmoved - the verify agent
// reverted them. (HEAD moving would mean the work was committed, not erased.)
func verifyRevertedFixes(changedAtEntry, changedNow bool, headAtEntry, headNow string) bool {
	return changedAtEntry && !changedNow && headAtEntry == headNow
}

// nothingNewToCite is the reply gate: finishShip created no new commit and the
// push had no local commits to publish, so no sha exists that contains the
// fix. Only a POSITIVELY known ahead>0 excuses an unmoved HEAD.
func nothingNewToCite(headBefore, headAfter string, ahead int, divErr error) bool {
	return headBefore == headAfter && !(divErr == nil && ahead > 0)
}

// recordOutcome marks the comments the agent reported fixing, preserves its
// reasons for the ones it declined, and tells the reviewers what happened.
func recordOutcome(t Task, h Hooks, worktree string, cmts []store.Comment, prURL string, logf logFn) {
	sha := git.HeadSHA(worktree)
	fixes, err := agent.ReadCommentFixes(worktree)

	var fixed []string
	if err != nil || fixes == nil || len(fixes.Fixes) == 0 {
		// No per-comment verdict. Assume the whole batch was handled: the
		// alternative marks nothing, which traps the user in a loop where the fix
		// really did land but the badge never clears.
		logf("[%s] (warn) no comment_fixes.json - assuming all %d comment(s) were addressed", t.Ticket, len(cmts))
		for _, c := range cmts {
			fixed = append(fixed, c.ID)
		}
	} else {
		// The preview can exclude a comment after the agent has produced its
		// contract. Only comments still queued are part of this approval, even
		// if an older contract mentions more ids.
		queued := make(map[string]bool, len(cmts))
		for _, c := range cmts {
			queued[c.ID] = true
		}
		for _, id := range fixes.Fixed() {
			if queued[id] {
				fixed = append(fixed, id)
			}
		}
		for _, s := range fixes.Skipped() {
			if !queued[s.ID] {
				continue
			}
			logf("[%s] skipped a comment: %s", t.Ticket, s.Note)
			_ = t.Store.SetCommentAgentNote(s.ID, s.Note)
		}
	}

	if err := t.Store.MarkAddressed(fixed, sha); err != nil {
		logf("[%s] (warn) recording addressed comments: %v", t.Ticket, err)
	}
	if err := t.Store.ClearQueued(t.Ticket); err != nil {
		logf("[%s] (warn) clearing the comment queue: %v", t.Ticket, err)
	}
	logf("[%s] addressed %d of %d review comment(s) in %s", t.Ticket, len(fixed), len(cmts), git.ShortSHA(sha))

	replyOnGitHub(t, cmts, fixed, fixes, sha, logf)

	if h.Comment != nil {
		h.Comment(fmt.Sprintf("🤖 *Apple Pie [%s]* - addressed %d review comment(s), pushed `%s` to %s",
			t.Ticket, len(fixed), git.ShortSHA(sha), prURL))
	}
}

// replyOnGitHub posts what changed on each fixed thread and resolves it.
//
// Never fatal: the code is committed and pushed by the time this runs, so a
// failure here is cosmetic. Resolving is config-gated because on many teams
// marking a thread settled is the reviewer's call, not the author's.
func replyOnGitHub(t Task, cmts []store.Comment, fixed []string, fixes *agent.CommentFixes,
	sha string, logf logFn) {
	if !t.Cfg.ReplyOnReview() || len(fixed) == 0 {
		return
	}
	byID := make(map[string]store.Comment, len(cmts))
	for _, c := range cmts {
		byID[c.ID] = c
	}
	// Thread id → our reply's comment id. The id matters: MarkReplied advances
	// the thread's last_comment to it, so the next poll does not mistake our own
	// reply for the reviewer coming back and reopen the thread we just closed.
	replied := map[string]string{}
	for _, id := range fixed {
		c, known := byID[id]
		// Only inline threads can be replied to or resolved; a review's summary
		// body is not a thread and has no reply target.
		if !known || c.Kind != review.KindThread || c.Replied {
			continue
		}
		// The store's draft is authoritative: the agent wrote it, the human may
		// have rewritten it in the approve screen, and last write wins. The
		// contract note is only the fallback for a batch that predates drafts.
		body := "Fixed in `" + git.ShortSHA(sha) + "`."
		if draft := strings.TrimSpace(c.DraftReply); draft != "" {
			body = draft + "\n\nFixed in `" + git.ShortSHA(sha) + "`."
		} else if note := strings.TrimSpace(fixes.NoteFor(id)); note != "" {
			body = "Fixed in `" + git.ShortSHA(sha) + "`: " + note
		}
		commentID, err := review.Reply(t.Repo.Path, id, body)
		if err != nil {
			logf("[%s] (warn) replying to a review comment: %v", t.Ticket, err)
			continue
		}
		replied[id] = commentID
		if !t.Cfg.ResolveReviewThreads() {
			continue
		}
		if err := review.Resolve(t.Repo.Path, id); err != nil {
			logf("[%s] (warn) resolving a review thread: %v", t.Ticket, err)
		}
	}
	if len(replied) > 0 {
		_ = t.Store.MarkReplied(replied)
		logf("[%s] replied on %d review thread(s)", t.Ticket, len(replied))
	}
}
