// The prompt corpus: one file for every instruction Apple Pie gives the agent, so
// the product's most behaviour-critical prose can be read and reviewed without
// the subprocess plumbing around it.
package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// toolPermissionsBlock renders the shared prompt section that tells the agent
// exactly which tools it may use. Commands outside the allowlist now pause
// for the repository owner's approval in the pie dashboard (the permission
// callback) instead of hard-failing - but a denied or unanswered request is
// still a refusal, so the shape guidance below stays load-bearing: without it
// agents discovered the boundary by burning tool calls against it (four
// denied attempts in one observed planning session).
func toolPermissionsBlock(allowedTools string) string {
	if allowedTools == "" {
		// Auto mode: a classifier judges each action; there is no static list
		// to teach, only the habits that keep commands judgeable.
		return `TOOL PERMISSIONS - a safety classifier reviews each command in context. Ordinary build, test and inspection commands pass; destructive or evasive ones do not. Keep commands plain: no wrapper scripts, no piping through tee, no VAR=value prefixes - prefer tool flags (e.g. -Dorg.gradle.java.home=<path> to select a JDK).`
	}
	return fmt.Sprintf(`TOOL PERMISSIONS - your Bash commands are checked against this prefix allowlist. A command outside it pauses while the repository owner is asked to approve it in their dashboard - for as long as that takes; if they deny it, it is refused - so prefer allowlisted shapes and treat approval as slow and expensive:
%s
The reliable command shapes, in order of preference:
1. A single plain command from the directory you are in: "./gradlew <task>".
2. One compound to run in a subdirectory: "cd <dir> && ./gradlew <task>".
Never use: VAR=value prefixes ("JAVA_HOME=... ./gradlew" - a prefix rule cannot match them; select a JDK with -Dorg.gradle.java.home=<path> instead), "export ... &&", wrapper scripts you wrote yourself, piping through tee (read Gradle's own output), or absolute paths to binaries. Each of those is a new string the allowlist has never seen, and retrying variations only burns attempts.
If a command you genuinely need is refused, do NOT keep rephrasing it. Write it in verifyLog on its own line as "BLOCKED: <the exact command>" - the command only, never a sentence describing the problem.`, allowedTools)
}

// budgetRule tells the agent the spending limit is not its to manage. Claude
// Code shows a session its --max-budget-usd and what is left of it on every
// turn (verified live: with the flag the model answers "$5 total, $5
// remaining", without it "no spending budget"), and agents ration against it:
// a field implement session (PLEX-64819) spent its $5 reading code, cut scope
// at "$2.6 of $5", stopped "before editing code" at $0.33 and reported
// needs_human with no change made. The harness pauses a session that reaches
// its limit and asks the human whether to continue it, intact - so stopping
// early to "save budget" only ever throws the work away.
const budgetRule = `SPENDING LIMIT - this session may show a spending budget and how much of it remains. Ignore it when deciding what to do. The orchestrator manages spend: if this session reaches its limit it is paused and the repository owner decides whether to continue it, with your context and work kept intact. Never reduce scope, skip planned work, stop early, or report needs_human because of budget - do the complete task as if no limit existed.`

// stageRules is the shared harness section of every stage prompt: the tool
// permissions, then the spending-limit rule.
func stageRules(allowedTools string) string {
	return toolPermissionsBlock(allowedTools) + "\n\n" + budgetRule
}

// BuildPlanPrompt is the instruction for the plan stage (read-only, strong model).
func BuildPlanPrompt(ticketKey, summary, description, allowedTools string) string {
	desc := strings.TrimSpace(description)
	if desc == "" {
		desc = "(no description provided)"
	}
	return fmt.Sprintf(`Plan the implementation of a ticket in this Android codebase.
Understand the code and produce your implementation plan however you see fit,
using whatever tools and approach work best for you. Do NOT edit any source
files - this is a planning step only.

%s

TICKET %s: %s

%s

When your plan is ready, write it to .agent/plan.json (create the .agent directory if needed):
{
  "plan": "<your full implementation plan, as markdown - any length and structure you consider right>",
  "questions": ["<question if truly blocking>"],
  "confidence": "high" | "medium" | "low",
  "type": "bug" | "feature" | "chore",
  "needs_emulator": true | false
}
The fields besides "plan" drive the orchestrator:
- questions: only genuine ambiguities that block safe implementation - prefer a
  reasonable assumption over a question. Empty array = proceed without human input.
  If the human left "Plan feedback" in the ticket text, treat it as the priority:
  revise the plan to address it directly.
- needs_emulator coordinates a shared Android emulator across tickets. Set it
  true ONLY when this change touches an actual INSTRUMENTED test - a file
  under androidTest/, a new or modified Espresso/Compose UI test, or the
  ticket text explicitly asks for connected/instrumented tests to run.
  Touching Compose or other UI source is NOT by itself a reason to set this
  true: a new Composable, a layout tweak, or a text/resource change needs
  only normal unit-test verification and should set this false, even though
  it "involves Compose." Set it false for domain logic, ViewModels,
  repositories, unit tests under test/, or pure Kotlin/Java changes.`,
		stageRules(allowedTools), ticketKey, summary, desc)
}

