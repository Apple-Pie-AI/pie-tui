package agent

// What the workflow editor tells a person about each slot: what it controls,
// what Apple Pie does when it is left alone (in plain words, not the prompt
// text), and an example of a real customization.
type slotGuide struct{ help, behavior, example string }

var slotGuides = map[string]slotGuide{
	SlotPlanApproach: {"How the agent explores the code and builds its plan. Planning stays read-only whatever you write.",
		"The agent decides on its own how to explore the code and how to structure the plan.",
		"Start from the module's README and its tests. Find a similar existing feature and follow its structure. Plan in steps: files to change, new classes, tests to add."},
	SlotPlanQuestions: {"When the plan stops to ask you instead of making an assumption.",
		"It asks only when something truly blocks a safe implementation; otherwise it assumes and carries on.",
		"Always ask before changing a public API, a database schema or analytics events. For UI copy, assume and say so in the plan."},
	SlotImplPersona: {"Who the agent is while writing the code - its role and expertise.",
		"An autonomous Android engineer implementing the approved plan.",
		"You are a senior Android engineer on the Payments team, fluent in Kotlin, Jetpack Compose and Hilt"},
	SlotImplRules: {"The rules every change must follow: conventions, architecture, libraries to use or avoid.",
		"Follow the approved plan and keep the change focused and minimal.",
		"Follow the approved plan. Use Hilt for DI and StateFlow in ViewModels. Never add a dependency without saying why. Every new ViewModel gets a unit test."},
	SlotImplPRBody: {"How the agent writes the pull request description.",
		"It fills in your repo's PR template, section by section. With no template, Apple Pie uses its own (~/.pie/templates/pr_body.tmpl).",
		`fill in .github/pull_request_template.md. Under "Testing", list the exact Gradle tasks you ran. Put "prBody" in the report.`},
	SlotReviewPersona: {"Who reviews the change before it is verified.",
		"A senior Android reviewer, adversarial on purpose.",
		"You are a staff Android engineer on our architecture guild, adversarially reviewing"},
	SlotReviewCriteria: {"The checklist the reviewer judges the diff against. Keep the indented-list shape.",
		"Correctness, scope (nothing beyond the plan), Android conventions (Kotlin idioms, no deprecated APIs, threading) and safety (nullability, leaks, crashes).",
		"   - Correctness: does the change solve the ticket?\n   - Compose: stateless composables, hoisted state, stable parameters\n   - Architecture: no Android types in domain/"},
	SlotReviewStrictness: {"How high the bar is for a pass. A fail sends its issues back to the implementation for one fix round.",
		"It passes only changes that are genuinely good.",
		"Fail only for bugs, crashes or plan violations - style nits are notes, not a reason to fail."},
	SlotVerifyApproach: {"Where the agent looks to find how your project builds and runs its tests.",
		"It searches the Gradle files, Makefile, scripts/, README, CI config and any company build skill, and works it out.",
		"Run ./scripts/ci-test.sh - it is exactly what CI runs. Skip the :benchmark module."},
	SlotCommentsPersona: {"Who answers the reviewers' comments.",
		"An autonomous Android engineer responding to code review.",
		"You are the Android engineer who owns this feature, responding to code review"},
	SlotCommentsScope: {"How far a fix may go beyond what the reviewer asked.",
		"The smallest change that satisfies each comment - no refactors, no fixing things nobody mentioned.",
		"Fix exactly what was asked. If a comment points at a pattern, fix every occurrence in the files this PR already touches."},
	SlotCommentsSkip: {"When the agent pushes back instead of changing the code. A skipped comment still gets a reply.",
		"It skips comments that are wrong, unsafe or would break the build, and explains why.",
		"Skip style comments that contradict our .editorconfig and quote the rule. Never skip a comment from a code owner."},
	SlotCommentsVoice: {"How the draft replies to reviewers read. You see and can edit every reply before it is posted.",
		"Short and direct, in a human voice: what changed and where. Never mentions commits or being an AI.",
		"Reply in one or two sentences. Name the file you changed. No thanks-for-the-review filler."},
	SlotReworkPolicy: {"How far a revision may go beyond your notes.",
		"The smallest change that satisfies each of your notes.",
		"Do what each note asks, and also fix anything the note reveals in the same function"},
	SlotRunInvestigate: {"What the agent checks first when building, installing or launching the app fails. {serial} becomes the device's serial.",
		"It reads settings.gradle and the module's build file, lists the Gradle tasks, checks java -version and the installed packages, and builds only to prove a fix.",
		"This app needs JDK 17 from /opt/jdk17 and the -Pstaging flag. Check adb -s {serial} devices first."},
}

// extraExamples are per-stage examples for the team-instructions slot.
var extraExamples = map[string]string{
	"plan":        "Read docs/ARCHITECTURE.md first. Never plan changes in the legacy/ module.",
	"implement":   "Strings go in strings.xml, never hardcoded. Use our DesignSystem components, not raw Material ones.",
	"self-review": "Every new ViewModel needs a unit test under test/. Flag a missing one as fix.",
	"verify":      "Run ./gradlew lint too, and treat new lint errors as failures.",
	"comments":    "Bot comments (Sonar, CodeRabbit) about formatting: fix them silently, no long reply.",
	"rework":      "When I mention a file with @, change only that file unless I say otherwise.",
	"run":         "The staging flavor needs VPN; if a network call fails at launch, say so instead of retrying.",
}

func init() {
	for i := range Slots {
		s := &Slots[i]
		if g, ok := slotGuides[s.ID]; ok {
			s.Help, s.Behavior, s.Example = g.help, g.behavior, g.example
			continue
		}
		if ex, ok := extraExamples[s.Stage]; ok && s.Default == "" {
			s.Help = "Anything else this step should know. It is added under a TEAM INSTRUCTIONS heading, before the step's required output."
			s.Behavior = "Empty - nothing is added."
			s.Example = ex
		}
	}
}
