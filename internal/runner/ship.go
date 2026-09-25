// The terminal paths: committing, pushing and opening the PR, plus the two
// alternate entrypoints that reuse an existing worktree instead of planning
// from scratch (--resume verifies the human's fix, --ship trusts it outright).
package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/vcs"
)

// defaultCommitMsg is the subject the first commit on a ticket's branch gets.
func defaultCommitMsg(t Task) string {
	return fmt.Sprintf("[%s] %s", t.Ticket, t.Summary)
}

// finishShip commits any pending changes, and (unless dry-run) pushes the branch
// and opens a PR. Shared by the full pipeline and the resume path. report may be
// nil (resume has no fresh report.json).
//
// commitMsg is passed in rather than derived because the address-comments path
// adds a SECOND commit to a branch that already has one: reusing the original
// subject there makes the PR read as if the same change was committed twice.
func finishShip(ctx context.Context, t Task, h Hooks, worktree, branch, commitMsg string, report *agent.Report,
	logf func(string, ...interface{}), setState func(string, int)) Outcome {
	repo := t.Repo

	// Last gate before anything leaves the machine. All three entrypoints funnel
	// through here, so one check keeps a cancelled run from pushing a branch and
	// opening a PR nobody is still waiting for. (The git/gh calls below are short
	// and not themselves interruptible - the point is not to start them.)
	if out, done := stopped(ctx, t, logf); done {
		return out
	}

	// Re-read the freshest report (self-review/fix rounds rewrite it), archive it
	// in Apple Pie's own home, and make sure the .agent/ scratch dir never lands in
	// the commit/PR. report may be nil on the resume path.
	report = reloadReport(worktree, t.Ticket, report, logf)
	if report != nil {
		archiveReport(t.Ticket, report, logf)
	}
	// Archive the self-review verdict beside the report - like plan/report, its
	// durable home is ~/.pie, never the target repo.
	if review, err := agent.ReadReview(worktree); err == nil && review != nil {
		if data, jerr := json.MarshalIndent(review, "", "  "); jerr == nil {
			_ = os.WriteFile(filepath.Join(paths.Reports(), t.Ticket+"-review.json"), data, 0o644)
		}
	}
	git.UntrackAgentDir(worktree)

	if git.HasChanges(worktree) {
		if err := git.CommitAll(worktree, commitMsg); err != nil {
			setState(store.StateFailed, 0)
			return Outcome{State: store.StateFailed, Err: fmt.Errorf("commit: %w", err)}
		}
	}

	if t.DryRun {
		logf("[%s] dry-run: branch %s ready - skipping push + PR", t.Ticket, branch)
		setState(store.StateReview, 0)
		return Outcome{State: store.StateReview}
	}

	// PR base: use the stacked base if set, else load from the store (covers
	// resume/ship where t.Base is "" but the base was recorded at worktree time),
	// else fall back to the repo default.
	base := t.Base
	if base == "" && t.Store != nil {
		if sess, err := t.Store.Get(t.Ticket); err == nil && sess != nil {
			base = sess.BaseBranch
		}
	}
	if base == "" {
		base = repo.Base
	}
	if base == "" {
		base = git.DefaultBranch(repo.Path)
	}
	setState(store.StateReview, 0)
	logf("[%s] review · pushing %s", t.Ticket, branch)
	if err := git.Push(worktree, branch); err != nil {
		setState(store.StateFailed, 0)
		return Outcome{State: store.StateFailed, Err: fmt.Errorf("push: %w", err)}
	}

	var files []string
	if report != nil {
		files = report.FilesChanged
	}
	pr := vcs.PRData{
		Ticket: t.Ticket, Summary: t.Summary, Branch: branch, Base: base,
		FilesChanged: files, Tests: testsLine(report),
		Attempts: 1, JiraBaseURL: t.Cfg.JiraBaseURL,
	}
	// Prefer the repo's own PR template (filled in by the agent); fall back to
	// Apple Pie's default body only when the repo has no template.
	body := ""
	if report != nil && strings.TrimSpace(report.PRBody) != "" {
		body = report.PRBody
		logf("[%s] PR body from repo template", t.Ticket)
		// Completeness repair: restore any sections the LLM dropped.
		if tmpl := vcs.ReadRepoTemplate(worktree); tmpl != "" {
			if missing := vcs.MissingSections(tmpl, body); len(missing) > 0 {
				logf("[%s] PR body was missing %d template section(s) - restored from template", t.Ticket, len(missing))
				body = vcs.RepairSections(tmpl, body, missing)
			}
		}
	} else {
		b, err := vcs.RenderPRBody(pr)
		if err != nil {
			// The branch is already pushed and the row already says "review" -
			// without this the store keeps claiming review for a ticket that has
			// no PR, and the daemon polls PRState on an empty PRURL forever.
			setState(store.StateFailed, 0)
			return Outcome{State: store.StateFailed, Err: err}
		}
		body = b
	}
	prTitle := prTitleFor(worktree, t.Ticket, t.Summary)
	prURL, existed, err := vcs.OpenPR(worktree, base, prTitle, body)
	if err != nil {
		setState(store.StateFailed, 0)
		return Outcome{State: store.StateFailed, Err: err}
	}
	if existed {
		// A --from-branch ticket continuing someone else's open PR (or a second
		// resume/ship on this one) - the new commits are already pushed to it,
		// nothing else to do. Never open a second, competing PR for one branch.
		logf("[%s] PR already open for %s - not creating another: %s", t.Ticket, branch, prURL)
	} else {
		logf("[%s] PR opened: %s", t.Ticket, prURL)
	}
	if h.OnField != nil {
		h.OnField("", "", prURL)
	}

	if h.Comment != nil {
		pr.PRURL = prURL
		if c, err := vcs.RenderJiraComment(pr); err == nil {
			h.Comment(c)
		}
	}
	logf("[%s] review - human-gated. Apple Pie stops here.", t.Ticket)
	return Outcome{State: store.StateReview, PRURL: prURL}
}

