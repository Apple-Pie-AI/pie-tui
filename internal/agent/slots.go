package agent

import "strings"

// Customizable prompts. Each stage prompt is mostly contract - the .agent/
// JSON it must write, tool permissions, the budget and device rules, the
// verified semantics - which the orchestrator reads back and so must never
// change. Woven through it are a few passages of plain guidance (who the agent
// is, what a good review looks for, how to approach verification). Those are
// Slots: a team can rewrite them per repo from the workflow editor, and every
// stage also takes an empty "extra" slot for team instructions.
//
// Slot text reaches a prompt only as a %s argument, so the builders' format
// strings stay constants that go vet checks (Decision 4), and a stray % in a
// team's text is printed literally.

// Slot is one customizable passage of a stage prompt.
type Slot struct {
	ID      string `json:"id"`
	Stage   string `json:"stage"`
	Title   string `json:"title"`
	Help    string `json:"help"`
	Default string `json:"default"`
	// For the editor (see slots_guide.go): what Apple Pie does when the slot
	// is left alone, in plain words, and an example customization.
	Behavior string `json:"behavior,omitempty"`
	Example  string `json:"example,omitempty"`
}

// Stage is one step of the workflow, as the editor shows it.
type Stage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Blurb string `json:"blurb"`
	// Fixed stages carry only your feedback and the contract around it, so
	// the editor shows them read-only and they take no team instructions.
	Fixed bool `json:"fixed,omitempty"`
}

// Stages are every prompt Apple Pie sends an agent.
var Stages = []Stage{
	{"plan", "Plan", "Reads the ticket and the code, then writes plan.json: the plan, any blocking questions, and whether it needs the emulator.", false},
	{"implement", "Implement", "Edits the code in the ticket's worktree to the approved plan and writes report.json.", false},
	{"self-review", "Self-review", "Reads the diff without building and decides pass or fix.", false},
	{"review-fix", "Fix round", "Sends self-review's issues back to the implementation session to fix. It carries only the issues and the report.json reminder.", true},
	{"verify", "Verify", "Discovers and runs this project's own build and tests on the reserved device, then certifies the result in report.json.", false},
	{"comments", "Fix comments", "Addresses the PR comments you picked in its worktree, locally, and drafts a reply to each reviewer.", false},
	{"comment-revise", "Revise fixes", "Sends your request-changes feedback on the previewed fixes back to the same session. It carries only your words and the file reminders.", true},
	{"discuss", "Discuss", "Answers your questions about the change in plan mode - it cannot edit. It carries only your message.", true},
	{"rework", "Rework", "Revises the local change to your feedback, then the change is verified again.", false},
	{"run", "Repair", "When a Run app step fails, finds out why and tells Apple Pie how to run the app - without touching the code.", false},
}

// MaxSlotLen caps one slot's text.
const MaxSlotLen = 8 << 10

// Markers bracket slot text in a marked prompt (the editor's preview).
const (
	SlotOpen    = "⟦slot:"
	SlotOpenEnd = "⟧"
	SlotClose   = "⟦/slot⟧"
)

// Slot IDs.
const (
	SlotPlanApproach      = "plan.approach"
	SlotPlanQuestions     = "plan.questions"
	SlotImplPersona       = "implement.persona"
	SlotImplRules         = "implement.rules"
	SlotImplPRBody        = "implement.pr_body"
	SlotReviewPersona     = "self-review.persona"
	SlotReviewCriteria    = "self-review.criteria"
	SlotReviewStrictness  = "self-review.strictness"
	SlotVerifyApproach    = "verify.approach"
	SlotCommentsPersona   = "comments.persona"
	SlotCommentsScope     = "comments.scope"
	SlotCommentsSkip      = "comments.skip"
	SlotCommentsVoice     = "comments.voice"
	SlotReworkPolicy      = "rework.policy"
	SlotRunInvestigate    = "run.investigate"
	extraSuffix           = ".extra"
	teamInstructionsTitle = "Extra team instructions"
)