// BuildImplPrompt is the instruction for the implement stage, injecting the approved plan.
func BuildImplPrompt(ticketKey, summary, description string, plan *Plan, allowedTools, projectDir string) string {
	desc := strings.TrimSpace(description)
	if desc == "" {
		desc = "(no description provided)"
	}
	// The plan is free-form markdown. Legacy plans (pre-2.0 archives) may carry a
	// separate steps list - append it so nothing is lost on re-runs.
	planBlock := plan.Plan
	if len(plan.Steps) > 0 {
		planBlock += "\n\nIMPLEMENTATION STEPS:\n" + strings.Join(plan.Steps, "\n")
	}
	return fmt.Sprintf(`You are an autonomous Android engineer implementing a pre-approved plan inside an isolated git worktree (your current working directory).

%s

TICKET %s: %s

%s

APPROVED PLAN:
%s

Rules:
- Follow the approved plan above. Make the focused, minimal change described.
- This is an Android/Gradle project. A later verification step will build the project and run its tests - write code that compiles and passes.%s
- Do NOT git push and do NOT open a pull request - the orchestrator handles commit, push, and PR.
- PR description: check whether this repo has a pull request template (.github/PULL_REQUEST_TEMPLATE.md, .github/pull_request_template.md, or docs/PULL_REQUEST_TEMPLATE.md). If one exists, fill it out for THIS change: EVERY heading, section, and checklist item MUST appear in prBody, in the same order. Never drop a section - fill each one with real content about THIS change. Keep every checklist item exactly as written: tick the boxes that apply, leave the rest unticked. Do not add sections, filler text, or commentary not in the template. Put the finished markdown in "prBody". If there is NO template, set "prBody" to "".
- Your FINAL action MUST be to write .agent/report.json (create the .agent directory if needed):
{
  "status": "ready_for_build" | "needs_human",
  "summary": "<one line: what changed and why>",
  "filesChanged": ["<relative paths>"],
  "branch": "",
  "tests": "<what you checked, if anything>",
  "prBody": "<repo PR template filled in, or \"\" if the repo has none>"
}
Use "needs_human" if you hit an unexpected blocker you cannot resolve safely.`,
		stageRules(allowedTools), ticketKey, summary, desc, planBlock, implWhere(projectDir))
}

// implWhere names the project subdirectory in the implement prompt, or nothing
// when the build lives at the worktree root.
func implWhere(projectDir string) string {
	if projectDir == "" || projectDir == "." {
		return ""
	}
	return " The Gradle project root is `" + projectDir + "` inside this worktree - any build/test commands run there."
}

// BuildReviewPrompt is the instruction for the self-review stage. The review
// allowlist is deliberately minimal (ReviewTools: read + git inspection), so
// the prompt says so explicitly - an observed session burned three denied
// gradle attempts trying to "verify" before adapting, because nothing told it
// builds are out of scope here.
func BuildReviewPrompt(ticketKey, summary string, plan *Plan) string {
	return fmt.Sprintf(`You are a senior Android code reviewer adversarially reviewing changes made for ticket %s: %s

The approved plan was: %s

%s

This is a STATIC review: do NOT try to build or run tests - a separate verify stage does that with build permissions. Judge the diff by reading it.

Instructions:
1. Run "git diff HEAD" to see all changes made in this worktree.
2. Review for:
   - Correctness: does the change actually solve the ticket as planned?
   - Scope: did the agent change more than the plan described?
   - Android conventions: proper Kotlin idioms, no deprecated APIs, correct threading?
   - Safety: nullability issues, resource leaks, or crash risks?
3. Be an adversarial reviewer - only pass if the change is genuinely good.

Your ONLY write action is to create .agent/review.json:
{
  "verdict": "pass" | "fix",
  "issues": ["<specific, actionable issue>"]
}
Use "pass" if the change correctly implements the plan with no significant issues.
Use "fix" with a list of specific problems that must be corrected.`,
		ticketKey, summary, plan.Plan, stageRules(ReviewTools))
}

