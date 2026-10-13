package workflows

import (
	"regexp"
	"strings"
)

// Lint is advice, never a block: text a slot holds can't remove the contract
// around it, but instructions that contradict it confuse the agent - which
// then follows one or the other.
var lintRules = []struct {
	re  *regexp.Regexp
	msg string
}{
	{regexp.MustCompile(`(?i)\.agent/|\b(plan|report|review|comment_fixes|run)\.json\b`),
		"Mentions an .agent/ contract file. Apple Pie already tells the agent exactly what to write there - restating it here risks a conflict."},
	{regexp.MustCompile(`(?i)\b(don'?t|do not|never|skip|no need to)\b[^.\n]{0,40}\b(writ(e|ing)|creat(e|ing)|updat(e|ing))\b[^.\n]{0,30}\b(report|json|file)`),
		"Tells the agent not to write a file. The stage can't finish without its contract file, so a run would stall or fail."},
	{regexp.MustCompile(`(?i)\b(git (commit|push)|open (a )?(pull request|pr)|gh pr)\b`),
		"Apple Pie owns commit, push and the PR - an agent told to do them is refused, and the attempt wastes budget."},
	{regexp.MustCompile(`(?i)"?verified"?\s*[:=]\s*true|always (mark|set|certify)[^.\n]{0,20}(verified|pass)`),
		"Asks for a verdict up front. Verify certifies only what it watched pass; a forced verdict ships unverified code."},
}

// Lint returns advice for one slot's text.
func Lint(text string) []string {
	var out []string
	for _, r := range lintRules {
		if r.re.MatchString(text) {
			out = append(out, r.msg)
		}
	}
	if strings.Contains(text, "⟦") {
		out = append(out, "The ⟦ ⟧ brackets are the editor's own markers - they will be sent to the agent as plain text.")
	}
	return out
}
