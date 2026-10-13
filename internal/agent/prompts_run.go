// Run app's one prompt: repairing a deterministic step that failed. There is
// no discovery prompt - Run app derives everything the way Android Studio
// does (see internal/runapp) - so an agent is only asked when that failed.
package agent

import (
	"fmt"
	"strings"
)

// RunTools is the repair session's allowlist: read the project, list Gradle
// tasks and build, query the device, check the JDK - and write only the
// contract. Edits are denied outright (RunSettingsJSON); a repair fixes how
// the app is RUN, never its code.
const RunTools = "Write Read Glob Grep TodoWrite " +
	"Bash(ls:*) Bash(cat:*) Bash(head:*) Bash(tail:*) Bash(grep:*) Bash(rg:*) Bash(find:*) " +
	"Bash(./gradlew:*) Bash(adb:*) Bash(java -version:*) Bash(/usr/libexec/java_home:*) " +
	"Bash(git status:*) Bash(git log:*)"

// RunDenyRules are refused even if the user's settings would allow them.
var RunDenyRules = []string{"Edit", "MultiEdit", "NotebookEdit",
	"Bash(adb uninstall:*)", "Bash(git commit:*)", "Bash(git push:*)", "Bash(git checkout:*)", "Bash(git reset:*)"}

// RunFailure is the step that failed, as the repair prompt reports it.
type RunFailure struct {
	Step, Explanation string
	Argv              []string
	ExitCode          int
	Tail              string
}

// RunRepairInput is everything the repair prompt needs.
type RunRepairInput struct {
	ProjectDir, Serial, Module, Variant string
	RecipeJSON                          string // the current recipe, as pie stores it
	Failure                             RunFailure
}

// BuildRunRepairPrompt asks the agent to fix how the app is run - and to say
// so honestly when the project itself is broken.
func BuildRunRepairPrompt(in RunRepairInput) string {
	return Prompts{}.RunRepair(in)
}

// RunRepair is BuildRunRepairPrompt with this workflow's slots.
func (p Prompts) RunRepair(in RunRepairInput) string {
	argv := strings.Join(in.Failure.Argv, " ")
	if argv == "" {
		argv = "(not a single command)"
	}
	return fmt.Sprintf(`Apple Pie runs this Android app on a device the way Android Studio's Run button does: it lists the app modules and variants from "./gradlew tasks --all", builds with ":<module>:assemble<Variant>", reads the applicationId and APK from build/outputs/apk/**/output-metadata.json, removes the old app, installs the APK with adb, and starts the LAUNCHER activity.

One of those steps just failed. Your job is to find out WHY and, if it is a problem with HOW the app is run, tell Apple Pie how to run it. Do NOT change any file in the project - you are not fixing the code.

Project (Gradle) directory: %s
App module: %s · variant: %s
Device: %s (ANDROID_SERIAL is set; always pass -s %s to adb)

FAILED STEP: %s
Command: %s
Exit code: %d
%s
Last output lines:
%s

The current run recipe (what Apple Pie already knows):
%s

%s%s

Then write %s/.agent/run.json with exactly one of:
- {"status":"ok", ...corrections...} when the problem is how the app is run. Corrections (omit what doesn't change):
  "gradle_dir": "<the Gradle root, relative to the project dir, if it isn't the project dir>",
  "gradle_args": ["<plain flags only, e.g. -Pflavor=staging>"],
  "env": {"JAVA_HOME": "<path>", "GRADLE_OPTS": "<value>"} (no other keys),
  "assemble_task": ":<module>:assemble<Variant>",
  "application_id": "<the installed package>",
  "launch_activity": "<package>/<fully.qualified.Activity>",
  "notes": "<what you learned, for next time>",
  "explanation": "<one or two sentences for the developer>"
- {"status":"source_error","explanation":"<file:line and what is wrong>"} when the project's own code or tests are broken (a compile error, a bug). Change nothing.
- {"status":"blocked","explanation":"<why>","suggested_command":"<what the developer could run>"} when it needs a human or something destructive (wiping the device, clearing data, signing keys). Never uninstall, wipe or clear data yourself.`,
		in.ProjectDir, in.Module, in.Variant, in.Serial, in.Serial,
		in.Failure.Step, argv, in.Failure.ExitCode, explanationLine(in.Failure.Explanation),
		indentBlock(in.Failure.Tail), indentBlock(in.RecipeJSON),
		strings.ReplaceAll(p.slot(SlotRunInvestigate), "{serial}", in.Serial), p.extra("run"), in.ProjectDir)
}

func explanationLine(s string) string {
	if s == "" {
		return ""
	}
	return "What Apple Pie saw: " + s + "\n"
}

func indentBlock(s string) string {
	if strings.TrimSpace(s) == "" {
		return "    (none)"
	}
	return "    " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n    ")
}
