// Package runner is the per-ticket pipeline: worktree → agent → gate → PR.
// It is driven identically by the CLI (`pie run`) and the daemon; callers
// observe progress through Hooks (Decision 9 lifecycle states).
package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/vcs"
)

// Run executes the full staged pipeline for one ticket:
// PLAN → CLARIFY → IMPLEMENT → SELF-REVIEW → GATE → PR
//
// Cancelling ctx stops the agent mid-stage and unwinds the pipeline at the next
// stage boundary, so a cancelled ticket never goes on to commit and push.
func Run(ctx context.Context, t Task, h Hooks) Outcome {
	logRunDiagnostics(t, h.logf())
	if t.ShipComments {
		return shipComments(ctx, t, h)
	}
	if t.AddressComments {
		return addressComments(ctx, t, h)
	}
	if t.ShipChange {
		return shipChange(ctx, t, h)
	}
	if t.Rework {
		return reworkChange(ctx, t, h)
	}
	if t.Discuss {
		return discussChange(ctx, t, h)
	}
	if t.Ship {
		return shipOnly(ctx, t, h)
	}
	if t.Resume {
		return resumeShip(ctx, t, h)
	}
	logf, setState := h.logf(), h.setState()

	// Before anything, including the first transition. setState(planning) is the
	// very next statement, and a run that is already cancelled must not record it
	// - nor the `failed` that follows when worktree creation is then abandoned.
	// The guards further down cover cancellation *during* a stage; this one
	// covers a run that never got to start, which is what a TUI Stop issued
	// moments after launch looks like.
	if out, done := stopped(ctx, t, logf); done {
		return out
	}

	repo := t.Repo
	branch := branchFor(t)
	worktree := paths.WorktreeFor(repo.Path, t.Ticket)
	anthropicKey := t.AnthropicKey

	// 1. Provision an isolated worktree (Decision 7).
	setState(store.StatePlanning, 0)
	if t.FromPlan {
		// Approve path: REUSE the worktree the human reviewed. CreateWorktree
		// removes and recreates the dir (os.RemoveAll), which would destroy the
		// reviewed .agent/plan.json that --from-plan implements from.
		if !paths.WorktreeReady(worktree) {
			return needsYou(t, h, "approve requested but the reviewed worktree is gone",
				"approve requested but the reviewed worktree is gone - re-run the ticket to plan again")
		}
		logf("[%s] from-plan: reusing the reviewed worktree %s on %s", t.Ticket, worktree, branch)
	} else if t.FromBranch {
		// Start-from-branch: check out the EXISTING branch as-is. CreateWorktree's
		// -B would reset it to the base and destroy the very commits (an open
		// PR's, a colleague's) this mode exists to work on.
		logf("[%s] worktree %s on existing branch %s (checkout, no reset)", t.Ticket, worktree, branch)
		if err := git.CheckoutWorktree(repo.Path, worktree, branch); err != nil {
			setState(store.StateFailed, 0)
			return Outcome{State: store.StateFailed, Err: fmt.Errorf("checkout branch %q: %w", branch, err)}
		}
	} else {
		// Resolve the stacked base: the flag wins, but on a plain re-run (no --base)
		// fall back to the value persisted at creation time so re-running a stacked
		// ticket doesn't silently reset it to origin/<default> and drop the parent's
		// commits. (finishShip reads the same stored value for the PR base.)
		stackBase := t.Base
		if stackBase == "" && t.Store != nil {
			if sess, err := t.Store.Get(t.Ticket); err == nil && sess != nil {
				stackBase = sess.BaseBranch
			}
		}
		// Resolve the base ref: prefer a local branch (covers in-progress parents),
		// then fall back to origin/<name>, then origin/<default>. Validate before
		// creating the worktree so a bad base surfaces a clear error rather than a
		// raw git message.
		baseRef := "origin/" + func() string {
			if repo.Base != "" {
				return repo.Base
			}
			return git.DefaultBranch(repo.Path)
		}()
		if stackBase != "" {
			if git.RefExists(repo.Path, stackBase) {
				baseRef = stackBase // local branch (e.g. in-progress parent)
			} else if git.RefExists(repo.Path, "origin/"+stackBase) {
				baseRef = "origin/" + stackBase
			} else {
				setState(store.StateNeedsYou, 0)
				return Outcome{State: store.StateNeedsYou,
					Err: fmt.Errorf("stack base %q: no local or remote branch found", stackBase)}
			}
		}
		logf("[%s] worktree %s on %s (base: %s)", t.Ticket, worktree, branch, baseRef)
		if err := git.CreateWorktree(repo.Path, worktree, branch, baseRef); err != nil {
			setState(store.StateFailed, 0)
			return Outcome{State: store.StateFailed, Err: fmt.Errorf("create worktree: %w", err)}
		}
	}
	if h.OnField != nil {
		h.OnField(branch, worktree, "")
	}

	// Make the worktree buildable: seed the main checkout's git-ignored files
	// (local.properties, google-services.json, .env, …) and ensure an SDK path
	// in the project dir - git materializes only tracked files, and this gap is
	// exactly what broke Gradle in fresh worktrees.
	projectDir := relProjectDir(worktree, ensureProjectFiles(t, worktree, logf))

	// Copy any attached images into the worktree so the agent's Read tool can view
	// them, and point the description at them. .agent/ is git-excluded, so the
	// images never land in the PR.
	desc := stageImages(t, worktree, logf)

	// 2. PLAN stage - strong model, read-only tools.
	modelPlan := t.Cfg.ModelPlan
	modelImpl := t.Cfg.ModelImpl
	modelReview := t.Cfg.ModelReview
	logf("[%s] stage:plan model:%s", t.Ticket, modelPlan)
	planOpts := agent.Options{
		WorktreeDir:  worktree,
		AllowedTools: agent.PlanTools,
		Model:        modelPlan,
		MaxBudgetUSD: t.Cfg.MaxBudgetUSD,
		AnthropicKey: anthropicKey,
		SettingsJSON: t.Cfg.SandboxSettingsJSON(),
		Logf:         logf,
	}
	// On the approve path (--from-plan) we skip re-planning entirely and implement
	// straight from the plan.json the human already reviewed in the worktree
	// (.agent/ is git-excluded, so it survives the branch reset on re-provision).
	var planRes agent.Result
	if !t.FromPlan {
		var err error
		planRes, err = agent.Run(ctx, agent.BuildPlanPrompt(t.Ticket, t.Summary, desc, agent.PlanTools), planOpts)
		if err != nil {
			logf("[%s] (warn) plan stage exited: %v", t.Ticket, err)
		}
		// Persist the plan session so "Open in Claude Code" on a ticket paused at
		// the plan-review gate resumes into the planning conversation.
		if planRes.SessionID != "" && t.Store != nil {
			_ = t.Store.SetSessionID(t.Ticket, planRes.SessionID)
		}
	}

	if out, done := stopped(ctx, t, logf); done {
		return out
	}

	plan, err := agent.ReadPlan(worktree)
	if err != nil {
		if t.FromPlan {
			return needsYou(t, h, fmt.Sprintf("approve requested but no plan.json found in the worktree (%v)", err),
				"approve requested but no plan.json found in the worktree")
		}
		// An API-level failure (budget, auth, availability) means the agent never
		// got to think - blaming the ticket would send the human down the wrong
		// road entirely (and did: a 429 used to read as "ticket too ambiguous").
		if planRes.ErrorText != "" {
			return apiNeedsYou(t, h, "plan", planRes.ErrorText)
		}
		return needsYou(t, h, fmt.Sprintf("plan stage did not produce plan.json (%v)", err),
			fmt.Sprintf("The plan stage failed to produce a plan - the ticket may be too ambiguous or the codebase too complex to analyse automatically.\n\nError: `%v`", err))
	}
	logf("[%s] plan (%s/%s): %s", t.Ticket, plan.Type, plan.Confidence, oneLine(plan.Plan, 120))
	archivePlan(t.Ticket, t.Summary, plan, logf)

	// Start emulator in the background, concurrent with the implement stage.
	// We do this right after reading the plan so boot time overlaps with Claude.
	sdkPaths := emulator.ResolvePaths(t.Cfg.AndroidSDKPath)
	avdName := ""
	if plan.NeedsEmulator {
		avdName = resolveAVD(t.Cfg.AVDName, sdkPaths, logf)
	}
	needsEmu := avdName != "" && t.Store != nil
	var emulatorReady chan error
	if needsEmu {
		_ = t.Store.RegisterEmulator(avdName)
		emulatorReady = make(chan error, 1)
		go bootOrWait(ctx, avdName, sdkPaths, t.Store, logf, emulatorReady)
		logf("[%s] emulator boot started in background (avd: %s, adb: %s)", t.Ticket, avdName, sdkPaths.ADB)
	}

	// 3. CLARIFY - always post plan to Jira; stop if there are blocking questions.
	if h.Comment != nil {
		if comment, cerr := vcs.RenderPlanComment(vcs.PlanData{
			Ticket: t.Ticket, Summary: t.Summary,
			Plan: plan.Plan, Steps: plan.Steps, Questions: plan.Questions,
			Confidence: plan.Confidence, Type: plan.Type,
		}); cerr == nil {
			h.Comment(comment)
		}
	}
	// Questions always win: even with the plan-review gate on, blocking questions
	// stop first so the human answers before there's a plan worth reviewing.
	// On the approve path (--from-plan) the human has already seen the plan, so
	// both the questions gate and the review gate are skipped.
	if !t.FromPlan {
		if len(plan.Questions) > 0 {
			logf("[%s] awaiting-answer: %d question(s) posted to ticket", t.Ticket, len(plan.Questions))
			for i, q := range plan.Questions {
				logf("[%s]   Q%d: %s", t.Ticket, i+1, q)
			}
			setState(store.StateAwaiting, 0)
			return Outcome{State: store.StateAwaiting}
		}
		// Plan-review gate: pause here so the human can approve or give feedback in
		// the dashboard. The plan is already persisted in .agent/plan.json.
		if t.ReviewPlan {
			logf("[%s] plan-review: plan ready - approve or give feedback in the dashboard", t.Ticket)
			setState(store.StatePlanReview, 0)
			return Outcome{State: store.StatePlanReview}
		}
	}

	// 4. IMPLEMENT stage - fast model, full tools, plan injected.
	setState(store.StateWorking, 0)
	implAllowed := t.Cfg.EffectiveAllowedTools()
	implMode := t.Cfg.PermissionModeFor(modelImpl)
	logf("[%s] stage:implement model:%s permissions:%s", t.Ticket, modelImpl, implMode)
	if implMode == "auto" {
		implAllowed = "" // no allowlist under the classifier; the prompt says so
	}
	implOpts := agent.Options{
		WorktreeDir:            worktree,
		AllowedTools:           implAllowed,
		PermissionMode:         implMode,
		PermissionPromptConfig: approvalCallbackConfig(t, worktree, implMode, logf),
		Model:                  modelImpl,
		MaxBudgetUSD:           t.Cfg.MaxBudgetUSD,
		AnthropicKey:           anthropicKey,
		SettingsJSON:           t.Cfg.SandboxSettingsJSON(),
		Logf:                   logf,
	}
	implRes, runErr := agent.Run(ctx,
		agent.BuildImplPrompt(t.Ticket, t.Summary, desc, plan, implAllowed, projectDir),
		implOpts)
	if runErr != nil {
		logf("[%s] (warn) implement stage exited: %v", t.Ticket, runErr)
	}
	sessionID := implRes.SessionID
	// Persist the Claude session id so "Open in Claude Code" can `claude --resume`
	// straight back into the agent's conversation for this ticket.
	saveSession := func(id string) {
		if t.Store != nil && id != "" {
			_ = t.Store.SetSessionID(t.Ticket, id)
		}
	}
	saveSession(sessionID)

	if out, done := stopped(ctx, t, logf); done {
		return out
	}

	report, err := agent.ReadReport(worktree)
	if err != nil {
		if implRes.ErrorText != "" {
			return apiNeedsYou(t, h, "implement", implRes.ErrorText)
		}
		return needsYou(t, h, fmt.Sprintf("missing/invalid report.json (%v)", err),
			"The implement stage ended without producing a completion report - the session likely crashed or timed out mid-run.")
	}
	if report.Status == "needs_human" {
		return needsYou(t, h, fmt.Sprintf("agent reported needs_human - %s", report.Summary),
			fmt.Sprintf("The agent determined it cannot safely complete this ticket automatically:\n\n> %s", report.Summary))
	}
	logf("[%s] implement done: %s", t.Ticket, report.Summary)

	// 5. SELF-REVIEW - adversarial diff review; one fix round if issues found.
	setState(store.StateReviewing, 0)
	logf("[%s] stage:review model:%s", t.Ticket, modelReview)
	reviewOpts := agent.Options{
		WorktreeDir:  worktree,
		AllowedTools: agent.ReviewTools,
		Model:        modelReview,
		MaxBudgetUSD: t.Cfg.MaxBudgetUSD * 0.3,
		AnthropicKey: anthropicKey,
		SettingsJSON: t.Cfg.SandboxSettingsJSON(),
		Logf:         logf,
	}
	if _, err := agent.Run(ctx, agent.BuildReviewPrompt(t.Ticket, t.Summary, plan), reviewOpts); err != nil {
		logf("[%s] (warn) review stage exited: %v", t.Ticket, err)
	}
	if review, err := agent.ReadReview(worktree); err != nil {
		logf("[%s] (warn) no review.json - skipping self-review gate", t.Ticket)
	} else if review.Verdict == "fix" && len(review.Issues) > 0 {
		logf("[%s] self-review: %d issue(s) - applying one fix round", t.Ticket, len(review.Issues))
		fixOpts := implOpts
		fixOpts.ResumeID = sessionID
		if fixRes, ferr := agent.Run(ctx, agent.BuildReviewFixPrompt(review.Issues), fixOpts); ferr != nil {
			logf("[%s] (warn) fix round exited: %v", t.Ticket, ferr)
		} else if fixRes.SessionID != "" {
			sessionID = fixRes.SessionID
			saveSession(sessionID)
		}
	} else {
		logf("[%s] self-review: pass ✓", t.Ticket)
	}

	if out, done := stopped(ctx, t, logf); done {
		return out
	}

	// 6. Verify. Sync with the emulator and acquire it first (the agent runs any
	// instrumented tests against it). Neither the boot nor the lease is allowed to
	// block the run indefinitely: both degrade to unit tests instead.
	if needsEmu {
		logf("[%s] waiting for emulator…", t.Ticket)
		if err := waitForBoot(ctx, emulatorReady); err != nil {
			logf("[%s] (warn) emulator unavailable: %v - falling back to unit tests", t.Ticket, err)
			needsEmu = false
		} else if !acquireEmulator(ctx, t.Store, avdName, t.Ticket, logf) {
			logf("[%s] (warn) emulator still busy after %s - falling back to unit tests",
				t.Ticket, emulatorWaitTimeout)
			needsEmu = false
		} else {
			defer func() { _, _ = t.Store.ReleaseEmulator(avdName, t.Ticket) }()
			logf("[%s] emulator acquired", t.Ticket)
		}
	}

	// VERIFY - the agent discovers and runs this project's own build + tests
	// itself (per-project Gradle setups and company build skills all work), fixes
	// failures, and certifies the result in report.json. Apple Pie trusts that
	// verdict instead of running hardcoded Gradle commands. Because the agent's
	// process inherits the real environment, this also sidesteps the isolated-Gradle
	// TLS/cert failures the orchestrator-run gate hit on locked-down machines.
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	logf("[%s] stage:verify - agent will discover & run build + tests (emulator=%v)", t.Ticket, needsEmu)
	vreport, verified, sid, denials, verifyErrText := verifyWithAgent(ctx, t, worktree, sessionID, needsEmu, true, anthropicKey, logf, setState)
	if sid != "" {
		sessionID = sid
		saveSession(sessionID)
	}
	// A cancelled verify leaves no fresh report, which would otherwise be read as
	// "the agent could not get the build green" and park the ticket in NEEDS YOU.
	if out, done := stopped(ctx, t, logf); done {
		return out
	}
	report = adoptVerifyReport(report, vreport, t.Ticket, logf)
	if !verified {
		// Refused permissions and a red build demand opposite human responses:
		// the first is fixed in config (one keystroke in the dashboard), the
		// second in the code. Blaming the build for a permissions problem sent
		// humans down the wrong road - never again.
		if len(denials) > 0 {
			return denialNeedsYou(t, h, worktree, denials)
		}
		// No denials and an API failure: the agent never got to run the build -
		// "not green" would blame the code for an account problem.
		if vreport == nil {
			return verifyDidNotRun(t, h, worktree, verifyErrText)
		}
		reason := "the agent could not get the build and tests green."
		if vreport != nil && strings.TrimSpace(vreport.VerifyLog) != "" {
			reason = vreport.VerifyLog
		}
		return needsYou(t, h, "agent did not certify the build (verified=false)",
			fmt.Sprintf(
				"The agent ran this project's build and tests but could not certify them green:\n\n{code}\n%s\n{code}\n\nOpen the worktree in Android Studio to investigate:\n`open -a \"Android Studio\" %s`",
				tail(reason, 1500), worktree))
	}
	logf("[%s] verify: agent certified build + tests green ✓", t.Ticket)
	if t.Store != nil {
		_ = t.Store.SetDenials(t.Ticket, "", false) // a green verify clears stale denials
	}

	// The review-before-PR gate: verified, but a human reads the change before
	// anything leaves the machine.
	if shouldParkChange(t) {
		return parkChangeReview(t, h, worktree, report, 1)
	}

	// 7-9. Commit, push, open PR (shared with the resume path).
	return finishShip(ctx, t, h, worktree, branch, defaultCommitMsg(t), report, logf, setState)
}
