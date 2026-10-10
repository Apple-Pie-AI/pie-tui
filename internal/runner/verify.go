// The verify stage. Apple Pie does not run the build itself: the agent discovers
// and runs this project's own build and tests, then self-certifies the result in
// report.json, and the orchestrator trusts that verdict. What the orchestrator
// does NOT trust is a bare "not verified" - it distinguishes "the build is red"
// from "the agent was not allowed to run the build", because the two demand
// opposite human responses.
package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	// Aliased: verifyWithAgent's `emulator bool` parameter shadows the name.
	emusdk "github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// verifyWithAgent has the Claude session itself discover and run this project's
// build + tests, then reads back the verdict the agent wrote into report.json.
// When allowFix is true the agent may edit code to make it pass; when false
// (resume) it only confirms a human's existing fix and must not edit. emulator
// tells the agent whether instrumented tests can run. Returns the refreshed
// report (nil if the agent left none), whether the agent certified success, the
// (possibly new) Claude session id, and every command the permission system
// refused - the stream's permission_denials merged with the BLOCKED: lines
// older claude CLIs leave in verifyLog.
func verifyWithAgent(ctx context.Context, t Task, worktree, sessionID string,
	emulator, allowFix bool, anthropicKey string,
	logf func(string, ...interface{}), setState func(string, int)) (*agent.Report, bool, string, []agent.Denial, string) {

	// Nothing below may run on a cancelled run, and the reason is the very first
	// statement: setState(building). A run cancelled from the TUI has already had
	// needs-you (Pause) or stopped (Stop) written for it, and recording "building"
	// on the way out would clobber that with a state whose driver is already dead.
	// The check lives here rather than only at the call sites because the two are
	// not atomic - the pipeline can be cancelled between its stage guard and this.
	// The PR body the implement/fix agent filled from the repo's template,
	// snapshotted BEFORE verify runs: the verify agent is instructed to
	// preserve prBody but nothing used to enforce it, and one that rewrote it
	// with its verification narrative shipped that narrative as a brand-new
	// PR's description (PLEX-60299). Verify owns the verdict, never the body.
	var prBodyBefore string
	if pre, err := agent.ReadReport(worktree); err == nil && pre != nil {
		prBodyBefore = pre.PRBody
	}
	if ctx.Err() != nil {
		return nil, false, sessionID, nil, ""
	}
	setState(store.StateBuilding, 0)
	allowed := t.Cfg.EffectiveAllowedTools()
	vModel := t.Cfg.EffectiveModelVerify()
	mode := t.Cfg.PermissionModeFor(vModel)
	if mode == "auto" {
		// Auto mode has no allowlist; a blank one makes the prompt describe
		// the classifier instead of a list that is not being enforced.
		allowed = ""
	}
	logf("[%s] stage:verify model:%s permissions:%s", t.Ticket, vModel, mode)
	// The agent's shell must see the same SDK pie boots the emulator from:
	// Android Studio setups rarely have platform-tools on PATH, and a bare
	// `adb devices` exiting 127 once made an agent misread a booted emulator
	// as "no emulator in this environment" (PLEX-59644).
	sdkRoot := emusdk.RootFor(t.Cfg.AndroidSDKPath)
	adbPath := ""
	if p := emusdk.ResolvePaths(t.Cfg.AndroidSDKPath).ADB; filepath.IsAbs(p) {
		adbPath = p
	}
	// Monorepo: the wrapper is not at the worktree root, and the allowlist's
	// Bash(./gradlew:*) never matches the shape the agent must use there.
	// Derive the one rule that does - Bash(<projectDir>/gradlew:*) - from the
	// repo's configured project path. Verified against the real CLI: this
	// prefix form is honored; a `cd X && ./gradlew` rule is not, even
	// verbatim. (Moving the session's cwd into the subdir instead killed the
	// CLI at launch on every monorepo ticket - PLEX-57773.)
	projectDir := relProjectDir(worktree, git.ProjectDirIn(worktree, t.Repo.Path))
	if projectDir != "" && projectDir != "." && mode != "auto" {
		allowed = strings.TrimSpace(allowed + " " + projectGradlewRule(projectDir))
	}
	vOpts := agent.Options{
		WorktreeDir:            worktree,
		AllowedTools:           allowed,
		PermissionMode:         mode,
		PermissionPromptConfig: approvalCallbackConfig(t, worktree, mode, logf),
		Model:                  vModel,
		MaxBudgetUSD:           t.Cfg.BudgetFor(config.StageVerify),
		AnthropicKey:           anthropicKey,
		ResumeID:               sessionID,
		SettingsJSON:           t.Cfg.SandboxSettingsJSON(),
		AndroidSDKRoot:         sdkRoot,
		Logf:                   logf,
	}
	newSession := sessionID
	// Truncated to the second so a coarse-granularity filesystem can't stamp the
	// report just below the threshold and make a genuinely fresh one look stale.
	stageStart := time.Now().Truncate(time.Second)
	res, err := runStage(ctx, t, "verify",
		agent.BuildVerifyPrompt(t.Ticket, t.Summary, worktree, emulator, allowFix, allowed, projectDir, adbPath),
		vOpts, logf)
	if err != nil {
		logf("[%s] (warn) verify stage exited: %v", t.Ticket, err)
	}
	if res.SessionID != "" {
		newSession = res.SessionID
	}

	rep, rerr := agent.ReadReportSince(worktree, stageStart)
	if rep != nil && strings.TrimSpace(prBodyBefore) != "" && rep.PRBody != prBodyBefore {
		logf("[%s] (warn) verify rewrote the PR body - restoring the template-filled one", t.Ticket)
		rep.PRBody = prBodyBefore
	}
	denials := res.Denials
	if rep != nil {
		denials = agent.MergeDenials(denials, agent.ParseBlockedLines(rep.VerifyLog))
	}
	for _, d := range denials {
		logf("[%s] ✗ permission denied: %s(%s)", t.Ticket, d.Tool, d.Command)
	}
	if rerr != nil {
		logf("[%s] (warn) verify produced no usable report.json (%v)", t.Ticket, rerr)
		errText := res.ErrorText
		if errText == "" {
			// The session ran but left no fresh report. Say that - every
			// gate reads an empty error text as "the build is red".
			errText = agent.NoReportPrefix + ": " + rerr.Error()
		}
		return nil, false, newSession, denials, errText
	}
	return rep, rep.Verified != nil && *rep.Verified, newSession, denials, res.ErrorText
}