// Slots is every customizable passage, in prompt order within each stage.
// Defaults are today's prompt text word for word: an untouched workflow
// produces exactly the prompts Apple Pie always sent.
var Slots = func() []Slot {
	base := []struct{ ID, Stage, Title, Help, Default string }{
		{SlotPlanApproach, "plan", "How to plan", "How the agent should go about understanding the code and planning. It stays read-only whatever this says.",
			"Understand the code and produce your implementation plan however you see fit,\nusing whatever tools and approach work best for you."},
		{SlotPlanQuestions, "plan", "When to ask questions", "When the plan should stop and ask you instead of assuming. An empty questions list always means proceed.",
			"only genuine ambiguities that block safe implementation - prefer a\n  reasonable assumption over a question."},
		{SlotImplPersona, "implement", "Engineer persona", "Who the agent is while implementing.",
			"You are an autonomous Android engineer implementing a pre-approved plan"},
		{SlotImplRules, "implement", "Coding rules", "The rules the change must follow: conventions, architecture, libraries to prefer or avoid.",
			"Follow the approved plan above. Make the focused, minimal change described."},
		{SlotImplPRBody, "implement", "PR description", `How to write the PR description. With no template the orchestrator still sets prBody to "" and uses ~/.pie/templates/pr_body.tmpl.`,
			`check whether this repo has a pull request template (.github/PULL_REQUEST_TEMPLATE.md, .github/pull_request_template.md, or docs/PULL_REQUEST_TEMPLATE.md). If one exists, fill it out for THIS change: EVERY heading, section, and checklist item MUST appear in prBody, in the same order. Never drop a section - fill each one with real content about THIS change. Keep every checklist item exactly as written: tick the boxes that apply, leave the rest unticked. Do not add sections, filler text, or commentary not in the template. Put the finished markdown in "prBody".`},
		{SlotReviewPersona, "self-review", "Reviewer persona", "Who reviews the change.",
			"You are a senior Android code reviewer adversarially reviewing"},
		{SlotReviewCriteria, "self-review", "What to review for", "The checklist the reviewer judges the diff against.",
			"   - Correctness: does the change actually solve the ticket as planned?\n   - Scope: did the agent change more than the plan described?\n   - Android conventions: proper Kotlin idioms, no deprecated APIs, correct threading?\n   - Safety: nullability issues, resource leaks, or crash risks?"},
		{SlotReviewStrictness, "self-review", "How strict to be", "The bar for a pass.",
			"Be an adversarial reviewer - only pass if the change is genuinely good."},
		{SlotVerifyApproach, "verify", "How to find the build", "Where to look for this project's build and test commands - your scripts, skills or CI config. The device, certification and BLOCKED rules stay fixed.",
			"Discover how this project builds and tests: inspect build.gradle(.kts), gradle.properties, settings.gradle, a Makefile, scripts/, README/CONTRIBUTING, CI config under .github/, and any company-provided build skill. No build/test commands are pre-configured - find them yourself."},
		{SlotCommentsPersona, "comments", "Engineer persona", "Who answers the review.",
			"You are an autonomous Android engineer responding to code review"},
		{SlotCommentsScope, "comments", "How much to change", "How far a fix may go beyond what the reviewer asked.",
			"Make the SMALLEST change that satisfies each comment. Do NOT refactor beyond\n   what was asked, and do NOT fix problems nobody commented on - unrelated\n   changes make a reviewer re-review the whole PR."},
		{SlotCommentsSkip, "comments", "When to push back", `When a comment should be skipped with an answer instead of done. A skipped comment is still recorded as "skipped".`,
			"If a comment is factually wrong, unsafe, or asks for something that would\n   break the build, SKIP it and say why. Do not silently do nothing, and do not\n   substitute a different change you prefer."},
		{SlotCommentsVoice, "comments", "Reply voice", "How the draft replies to reviewers read. You review every reply before it is posted.",
			`The "reply" is a draft the human will review and may rewrite before it is
posted. Address the reviewer directly, say what changed and where, and never
mention commit hashes (the commit does not exist yet) or that you are an AI.

A skipped comment's "note" can reach the reviewer the same way: when a comment
is a question, or needs no change, the note IS the reply the human may post on
the thread verbatim. Write it to the reviewer in the same voice as a "reply":
answer the question or say why no change is needed, directly and in a human
voice. Never narrate your triage ("this comment is a question, not a change
request", "no code was modified") - the reviewer asked about the code, not
about your process.`},
		{SlotReworkPolicy, "rework", "How much to change", "How far a revision may go beyond your notes.",
			"The smallest change that\nsatisfies each note"},
		{SlotRunInvestigate, "run", "How to investigate", "What to check first when a run step fails. {serial} becomes the device's serial.",
			`Investigate cheaply: read settings.gradle(.kts) and the module's build.gradle(.kts), run "./gradlew <module>:tasks --all", "java -version", "adb -s {serial} shell pm list packages". Run the build only if you must to prove a fix.`},
	}
	var out []Slot
	for _, st := range Stages {
		for _, s := range base {
			if s.Stage == st.ID {
				out = append(out, Slot{ID: s.ID, Stage: s.Stage, Title: s.Title, Help: s.Help, Default: s.Default})
			}
		}
		if st.Fixed {
			continue
		}
		out = append(out, Slot{ID: st.ID + extraSuffix, Stage: st.ID, Title: teamInstructionsTitle,
			Help: "Anything else this stage should know: house rules, tools, what to avoid. Added under TEAM INSTRUCTIONS."})
	}
	return out
}()

