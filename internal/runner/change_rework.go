// The feedback half of the change-review gate: the human left a comment on
// the change screen and pressed enter to send it. The run resumes the
// ticket's implementation session with that feedback, re-verifies the
// reworked change, and parks the next round at the gate. Nothing is
// committed, pushed, or posted - the gate's contract holds.
package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

func reworkChange(ctx context.Context, t Task, h Hooks) Outcome {
	logf, setState := h.logf(), h.setState()

	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	sess, out, ok := changePreflight(t, h)
	if !ok {
		return out
	}
	worktree := paths.WorktreeFor(t.Repo.Path, t.Ticket)
	if h.OnField != nil {
		h.OnField(branchFor(t), worktree, "")
	}
	ensureProjectFiles(t, worktree, logf)

	// The round baseline is the change AS THE HUMAN REVIEWED IT - snapshot
	// before the rework touches anything, so the next round's "changes since
	// your feedback" diff means exactly that.
	roundTree, terr := git.SnapshotTree(worktree)
	if terr != nil {
		logf("[%s] (warn) snapshotting the round baseline: %v", t.Ticket, terr)
	}
	// The PR body as it stood before THIS round - the only surviving copy if
	// the rework agent rewrites report.json without carrying prBody forward
	// (it's omitempty, and BuildChangeReworkPrompt's "preserve every other
	// field" instruction is unenforced). Same PLEX-60299 class verifyWithAgent
	// guards against for its OWN rewrite - but that snapshot is taken at
	// verify's own start, which is already too late for damage the rework
	// round itself did. See adoptVerifyReport, called below.
	implReport, _ := agent.ReadReport(worktree)

	feedback := strings.TrimSpace(sess.ChangeNotes)
	if extra := strings.TrimSpace(t.ReworkFeedback); extra != "" {
		if feedback != "" {
			feedback += "\n"
		}
		feedback += extra
	}
	if feedback == "" {
		return reparkWithError(t, h, "rework requested with no feedback - nothing to do")
	}

	setState(store.StateWorking, 0)
	model := t.Cfg.ModelImpl
	allowed := t.Cfg.EffectiveAllowedTools()
	mode := t.Cfg.PermissionModeFor(model)
	if mode == "auto" {
		allowed = ""
	}
	logf("[%s] stage:rework model:%s permissions:%s%s", t.Ticket, model, mode, changeRoundLabel(sess.ChangeRound+1))
	prompt := agent.BuildChangeReworkPrompt(feedback, sess.ChangeRound+1)
	opts := agent.Options{
		WorktreeDir:            worktree,
		AllowedTools:           allowed,
		PermissionMode:         mode,
		PermissionPromptConfig: approvalCallbackConfig(t, worktree, mode, logf),
		Model:                  model,
		MaxBudgetUSD:           t.Cfg.BudgetFor(config.StageImpl),
		AnthropicKey:           t.AnthropicKey,
		SettingsJSON:           t.Cfg.SandboxSettingsJSON(),
		Logf:                   logf,
	}
	if sess.SessionID != "" {
		// The implementation session's context is the change itself - resume it.
		opts.ResumeID = sess.SessionID
	} else {
		// No session to resume (the store lost it): a fresh session must first
		// re-read the change it is asked to revise.
		prompt = "Run \"git diff HEAD\" and \"git status\" first to read the local change under review.\n\n" + prompt
	}
	res, err := runStage(ctx, t, "rework", prompt, opts, logf)
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	if res.SessionID != "" && t.Store != nil {
		_ = t.Store.SetSessionID(t.Ticket, res.SessionID)
	}
	if err != nil && !git.HasChanges(worktree) {
		return reparkWithError(t, h, fmt.Sprintf("the rework agent failed before producing anything: %v", err))
	}
	if res.ErrorText != "" {
		return reparkWithError(t, h, "rework agent: "+oneLine(res.ErrorText, 300))
	}
	// The draft is consumed; the next round starts with a clean slate.
	if t.Store != nil {
		_ = t.Store.SetChangeNotes(t.Ticket, "")
	}
	return reworkVerifyAndPark(ctx, t, h, sess, worktree, roundTree, implReport)
}

// reworkVerifyAndPark is the round's tail: verify the reworked change
// (allowFix=true - it is the agent's own work) and park the next round.
// implReport is the PR body's last known-good copy, from before this round's
// rework touched anything (see reworkChange) - adoptVerifyReport falls back
// to it if the round emptied prBody.
func reworkVerifyAndPark(ctx context.Context, t Task, h Hooks, sess *store.Session, worktree, roundTree string, implReport *agent.Report) Outcome {
	logf, setState := h.logf(), h.setState()
	plan, _ := agent.ReadPlan(worktree)
	needsEmu, releaseEmu := acquireForRun(ctx, t, plan, logf)
	defer releaseEmu()
	vreport, verified, _, denials, verifyErrText := verifyWithAgent(ctx, t, worktree, "", needsEmu, true, t.AnthropicKey, logf, setState)
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
		return reparkWithError(t, h, "the reworked change does not verify: "+oneLine(reason, 400))
	}
	logf("[%s] rework: verify green ✓", t.Ticket)
	if roundTree != "" && t.Store != nil {
		_ = t.Store.SetChangeRoundTree(t.Ticket, roundTree)
	}
	report := adoptVerifyReport(implReport, vreport, t.Ticket, logf)
	return parkChangeReview(t, h, worktree, report, sess.ChangeRound+1)
}