// projectGradlewRule is the allowlist rule for a monorepo's wrapper, run by
// relative path from the worktree root: Bash(<projectDir>/gradlew:*).
func projectGradlewRule(projectDir string) string {
	return "Bash(" + filepath.ToSlash(projectDir) + "/gradlew:*)"
}

// verifyDidNotRun parks the ticket when verify produced no report at all - the
// CLI died at launch, or the session ended without writing one. Nothing was
// certified either way: the message must never call that a red build. The
// API-error case keeps its own copy (apiNeedsYou); everything else names the
// launch failure or the missing report, with the CLI's own stderr when there
// is one. Before this, PLEX-57773's ship-comments told the human "the build
// or tests failed" for a claude that never emitted one event.
func verifyDidNotRun(t Task, h Hooks, worktree, errText string) Outcome {
	switch {
	case strings.HasPrefix(errText, agent.LaunchFailurePrefix):
		return needsYou(t, h, "verify never started: "+oneLine(errText, 200),
			fmt.Sprintf("The verify stage could not start the Claude CLI, so the build and tests were NOT run - nothing is known to be red or green. The CLI said:\n\n{code}\n%s\n{code}\n\nYour changes are untouched in `%s`. Fix the CLI problem (often a settings/MCP/permissions config it refused, or a stale `claude` install), then retry the ticket.", errText, worktree))
	case errText != "" && !strings.HasPrefix(errText, agent.NoReportPrefix):
		return apiNeedsYou(t, h, "verify", errText)
	default:
		return needsYou(t, h, "verify produced no report: "+oneLine(errText, 200),
			fmt.Sprintf("The verify agent ran but did not write a fresh `.agent/report.json`, so nothing was certified - the build was NOT shown to be red.\n\n{code}\n%s\n{code}\n\nYour changes are untouched in `%s`. Check `pie logs %s` for what the agent did, then retry the ticket.", errText, worktree, t.Ticket))
	}
}

