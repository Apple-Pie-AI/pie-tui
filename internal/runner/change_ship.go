// The approved half of the change-review gate: what runs only after a human
// read the local change in the hub and confirmed "Create pull request".
// Re-verifies when the change moved since the park (hand-edits, a rework
// round), and only then commits, pushes, and opens the PR. Any failure
// re-parks the ticket AT THE GATE with the error stored, so the screen shows
// what went wrong instead of scattering to needs-you.
package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func shipChange(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()

	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	sess, out, ok := changePreflight(t, h)
	if !ok {
		return out
	}
	worktree := paths.WorktreeFor(t.Repo.Path, t.Ticket)
	branch := branchFor(t)
	if h.OnField != nil {
		h.OnField(branch, worktree, "")
	}
	ensureProjectFiles(t, worktree, logf)

	// Re-verify only when the content moved since the park: hand-edits in
	// Android Studio, or a rework round. An untouched change was already
	// certified by the verify that parked it.
	if fp := git.ChangeFingerprint(worktree); fp == "" || fp != sess.ChangeFingerprint {
		logf("[%s] ship-change: the change moved since the park - re-verifying before the PR", t.Ticket)
		// The last known-good PR body, from the park this ship is re-verifying
		// against - adoptVerifyReport's fallback if this re-verify empties it.
		// verifyWithAgent guards its OWN rewrite already, but only from ITS
		// own snapshot forward; this is the same belt-and-suspenders every
		// other report-finalizing call site uses (runner.go, reworkVerifyAndPark).
		implReport, _ := agent.ReadReport(worktree)
		plan, _ := agent.ReadPlan(worktree)
		needsEmu, releaseEmu := acquireForRun(ctx, t, plan, logf)
		defer releaseEmu()
		vreport, verified, _, denials, verifyErrText := verifyWithAgent(ctx, t, worktree, "", needsEmu, false, t.AnthropicKey, logf, setState)
		if out, done := stopped(ctx, t, logf); done {
			return out
		}
		if !verified {
			if len(denials) > 0 {
				return denialNeedsYou(t, h, worktree, denials)
			}
			reason := verifyErrText
			if vreport != nil && strings.TrimSpace(vreport.VerifyLog) != "" {
				reason = vreport.VerifyLog
			}
			if strings.TrimSpace(reason) == "" {
				reason = "the build or tests are red on the reviewed change"
			}
			return reparkWithError(t, h, fmt.Sprintf("verify failed: %s", oneLine(reason, 400)))
		}
		if vreport != nil {
			vreport = adoptVerifyReport(implReport, vreport, t.Ticket, logf)
			if err := agent.WriteReport(worktree, vreport); err == nil {
				archiveReport(t.Ticket, vreport, logf)
			}
		}
		logf("[%s] ship-change: re-verify green ✓", t.Ticket)
	} else {
		logf("[%s] ship-change: unchanged since the park - the parking verify still stands", t.Ticket)
	}

	report, _ := agent.ReadReport(worktree)
	res := finishShip(ctx, t, h, worktree, branch, defaultCommitMsg(t), report, logf, setState)
	if res.State != store.StateReview || res.Err != nil {
		if res.Err != nil {
			return reparkWithError(t, h, oneLine(res.Err.Error(), 400))
		}
		return res
	}
	if t.Store != nil {
		_ = t.Store.SetChangeError(t.Ticket, "")
		_ = t.Store.SetChangeNotes(t.Ticket, "")
	}
	return res
}

// changePreflight is the shared entry guard for the gate's two runs: a store,
// a session, a ready worktree, and - above all - the human approval the gate
// exists for, read from PriorState (the launcher resets the row to queued
// before the pipeline can see it, exactly like the ship-comments gate).
func changePreflight(t Task, h Hooks) (*store.Session, Outcome, bool) {
	if t.Store == nil {
		return nil, needsYou(t, h, "change-review: no state database",
			"The change-review gate needs Apple Pie's state database, which this run has no access to."), false
	}
	sess, err := t.Store.Get(t.Ticket)
	if err != nil || sess == nil {
		return nil, needsYou(t, h, fmt.Sprintf("change-review: no session for %s (%v)", t.Ticket, err),
			"Apple Pie has no record of this ticket, so there is no reviewed change to act on."), false
	}
	prior := t.PriorState
	if prior == "" {
		prior = sess.State
	}
	if !changeGateOK(prior, t.PriorFlow) {
		return nil, needsYou(t, h, "change-review run requested before the change was parked for review",
			"This ticket is not waiting at the review-before-PR gate. Run the ticket normally; it parks for review after a green verify."), false
	}
	worktree := paths.WorktreeFor(t.Repo.Path, t.Ticket)
	if !paths.WorktreeReady(worktree) {
		return nil, needsYou(t, h, "change-review: the worktree with the change is gone",
			fmt.Sprintf("The reviewed change lived in `%s`, which no longer exists. Run the ticket again.", worktree)), false
	}
	return sess, Outcome{}, true
}

// reparkWithError returns the ticket to the gate with the failure stored -
// the change screen shows it in its header, per the screen's contract that a
// failed approve "comes back here with the error", never to a dead end.
// Outcome.Err is always set here (every caller is a real failure, never a
// success path) - without it, cmd/run.go's own completion banner only checks
// out.Err and out.State, so a failed rework/ship printed the exact same
// "✓ change-review - waiting for your review" line a real success would have.
func reparkWithError(t Task, h Hooks, msg string) Outcome {
	logf, setState := h.logf(), h.setState()
	logf("[%s] ship-change failed - back to change-review: %s", t.Ticket, msg)
	if t.Store != nil {
		_ = t.Store.SetChangeError(t.Ticket, msg)
		_ = t.Store.SetParkedFlow(t.Ticket, "ship-change")
	}
	setState(store.StateChangeReview, 0)
	return Outcome{State: store.StateChangeReview, Err: errors.New(msg)}
}
