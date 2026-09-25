// The change-review gate (review-before-PR): after a green verify, the ticket
// parks here instead of committing/pushing/opening a PR, and the human reviews
// the local change in the hub's change screen. The approve run (--ship-change,
// change_ship.go) and the feedback rework (--rework, change_rework.go) are the
// only ways forward; nothing leaves the machine until approve.
package runner

import (
	"fmt"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// parkChangeReview stops the pipeline at the gate. It persists the adopted
// in-memory report to the worktree AND the archive - finishShip normally does
// both, and skipping them here would hand the later approve process a stale
// or template-mangled report.json (the PLEX-60299 class) and leave the change
// screen's verify strip with nothing to read. The fingerprint recorded lets
// the approve run skip re-verification when nothing moved since this park.
func parkChangeReview(t Task, h Hooks, worktree string, report *agent.Report, round int) Outcome {
	logf, setState := h.logf(), h.setState()
	if report != nil {
		if err := agent.WriteReport(worktree, report); err != nil {
			logf("[%s] (warn) persisting report at the change-review park: %v", t.Ticket, err)
		}
		archiveReport(t.Ticket, report, logf)
	}
	if t.Store != nil {
		fp := git.ChangeFingerprint(worktree)
		if err := t.Store.SetChangeParked(t.Ticket, fp, round); err != nil {
			logf("[%s] (warn) recording the change-review park: %v", t.Ticket, err)
		}
		if round <= 1 {
			// A fresh gate encounter, not a rework's next round (that path
			// always parks at sess.ChangeRound+1 ≥ 2) - clear any round tree
			// left over from an earlier, unrelated change-review cycle on
			// this same ticket (one that already shipped a PR, say), or the
			// diff view trusts it as this round's baseline and shows the
			// wrong, tiny diff instead of the real change.
			if err := t.Store.SetChangeRoundTree(t.Ticket, ""); err != nil {
				logf("[%s] (warn) clearing the stale round tree: %v", t.Ticket, err)
			}
		}
	}
	logf("[%s] change-review: the change is built and verified locally - review it in the dashboard; "+
		"nothing is committed, pushed, or opened as a PR until you approve", t.Ticket)
	setState(store.StateChangeReview, 0)
	return Outcome{State: store.StateChangeReview}
}

// changeGateOK is whether a --ship-change or --rework run has a human review
// behind it. The verbs come FROM the change screen, and the screen itself is
// the review - so every idle state the screen opens for passes: the gate
// park, a hand-checked-out branch, a needs-you or failed park (whatever flow
// parked it - --resume was always available ungated from there, and this
// path is stricter, never looser), and a ticket already under PR review.
// What the gate refuses is a run with no reviewable row behind it: an
// active/queued state, or none at all.
func changeGateOK(prior, priorFlow string) bool {
	switch prior {
	case store.StateChangeReview, store.StateCheckedOut, store.StateNeedsYou,
		store.StateFailed, store.StateReview:
		return true
	}
	return false
}

// shouldParkChange is the gate condition, in one place so its truth table is
// testable: the gate is on, this is a real run (a dry run never opens a PR,
// so there is nothing to gate), and no PR exists yet (a ticket with a PR
// already took its outward step; the comments flow owns everything after).
func shouldParkChange(t Task) bool {
	return t.ReviewChange && !t.DryRun && !rowHasPR(t)
}

// rowHasPR reports whether the ticket already has a pull request on record. A
// ticket with a PR never enters the change-review gate: its outward step
// already happened, and the comments flow owns everything after.
func rowHasPR(t Task) bool {
	if t.Store == nil {
		return false
	}
	sess, err := t.Store.Get(t.Ticket)
	if err != nil || sess == nil {
		return false
	}
	return sess.PRURL != ""
}

// changeReviewNeedsYou parks a gate run at needs-you with the flow recorded,
// so the retry re-enters this gate instead of an ungated ship verb.
func changeReviewNeedsYou(t Task, h Hooks, flow, logLine, explanation string) Outcome {
	if t.Store != nil {
		_ = t.Store.SetParkedFlow(t.Ticket, flow)
	}
	return needsYou(t, h, logLine, explanation)
}

// changeRoundLabel names a round for logs: "" for the first, " (round N)" after.
func changeRoundLabel(round int) string {
	if round <= 1 {
		return ""
	}
	return fmt.Sprintf(" (round %d)", round)
}