// adoptVerifyReport prefers the verify-stage report but keeps the
// implement-stage PR body when later stages dropped it: verifyWithAgent
// snapshots prBody AFTER the self-review fix round, so a fix round that
// rewrote report.json without carrying prBody forward (it's omitempty) leaves
// impl's body as the only surviving copy. Only an emptied body is restored -
// a non-empty rewrite may be the fix round legitimately updating it for its
// own changes (the verify agent's own rewrites are already reverted by the
// snapshot above).
func adoptVerifyReport(impl, vreport *agent.Report, ticket string, logf logFn) *agent.Report {
	if vreport == nil {
		return impl
	}
	if strings.TrimSpace(vreport.PRBody) == "" && impl != nil && strings.TrimSpace(impl.PRBody) != "" {
		logf("[%s] (warn) a later stage dropped the PR body - restoring the implement-stage one", ticket)
		vreport.PRBody = impl.PRBody
	}
	return vreport
}

// permissionExplanation renders the needs-you body for a verify that was
// refused permission to run its build. Kept deliberately short: the
// blocked commands shown once up top, then the fix, then at most one
// alternative - the reader is a stuck user, not a debugger. The {code}
// markers are Jira markup; the local comment logger strips them so terminal
// output stays clean.
//
// With no rules to suggest, the message is diagnosed rather than shrugged at:
// the denial's own error text (kept since the flavor work) says WHETHER an
// ask or deny rule in the user's Claude settings fired, and the settings scan
// says WHERE - the exact rule and file. The old message attributed every such
// refusal to "the command allowlist" and told the user no config change would
// help, which was true of pie's config and useless about the real one.
func permissionExplanation(denials []agent.Denial, rules []string, ticket, worktree string) string {
	var cmds []string
	for _, d := range denials {
		cmds = append(cmds, "  ✗ "+d.Command)
	}
	guarded := agent.GuardedCommands(denials)
	flavor := agent.DominantFlavor(denials)
	var b strings.Builder

	// Pie's own callback verdict gets its own copy: a human's Deny is a
	// decision, not a config gap, and the way forward is different from
	// every other flavor.
	switch flavor {
	case agent.FlavorHumanDenied:
		fmt.Fprintf(&b, "You denied the agent permission to run:\n{code}\n%s\n{code}\n\n", strings.Join(cmds, "\n"))
		b.WriteString("If that was intentional, open the worktree from the actions menu and handle the ticket by hand.\n\n")
		if len(rules) > 0 {
			b.WriteString("If it was a mistake: press Enter on the ticket and choose \"Allow denied commands & re-run\" to pre-approve them, or Resume and approve the prompts when they reappear.")
		} else {
			fmt.Fprintf(&b, "If it was a mistake: Resume the ticket (pie run %s --resume) and approve the prompts when they reappear.", ticket)
		}
		return b.String()
	}

	fmt.Fprintf(&b, "The agent was refused permission to run:\n{code}\n%s\n{code}\n\n", strings.Join(cmds, "\n"))
	if len(rules) == 0 {
		if len(guarded) > 0 {
			b.WriteString("These commands change git/branch state, which pie manages itself - allowing them is deliberately not offered. This usually means the worktree was not in the state the agent expected.\n\n")
			fmt.Fprintf(&b, "Check the session log to see what the agent was trying to do: `pie logs %s`", ticket)
			return b.String()
		}
		switch agent.DominantFlavor(denials) {
		case agent.FlavorAskRule:
			b.WriteString("Your Claude Code settings require interactive approval for these commands - pie's own allowlist already permits them, but an ask rule outranks it and a headless run has nobody to ask")
			writeBlockingRule(&b, denials, worktree)
			b.WriteString(".\n\nFix: re-run the ticket and answer the approval prompt in the pie dashboard when it appears (the agent now pauses on these instead of failing). Or open the session in Claude Code from the actions menu, approve and build there, then Resume.")
		case agent.FlavorDenyRule:
			b.WriteString("An explicit deny rule in your Claude Code settings blocks these commands")
			writeBlockingRule(&b, denials, worktree)
			b.WriteString(".\n\npie will not work around an explicit deny. If the rule is wrong, change it where it lives; if it is org policy, this ticket needs a human to run the build.")
		default:
			b.WriteString("They appear to already match the allowlist, so a config change would not help.\n\n")
			if raw := firstErrorText(denials); raw != "" {
				fmt.Fprintf(&b, "The CLI's own refusal was:\n{code}\n%s\n{code}\n\n", raw)
			}
			fmt.Fprintf(&b, "Check the session log for the underlying refusal (quoting, sandbox, or CLI issue): `pie logs %s`", ticket)
		}
		return b.String()
	}
	tomlLine := fmt.Sprintf("extra_allowed_tools = %q", strings.Join(rules, " "))
	b.WriteString("Fix: in the dashboard, press Enter on the ticket and choose \"Allow denied commands & re-run\".\n\n")
	fmt.Fprintf(&b, "Manual alternative: add the line below to %s (if extra_allowed_tools already exists, append the new rules inside its quotes), then re-run: pie run %s --resume\n{code}\n%s\n{code}\n",
		paths.Config(), ticket, boxed(tomlLine))
	b.WriteString("The new rules APPEND to the default allowlist (they do not replace it) and apply to every future run.")
	if len(guarded) > 0 {
		fmt.Fprintf(&b, "\n\nNot fixed by this: %s - pie manages git/branch state itself and will not suggest allowing it. Check `pie logs %s` if the agent still needs it.",
			strings.Join(guarded, "; "), ticket)
	}
	return b.String()
}