// BuildReviewFixPrompt feeds self-review issues back into the implement session.
func BuildReviewFixPrompt(issues []string) string {
	list := strings.Join(issues, "\n- ")
	return fmt.Sprintf(`A self-review found the following issues with your implementation. Fix each one, then rewrite .agent/report.json as your final step.

Issues to fix:
- %s`, list)
}

// BuildChangeReworkPrompt is the change-review gate's feedback round: the
// human read the local change before any PR exists and left notes. It resumes
// the implementation session (context still holds the plan and the edits), so
// it carries only the notes and the contract reminder.
func BuildChangeReworkPrompt(feedback string, round int) string {
	return fmt.Sprintf(`The repository owner reviewed your local change before creating a pull request
(this is round %d of that review) and asks for the following. "@path" and
"@path:line" tokens are already worktree-relative paths (and, where given, a
line number in the file's CURRENT content) - they need no further resolution,
just read them:

%s

Revise the code in this worktree accordingly. The smallest change that
satisfies each note - no commit, no push, no pull request; the owner ships
only after another review.

Then rewrite .agent/report.json: read the existing file and update "summary"
and "filesChanged" for the change AS IT NOW STANDS, preserving every other
field ("prBody" especially) exactly.`, round, strings.TrimSpace(feedback))
}

// BuildChangeDiscussPrompt is the change-review gate's Plan-mode round: the
// human wants to talk through the change before deciding whether to send it
// back for a rework. It resumes the same implementation session (context
// still holds the plan and the edits) under --permission-mode plan, which
// already refuses every edit tool at the CLI level - this prompt only sets
// the right tone, so the reply reads like an answer to the human, not
// operator narration about what it would do if it could edit.
func BuildChangeDiscussPrompt(message string) string {
	return fmt.Sprintf(`The repository owner wants to talk through this change before deciding what
to do next - they are NOT asking you to make any edits right now (and you
cannot: this turn is read-only). "@path" and "@path:line" tokens are already
worktree-relative paths (and, where given, a line number in the file's
CURRENT content) - they need no further resolution, just read them.

Answer directly, in your own voice, as if replying to the person who asked:

%s`, strings.TrimSpace(message))
}