// reloadReport re-reads the freshest report.json from the worktree, keeping the
// caller's template-filled PR body authoritative over the disk copy: the caller's
// report carries verifyWithAgent's in-memory prBody restore (PLEX-60299), which
// never reaches disk - taking the disk copy wholesale shipped the mangled body
// anyway. Returns prev unchanged when no report can be read (resume with a dead
// worktree, say).
func reloadReport(worktree, ticket string, prev *agent.Report, logf logFn) *agent.Report {
	prevBody := ""
	if prev != nil {
		prevBody = prev.PRBody
	}
	fresh, err := agent.ReadReport(worktree)
	if err != nil {
		return prev
	}
	if restorePRBody(fresh, prevBody) {
		logf("[%s] (warn) report.json on disk lost the template-filled PR body - restored it", ticket)
	}
	return fresh
}

// restorePRBody puts prev back into fresh when the report on disk lost or
// rewrote the template-filled PR body, and reports whether it did. An empty
// prev means the caller never had a body to protect (repo without a template,
// nil report on resume) - the disk copy then stands as-is.
func restorePRBody(fresh *agent.Report, prev string) bool {
	if fresh == nil || strings.TrimSpace(prev) == "" || fresh.PRBody == prev {
		return false
	}
	fresh.PRBody = prev
	return true
}

// testsLine is the PR comment's verification claim, derived from what the
// verify agent actually recorded rather than asserted by pie. The old
// hardcoded "✅ compile + tests" was stamped on every PR - including one whose
// agent had compiled only, with the test task still running when it certified
// (the field overclaim a reviewer then flagged). Pie never claims tests it
// did not see claimed.
func testsLine(report *agent.Report) string {
	if report == nil || strings.TrimSpace(report.VerifyLog) == "" {
		return "✅ agent-verified"
	}
	first := strings.TrimSpace(strings.SplitN(report.VerifyLog, "\n", 2)[0])
	return "✅ agent-verified: " + oneLine(first, 100)
}