// firstBlockingRule is the first denied command's matching ask/deny rule from
// the settings layers, or nil. Best-effort diagnosis, never a gate.
func firstBlockingRule(denials []agent.Denial, worktree string) *agent.BlockingRule {
	layers := agent.DefaultSettingsLayers(worktree)
	for _, d := range denials {
		if r := agent.FindBlockingRule(layers, d); r != nil {
			return r
		}
	}
	return nil
}

// writeBlockingRule appends ` (rule X in FILE)` when the settings scan can
// name the blocker, plus an org-managed note when the file is not the user's
// to edit. Best-effort: no hit appends nothing.
func writeBlockingRule(b *strings.Builder, denials []agent.Denial, worktree string) {
	r := firstBlockingRule(denials, worktree)
	if r == nil {
		return
	}
	fmt.Fprintf(b, " (rule `%s` in %s", r.Rule, r.File)
	if r.Managed {
		b.WriteString(" - org-managed, not editable")
	}
	b.WriteString(")")
}

// firstErrorText is the first captured raw refusal, for the unknown-cause
// branch where showing the CLI's own words beats pie guessing.
func firstErrorText(denials []agent.Denial) string {
	for _, d := range denials {
		if d.ErrorText != "" {
			return d.ErrorText
		}
	}
	return ""
}

// boxed draws a box around one line so the thing to copy stands out even in a
// plain monochrome log - the terminal has no colors to lean on there.
func boxed(line string) string {
	w := len([]rune(line))
	return "┌" + strings.Repeat("─", w+2) + "┐\n" +
		"│ " + line + " │\n" +
		"└" + strings.Repeat("─", w+2) + "┘"
}

