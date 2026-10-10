// The address-comments stage: applying human reviewers' feedback to a branch
// that already has an open PR.
//
// Kept separate from prompts.go because this stage answers to a different
// authority than the rest of the corpus - a person who has read the diff and
// asked for something specific, rather than a ticket the agent planned itself.
package agent

import (
	"fmt"
	"strconv"
	"strings"
)

// ReviewComment is the narrow view of a PR review thread the prompt needs.
// Declared here so this package depends on neither store nor review; the runner
// maps into it.
type ReviewComment struct {
	ID       string // opaque id the agent must echo back in comment_fixes.json
	Author   string
	Path     string // repo-root-relative; empty for a review's summary body
	Line     int
	Body     string
	DiffHunk string
	UserNote string // extra guidance the human typed in the hub
	Outdated bool   // anchored to a diff later commits moved past
}

// BuildCommentFixPrompt is the instruction for the address-comments stage.
//
// base is the branch the PR targets, used for the git diff that re-establishes
// what is under review - this stage often runs days after the code was written,
// with none of the original session's context.
func BuildCommentFixPrompt(ticketKey, summary, base, allowedTools string, cs []ReviewComment) string {
	if base == "" {
		base = "main"
	}
	var b strings.Builder
	for i, c := range cs {
		b.WriteString(commentBlock(i+1, c))
	}
	return fmt.Sprintf(`You are an autonomous Android engineer responding to code review on an OPEN pull request, inside its git worktree (your current working directory).

%s

TICKET %s: %s

The change has already been written, reviewed by humans, and pushed. Below is
every review comment you have been asked to address. Each one is a request from
a real reviewer, not a suggestion.

REVIEW COMMENTS:
%s
Instructions:
1. Run "git diff %s...HEAD" first to re-read the change under review. You did not
   necessarily write it, and the comments assume you have read it.
2. Address each comment above. File paths are relative to the repository root,
   which is your current working directory. Where a comment carries an OWNER
   INSTRUCTION, that instruction is how the owner wants it handled: follow it
   as written - it takes precedence over the reviewer's wording and over your
   own reading of the code or the git history.
3. Make the SMALLEST change that satisfies each comment. Do NOT refactor beyond
   what was asked, and do NOT fix problems nobody commented on - unrelated
   changes make a reviewer re-review the whole PR.
4. If a comment is factually wrong, unsafe, or asks for something that would
   break the build, SKIP it and say why. Do not silently do nothing, and do not
   substitute a different change you prefer.
5. Do NOT git commit, do NOT git push, and do NOT reply on GitHub - the
   orchestrator owns commit, push, PR and the reply to each reviewer.

Your FINAL actions MUST be to write BOTH of these files (create the .agent
directory if needed):

.agent/comment_fixes.json - one entry for EVERY comment listed above, none
omitted, using the exact id given in each block:
{
  "fixes": [
    {"id": "<id from the comment block>", "action": "fixed" | "skipped",
     "note": "<fixed: what you changed. skipped: your answer to the reviewer>",
     "reply": "<for fixed comments: a 1-2 sentence DRAFT reply to the reviewer>"}
  ]
}

The "reply" is a draft the human will review and may rewrite before it is
posted. Address the reviewer directly, say what changed and where, and never
mention commit hashes (the commit does not exist yet) or that you are an AI.

A skipped comment's "note" can reach the reviewer the same way: when a comment
is a question, or needs no change, the note IS the reply the human may post on
the thread verbatim. Write it to the reviewer in the same voice as a "reply":
answer the question or say why no change is needed, directly and in a human
voice. Never narrate your triage ("this comment is a question, not a change
request", "no code was modified") - the reviewer asked about the code, not
about your process.

.agent/report.json - read the existing file and rewrite it preserving every
field, updating "summary" and "filesChanged" to describe THIS round of changes:
{
  "status": "ready_for_build" | "needs_human",
  "summary": "<one line: what you changed in response to review>",
  "filesChanged": ["<relative paths>"],
  "branch": "",
  "tests": "<what you checked, if anything>",
  "prBody": "<preserve the existing value>"
}`,
		stageRules(allowedTools), ticketKey, summary, b.String(), base)
}

// BuildCommentRevisePrompt is the follow-up for a revise round: the human read
// the local fixes in the approve screen and asked for changes. It resumes the
// fix session (context still holds the comments and the edits), so it carries
// only the feedback and the contract reminder.
func BuildCommentRevisePrompt(feedback string) string {
	return fmt.Sprintf(`The repository owner reviewed your local fixes before shipping them and asks
for changes:

%s

Revise the code in this worktree accordingly. The same rules apply: smallest
change that satisfies the request, no commit, no push, no GitHub replies.

Then REWRITE .agent/comment_fixes.json (all entries, same schema and voice
rules as before - id, action, note, and a draft "reply" for each fixed
comment; a skipped comment's note is written TO THE REVIEWER, never a
narration of your triage) and update .agent/report.json's "summary" and
"filesChanged" for this round.`,
		strings.TrimSpace(feedback))
}

// commentBlock renders one comment for the prompt. Built with a strings.Builder
// rather than a nested Sprintf so the format strings above stay constant and
// go vet's printf checker keeps covering them (Decision 4).
func commentBlock(n int, c ReviewComment) string {
	var b strings.Builder
	b.WriteString("\n### [" + strconv.Itoa(n) + "] @" + c.Author)
	if c.Path != "" {
		b.WriteString(" — " + c.Path)
		if c.Line > 0 {
			b.WriteString(":" + strconv.Itoa(c.Line))
		}
	} else {
		b.WriteString(" — overall review comment (no specific file)")
	}
	b.WriteString("\nid: " + c.ID + "\n\n")

	for _, ln := range strings.Split(strings.TrimSpace(c.Body), "\n") {
		b.WriteString("> " + ln + "\n")
	}
	// The owner's instruction is the human's decision about this comment. It
	// used to trail the diff hunk as one "additional" line - a footnote the
	// agent read past (PLEX-56803: it re-derived its own fix from git history
	// instead). It leads, under its own heading, right after the reviewer's
	// words it qualifies.
	if note := strings.TrimSpace(c.UserNote); note != "" {
		b.WriteString("\nOWNER INSTRUCTION for this comment (from the repository owner, in the hub - " +
			"this is how to address it; it takes precedence over the reviewer's wording above):\n" +
			note + "\n")
	}
	if hunk := strings.TrimSpace(c.DiffHunk); hunk != "" {
		b.WriteString("\nThe diff this comment is anchored to:\n```\n" + hunk + "\n```\n")
	}
	if c.Outdated {
		b.WriteString("\nNOTE: this comment is anchored to an OUTDATED diff - later commits " +
			"have moved the code, so the line number above may be wrong. Use the diff hunk " +
			"to find what it refers to.\n")
	}
	return b.String()
}