var slotDefaults = func() map[string]string {
	m := make(map[string]string, len(Slots))
	for _, s := range Slots {
		m[s.ID] = s.Default
	}
	return m
}()

// KnownSlot reports whether id names a slot.
func KnownSlot(id string) bool { _, ok := slotDefaults[id]; return ok }

// SlotDefault is a slot's built-in text ("" for an unknown id).
func SlotDefault(id string) string { return slotDefaults[id] }

// NormalizeSlotText is how override text is stored and compared: \r\n folded
// to \n, blank leading lines and trailing whitespace dropped (indentation on
// the first line is kept - some defaults start indented).
func NormalizeSlotText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimRight(strings.TrimLeft(s, "\n"), " \t\n")
}

// Prompts builds stage prompts with a repo's slot overrides. The zero value
// is Apple Pie's built-in workflow.
type Prompts struct {
	over map[string]string
	mark bool
}

// NewPrompts applies overrides (slot id → text). Unknown ids and blank text
// are ignored, so a stale override can never reach a prompt.
func NewPrompts(over map[string]string) Prompts {
	m := map[string]string{}
	for id, t := range over {
		if t = NormalizeSlotText(t); KnownSlot(id) && strings.TrimSpace(t) != "" {
			m[id] = t
		}
	}
	return Prompts{over: m}
}

// Marked returns the same prompts with every slot bracketed by SlotOpen/SlotClose.
func (p Prompts) Marked() Prompts { p.mark = true; return p }

// Customized reports whether any slot differs from its default.
func (p Prompts) Customized() bool {
	for id, t := range p.over {
		if t != slotDefaults[id] {
			return true
		}
	}
	return false
}

func (p Prompts) wrap(id, t string) string {
	if !p.mark {
		return t
	}
	return SlotOpen + id + SlotOpenEnd + t + SlotClose
}

// slot is a passage's text: the override, else the default.
func (p Prompts) slot(id string) string {
	t, ok := p.over[id]
	if !ok {
		t = slotDefaults[id]
	}
	return p.wrap(id, t)
}

// extra is a stage's team instructions under a fixed header - nothing at all
// when there are none, so an untouched prompt is unchanged.
func (p Prompts) extra(stage string) string {
	id := stage + extraSuffix
	t := p.over[id]
	if t == "" {
		if p.mark {
			return p.wrap(id, "")
		}
		return ""
	}
	return "\n\nTEAM INSTRUCTIONS for this stage:\n" + p.wrap(id, t)
}

// Block is one piece of a marked prompt: a slot's text (Slot set) or the
// locked text between slots.
type Block struct {
	Slot string `json:"slot,omitempty"`
	Text string `json:"text"`
}

// SplitSlots cuts a marked prompt into locked and slot blocks, in order.
func SplitSlots(marked string) []Block {
	var out []Block
	for marked != "" {
		i := strings.Index(marked, SlotOpen)
		if i < 0 {
			return append(out, Block{Text: marked})
		}
		if i > 0 {
			out = append(out, Block{Text: marked[:i]})
		}
		rest := marked[i+len(SlotOpen):]
		j := strings.Index(rest, SlotOpenEnd)
		k := strings.Index(rest, SlotClose)
		if j < 0 || k < j {
			return append(out, Block{Text: marked[i:]})
		}
		out = append(out, Block{Slot: rest[:j], Text: rest[j+len(SlotOpenEnd) : k]})
		marked = rest[k+len(SlotClose):]
	}
	return out
}

// StripSlotMarkers turns a marked prompt back into the real one.
func StripSlotMarkers(marked string) string {
	var b strings.Builder
	for _, bl := range SplitSlots(marked) {
		b.WriteString(bl.Text)
	}
	return b.String()
}