// denialNeedsYou is the shared "verify was blocked" exit: it persists the
// denials (with the fixability verdict the why-pane reads) and the parking
// flow (so a retry re-enters ship-comments/address-comments instead of the
// bare resume), then stops at needs-you with the flavor-diagnosed
// explanation. All verify call sites (fresh run, resume, ship-comments) and
// a blocked comment-fix end here when denials exist.
func denialNeedsYou(t Task, h Hooks, worktree string, denials []agent.Denial) Outcome {
	rules := agent.SuggestAllowRules(denials, t.Cfg.EffectiveAllowedTools())
	if t.Store != nil {
		_ = t.Store.SetDenials(t.Ticket, agent.MarshalDenials(denials), len(rules) > 0)
		_ = t.Store.SetParkedFlow(t.Ticket, flowFor(t))
	}
	// Auto mode has no allowlist: a denial there is the safety classifier's
	// judgement, and suggesting allowlist rules would promise a fix that the
	// mode ignores.
	if t.Cfg.PermissionModeFor(t.Cfg.EffectiveModelVerify()) == "auto" {
		var cmds []string
		for _, d := range denials {
			cmds = append(cmds, "  ✗ "+d.Command)
		}
		header := fmt.Sprintf("🤖 *The agent was blocked on [%s] - the safety classifier refused a command*", t.Ticket)
		body := fmt.Sprintf("The safety classifier (permissions = \"auto\") blocked the agent from running:\n{code}\n%s\n{code}\n\nThis is a per-command judgement, not a config gap - there is no allowlist to extend in auto mode. Check the session log for what the agent was doing: `pie logs %s`. If the command is genuinely needed, re-run after simplifying the ticket, or set permissions = \"allowlist\" in %s to use the explicit allowlist instead.",
			strings.Join(cmds, "\n"), t.Ticket, paths.Config())
		h.logf()("[%s] needs-you: verify blocked by the safety classifier (%d denial(s))", t.Ticket, len(denials))
		h.setState()(store.StateNeedsYou, 0)
		h.comment(header + "\n\n" + body)
		return Outcome{State: store.StateNeedsYou}
	}
	// Not the generic "needs your help" wrapper: this failure has a known cause,
	// and the header should promise exactly what the body delivers. Pie's own
	// callback verdicts outrank the rules check: a human's deliberate Deny (or
	// an unanswered prompt) is not a "configuration issue", even when an
	// allowlist rule technically exists that would pre-approve the command.
	header := fmt.Sprintf("🤖 *The agent was blocked on [%s]*", t.Ticket)
	switch {
	case agent.DominantFlavor(denials) == agent.FlavorHumanDenied:
		header = fmt.Sprintf("🤖 *You denied [%s]'s commands from the dashboard*", t.Ticket)
	case len(rules) > 0:
		header = fmt.Sprintf("🤖 *Configuration issue on [%s] - one keystroke fixes it*", t.Ticket)
	case agent.DominantFlavor(denials) == agent.FlavorAskRule:
		header = fmt.Sprintf("🤖 *Your Claude settings want a human to approve [%s]'s build commands*", t.Ticket)
	case agent.DominantFlavor(denials) == agent.FlavorDenyRule:
		header = fmt.Sprintf("🤖 *A deny rule in your Claude settings blocked [%s]*", t.Ticket)
	default:
		header = fmt.Sprintf("🤖 *The agent was blocked on [%s] - a config change will not fix it*", t.Ticket)
	}
	h.logf()("[%s] needs-you: verify blocked by permissions (%d denial(s), %s)", t.Ticket, len(denials), agent.DominantFlavor(denials))
	h.setState()(store.StateNeedsYou, 0)
	h.comment(header + "\n\n" + permissionExplanation(denials, rules, t.Ticket, worktree))
	return Outcome{State: store.StateNeedsYou}
}