// BuildVerifyPrompt has the session discover and RUN this project's own build +
// test verification, then record the verdict in report.json. The agent
// self-certifies - Apple Pie trusts report.verified rather than running hardcoded
// Gradle commands, so per-project build setups and company-managed build skills
// all work, and the agent's process uses the real environment (no isolated-Gradle
// TLS/cert problems). The agent discovers the build/test commands itself. When
// allowFix is false it only confirms a human's existing fix and must not edit
// code (the resume path). emulator tells it whether instrumented tests can run;
// adbPath, when non-empty, is the resolved adb binary - naming it stops an
// agent whose shell lacks platform-tools on PATH from reading `command not
// found: adb` as "no emulator in this environment" (PLEX-59644).
// worktree is the absolute worktree root: the session's working directory is
// the PROJECT dir (a monorepo subdirectory when projectDir is set), so the
// .agent contract must be named by absolute path - a relative ".agent/" would
// land inside the project dir where the orchestrator never looks.
func BuildVerifyPrompt(ticketKey, summary, worktree string, emulator, allowFix bool, allowedTools, projectDir, adbPath string) string {
	device := "No emulator/device is attached - run UNIT tests only; do NOT run instrumented/connected androidTest tasks."
	if emulator {
		device = "An Android emulator is booted and attached via adb - also run the instrumented/connected tests."
		if adbPath != "" {
			device += " adb is at " + adbPath + " (its directory is on your PATH); if a device check fails, use that absolute path rather than concluding no emulator exists."
		}
	}
	fixRule := `4. If the build or any test fails, FIX the code and re-run until everything passes. Set "verified": true ONLY after you have actually seen the build AND tests pass.
   THE CHANGE UNDER VERIFICATION IS THE DELIVERABLE: fix FORWARD, never by reverting or weakening the change itself (run "git diff" first so you know exactly what it is). If a test depends on the old behavior, updating that test to the new behavior IS the forward fix. If the ONLY way to a green build is undoing the change, STOP: set "verified": false and explain the conflict in verifyLog - name the failing test/file and why it conflicts - so a human decides. A green build that no longer contains the change verifies nothing.`
	if !allowFix {
		fixRule = `4. Do NOT modify any source files - a human has already made the fix and you are only confirming it. Run the build + tests and report the result honestly.`
	}
	whereBuilds := ""
	if projectDir != "" && projectDir != "." {
		// Verified against the real CLI (2.1.145 and 2.1.246): a prefix rule
		// Bash(<dir>/gradlew:*) allows `<dir>/gradlew -p <dir> <task>`, while a
		// `cd <dir> && ./gradlew` compound is refused even by a rule quoting it
		// verbatim, and absolute paths never match anything. So exactly one
		// shape is sanctioned, and the verify spawn derives its rule.
		whereBuilds = "\nThe build lives in the `" + projectDir + "` subdirectory of this worktree (your working directory is the worktree root). Run every Gradle command through the wrapper BY RELATIVE PATH with the project flag, exactly this shape:\n  " + projectDir + "/gradlew -p " + projectDir + " <task>\nThat shape is allowlisted as Bash(" + projectDir + "/gradlew:*). Do NOT `cd` into the directory first, and do NOT use an absolute path to gradlew - neither matches the allowlist.\n"
	}
	reportPath := filepath.Join(worktree, ".agent", "report.json")
	return fmt.Sprintf(`You are verifying that the change for ticket %s (%s) actually builds and passes its tests, inside an isolated git worktree.

%s
%s
Apple Pie does NOT run the build for you - YOU discover and run it. Android/Gradle setups differ per project and some teams ship their own build/test scripts or skills, so figure out the right way to verify THIS repo:
1. Discover how this project builds and tests: inspect build.gradle(.kts), gradle.properties, settings.gradle, a Makefile, scripts/, README/CONTRIBUTING, CI config under .github/, and any company-provided build skill. No build/test commands are pre-configured - find them yourself.
2. Compile the project. %s
3. Run the tests. Capture the REAL pass/fail result - never assume it passed. In a Kotlin Multiplatform (multiplatform plugin) project, prefer the targeted JVM/Android unit-test tasks (e.g. testDebugUnitTest, jvmTest, :module:testDebugUnitTest) over a full "build" - a full build drags in iOS/native link tasks that fail or hang for environment reasons unrelated to this change.
%s
- Do NOT git commit, git push, or open a pull request - the orchestrator owns commit/push/PR.
- ENVIRONMENT PROBLEMS ARE REPORTED, NEVER PATCHED: if Java, the SDK, or a tool is missing or refused, do NOT modify build configuration to work around it - no writing gradle-daemon-jvm.properties, no editing gradle.properties/toolchain/wrapper files, no changing JVM criteria. Those files are committed into the team's PR and change everyone's build. Report the obstacle instead (see BLOCKED below) and let a human fix the environment.
- If a command you genuinely need is refused by the allowlist, do NOT keep retrying variations. Record each refused command in verifyLog on its own line prefixed exactly "BLOCKED: ", set "verified": false, and state clearly that verification was blocked - not that the build is red.
- CERTIFY ONLY WHAT YOU WATCHED FINISH: "verified": true requires that every build and test command you deem required ran to completion IN THIS SESSION with a passing result you saw. A task still running in the background, a test suite you started but never saw finish, or a step you skipped is NOT verified - name it in verifyLog as not run and certify only what completed. Certifying "tests green" after a compile-only session sends unverified code into a PR under a green checkmark.
- PARTIAL EVIDENCE IS NOT VERIFICATION: one test's logcat, one green result file, or any intermediate artifact is not a suite's outcome - only the suite's own final summary or exit status counts. NEVER write the report while a build or test task is still running: wait for it to finish, or certify "verified": false naming what you could not wait for. If ANY test in a suite you ran failed, "verified" is false - even when the failures look unrelated to this change and the test you care about passed. "My test passed; the other failures look pre-existing" is a note for verifyLog, and the SHIP decision it argues for belongs to the human reading it - never to you.
- Your FINAL action MUST be to update the report at %s: read the existing file and rewrite it preserving every existing field, adding/setting:
  "verified": true | false   (true ONLY if the build AND tests actually passed)
  "verifyLog": "<the commands you ran and their outcome; on failure include the failing output tail and why; refused commands as BLOCKED: lines>"
Use that absolute path exactly - a relative .agent/ from your working directory is the wrong place.
Setting "verified": false is a normal outcome (not an error) when you cannot get it green - it routes the ticket to a human.`,
		ticketKey, summary, stageRules(allowedTools), whereBuilds, device, fixRule, reportPath)
}