// resumeShip continues a ticket from its EXISTING worktree, preserving manual
// changes: it re-runs the gate once and, if green, commits + pushes + opens a PR.
// It never re-plans or lets the agent edit the code ("verify & ship").
func resumeShip(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()
	repo := t.Repo
	branch := branchFor(t)
	worktree := paths.WorktreeFor(repo.Path, t.Ticket)

	if !paths.WorktreeReady(worktree) {
		setState(store.StateFailed, 0)
		return Outcome{State: store.StateFailed,
			Err: fmt.Errorf("resume: no worktree at %s - run without --resume to start fresh", worktree)}
	}
	if h.OnField != nil {
		h.OnField(branch, worktree, "")
	}
	logf("[%s] resume: verifying your changes in %s on %s", t.Ticket, worktree, branch)

	// Heal the worktree before re-verifying: worktrees created before seeding
	// existed are missing local.properties etc., and re-running the gate against
	// that environment loops in needs-you forever no matter how good the fix is.
	// Idempotent - a file the human placed or edited is never overwritten.
	ensureProjectFiles(t, worktree, logf)

	// Reuse the preserved plan/report if present (for the emulator decision).
	plan, _ := agent.ReadPlan(worktree)
	report, _ := agent.ReadReport(worktree)

	// Boot + acquire the emulator only if the original plan needed instrumented tests.
	needsEmu, releaseEmu := acquireForRun(ctx, t, plan, logf)
	defer releaseEmu()

	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	anthropicKey := t.AnthropicKey
	logf("[%s] verify (resume): agent will run build + tests on your changes (emulator=%v)", t.Ticket, needsEmu)

	// The human already fixed the code; the agent only RUNS the build + tests to
	// confirm it (allowFix=false - it never edits) and self-certifies in report.json.
	vreport, verified, _, denials, verifyErrText := verifyWithAgent(ctx, t, worktree, "", needsEmu, false, anthropicKey, logf, setState)
	// A cancelled verify produces no report; without this it would read as "still
	// red" and park the ticket in NEEDS YOU on the way out.
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	if !verified {
		// Same classification as the fresh-run gate: a permissions refusal is a
		// config problem with a one-keystroke fix, not "your fix is still red".
		if len(denials) > 0 {
			return denialNeedsYou(t, h, worktree, denials)
		}
		if vreport == nil {
			return verifyDidNotRun(t, h, worktree, verifyErrText)
		}
		reason := "the build/tests are still red on your changes."
		if vreport != nil && strings.TrimSpace(vreport.VerifyLog) != "" {
			reason = vreport.VerifyLog
		}
		return resumeNeedsYou(t, h, setState, logf, worktree, reason)
	}
	if vreport != nil {
		report = vreport
	}
	logf("[%s] verify (resume): agent certified your fix builds + passes ✓", t.Ticket)
	if t.Store != nil {
		_ = t.Store.SetDenials(t.Ticket, "", false) // a green verify clears stale denials
	}

	return finishShip(ctx, t, h, worktree, branch, defaultCommitMsg(t), report, logf, setState)
}

// shipOnly trusts the human's fix completely: it SKIPS verification entirely and
// goes straight to commit + push + PR from the existing worktree. It's the escape
// hatch for when verification itself is the unreliable part - e.g. sandbox or
// environment constraints in the worktree that fail the build regardless of the
// code (the Kotlin Native cache .lock case), where re-running the gate would loop
// in needs-you forever no matter how good the fix is. The human has confirmed it
// by hand; Apple Pie takes their word and ships.
func shipOnly(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()
	repo := t.Repo
	branch := branchFor(t)
	worktree := paths.WorktreeFor(repo.Path, t.Ticket)

	if !paths.WorktreeReady(worktree) {
		setState(store.StateFailed, 0)
		return Outcome{State: store.StateFailed,
			Err: fmt.Errorf("ship: no worktree at %s - run without --ship to start fresh", worktree)}
	}
	if h.OnField != nil {
		h.OnField(branch, worktree, "")
	}
	logf("[%s] ship: trusting your fix - skipping verification, committing + opening PR", t.Ticket)

	// Same healing as resume: harmless here (nothing builds), but it keeps the
	// worktree consistent for whoever opens it next, and seeded files are
	// gitignored so none of them can reach the commit below.
	ensureProjectFiles(t, worktree, logf)

	// Reuse the last report (for PR body/files) if one survived; finishShip
	// tolerates a nil report.
	report, _ := agent.ReadReport(worktree)
	return finishShip(ctx, t, h, worktree, branch, defaultCommitMsg(t), report, logf, setState)
}

// resumeNeedsYou handles a failed verification during resume: it stops at
// needs-you and asks the human to fix the worktree again (the agent never edits
// on the resume path). reason is the agent's verifyLog (or a fallback).
func resumeNeedsYou(t Task, h Hooks, setState func(string, int), logf func(string, ...interface{}),
	worktree, reason string) Outcome {
	logf("[%s] needs-you: build/tests still red after your changes", t.Ticket)
	setState(store.StateNeedsYou, 0)
	if h.Comment != nil {
		h.Comment(fmt.Sprintf(
			"🤖 *Apple Pie [%s] - still red after resume*\n\nThe build or tests failed on your changes.\n\n*What the agent saw (tail):*\n{code}\n%s\n{code}\n\nFix in the worktree and resume again:\n`open -a \"Android Studio\" %s`",
			t.Ticket, tail(reason, 1500), worktree))
	}
	return Outcome{State: store.StateNeedsYou}
}
