// What a headless run refused to do, and how to say yes next time. The
// permission system hard-denies any Bash command outside the allowlist with no
// human to ask; until these types existed the pipeline could not tell that
// apart from a red build, and the needs-you message blamed the wrong thing.
package agent

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Denial is one tool call the permission system refused during a headless run.
type Denial struct {
	Tool    string `json:"tool"`    // e.g. "Bash"
	Command string `json:"command"` // the exact refused input, verbatim (untruncated)
	// ErrorText is the raw tool_result error the CLI attached to this refusal,
	// joined by tool_use_id (capped, prefix-preserving - the classification
	// fingerprints sit at the front). Empty when the CLI emitted none or the
	// denial came from the BLOCKED: verifyLog fallback. Flavor() derives from it.
	ErrorText string `json:"errorText,omitempty"`
}

// Result is everything one headless claude run yields besides its process error.
type Result struct {
	SessionID string
	// Denials are the tool calls the permission system refused, deduped in
	// stream order, from the result event's permission_denials (claude 2.1.220+;
	// verified to contain ONLY true permission denials, not execution errors).
	Denials []Denial
	// ErrorText is the final result text when the run ended in an API-level
	// error (e.g. "API Error: Request rejected (429) - ExceededBudget"). Empty
	// on a normal run. It exists so a stage that produced no contract file can
	// report the real reason instead of guessing "too ambiguous".
	ErrorText string
	// Stderr is the tail of what the CLI wrote to its stderr - the only place
	// a claude that failed at launch explains itself.
	Stderr string
	// BudgetExceeded is set when the session stopped on --max-budget-usd
	// (see budget.go); ErrorText then carries the CLI's own budget message.
	BudgetExceeded bool
	// CostUSD is the session's total_cost_usd from its last result event.
	CostUSD float64
}

// blockedPrefix is the verifyLog convention the verify prompt asks the agent to
// use for commands it needed but could not run. It is the denial signal of last
// resort for claude CLIs that predate permission_denials.
const blockedPrefix = "BLOCKED:"

// ParseBlockedLines extracts "BLOCKED: <command>" lines from a verifyLog into
// the same Denial shape the stream parser produces, so both signal paths
// converge on one data structure and the remediation flow works even when the
// CLI reports no permission_denials.
//
// Only command-shaped remainders count. Agents also write prose after the
// prefix ("BLOCKED: Unable to execute the test script...") and a fabricated
// denial from a sentence became the allowlist rule Bash(Unable:*) in the
// field - garbage that then degraded matching on every later run.
func ParseBlockedLines(verifyLog string) []Denial {
	var out []Denial
	for _, line := range strings.Split(verifyLog, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, blockedPrefix) {
			continue
		}
		cmd := strings.TrimSpace(strings.TrimPrefix(line, blockedPrefix))
		if cmd != "" && looksLikeCommand(cmd) {
			out = append(out, Denial{Tool: "Bash", Command: cmd})
		}
	}
	return out
}

// commandToken is what the first word of a real shell command looks like:
// a binary name or path, not a capitalized English word or punctuation.
var commandToken = regexp.MustCompile(`^[A-Za-z0-9_./~-]+$`)

// looksLikeCommand reports whether a BLOCKED: remainder is a command rather
// than a sentence describing one. First token (after any VAR=value prefixes)
// must be a plausible binary; sentence punctuation at the end and page-length
// lines are prose.
func looksLikeCommand(cmd string) bool {
	if len([]rune(cmd)) > 200 {
		return false
	}
	if strings.HasSuffix(cmd, ".") || strings.HasSuffix(cmd, ",") {
		return false
	}
	fields := strings.Fields(cmd)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "=") {
		i++ // skip VAR=value prefixes, same as commandHead
	}
	return i < len(fields) && commandToken.MatchString(fields[i])
}

// MergeDenials combines denial lists (stream + BLOCKED fallback), deduplicating
// by tool+command while preserving first-seen order. A duplicate's ErrorText
// backfills the kept entry when the first sighting had none (the BLOCKED
// fallback never carries one; the stream entry might).
func MergeDenials(lists ...[]Denial) []Denial {
	idx := map[string]int{}
	var out []Denial
	for _, list := range lists {
		for _, d := range list {
			key := d.Tool + "\x00" + d.Command
			if i, ok := idx[key]; ok {
				if out[i].ErrorText == "" {
					out[i].ErrorText = d.ErrorText
				}
				continue
			}
			idx[key] = len(out)
			out = append(out, d)
		}
	}
	return out
}

// MarshalDenials encodes denials as the opaque JSON the session store keeps
// (the store doesn't import this package; runner is the codec at both ends).
// Empty in → empty string out, which is also the store's "no denials" value.
func MarshalDenials(denials []Denial) string {
	if len(denials) == 0 {
		return ""
	}
	b, err := json.Marshal(denials)
	if err != nil {
		return ""
	}
	return string(b)
}

// UnmarshalDenials is MarshalDenials' inverse; "" or garbage yields nil.
func UnmarshalDenials(s string) []Denial {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []Denial
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

// neverSuggest lists command heads whose denial must never become an
// allowlist suggestion. Git state (checkout, reset, ...) is the
// orchestrator's: pie creates the worktree and branch and owns commit and
// push, and an agent moving refs mid-pipeline corrupts that - a denial there
// is a guardrail working, not a config gap. rm and sudo are too destructive
// for a permanent prefix rule (extra_allowed_tools applies to every future
// run). The denial still surfaces; the remediation just refuses to recommend
// the rule.
var neverSuggest = map[string]bool{
	"git checkout":    true,
	"git switch":      true,
	"git reset":       true,
	"git restore":     true,
	"git clean":       true,
	"git stash":       true,
	"git rebase":      true,
	"git merge":       true,
	"git cherry-pick": true,
	"git commit":      true,
	"git push":        true,
	"rm":              true,
	"sudo":            true,
}

// GuardedCommands returns the denied commands SuggestAllowRules refused to
// map to rules because a segment hits the neverSuggest set. Callers use it to
// explain that the block was a deliberate guardrail, not a config gap.
func GuardedCommands(denials []Denial) []string {
	var out []string
	for _, d := range denials {
		if d.Tool != "Bash" {
			continue
		}
		for _, seg := range splitSegments(d.Command) {
			if neverSuggest[commandHead(seg)] {
				out = append(out, d.Command)
				break
			}
		}
	}
	return out
}

// allowRuleToken tokenizes an allowlist string into whole rules: a
// Bash(...) rule is one token even when its head is two words ("git diff"),
// which strings.Fields split - and the split halves matched nothing, so
// rules already in the defaults were re-suggested every round.
var allowRuleToken = regexp.MustCompile(`Bash\([^)]*\)|\S+`)

// knownTools is the closed set of non-Bash tool names a denial may suggest.
// Anything else in a Denial's Tool field is a parse artifact or a tool no
// permanent rule should bless, and suggesting it wrote arbitrary strings
// into extra_allowed_tools.
var knownTools = map[string]bool{
	"Skill": true, "WebFetch": true, "WebSearch": true, "Task": true,
	"NotebookEdit": true,
}

// SuggestAllowRules maps denied commands to the minimal allowlist additions
// that would permit them, skipping rules the current allowlist already
// carries and heads in the neverSuggest set. Compound Bash commands (&&, ||,
// |, ;) yield one rule per segment, because the permission system checks each
// segment independently. A non-Bash tool suggests the bare tool name, and
// only from the knownTools set. The result can be empty: a command can be
// denied for reasons no allowlist rule fixes (quoting, sandbox, CLI quirks)
// or ones no rule should fix (guarded heads), and callers must not pretend a
// config change would help then.
func SuggestAllowRules(denials []Denial, currentAllowed string) []string {
	have := map[string]bool{}
	for _, tok := range allowRuleToken.FindAllString(currentAllowed, -1) {
		have[tok] = true
	}
	var out []string
	add := func(rule string) {
		if rule != "" && !have[rule] {
			have[rule] = true
			out = append(out, rule)
		}
	}
	for _, d := range denials {
		if d.Tool != "Bash" {
			if knownTools[d.Tool] {
				add(d.Tool)
			}
			continue
		}
		for _, seg := range splitSegments(d.Command) {
			head := commandHead(seg)
			if head == "" || neverSuggest[head] || !plausibleHead(head) {
				continue
			}
			add("Bash(" + head + ":*)")
		}
	}
	// Fallback: every generalized head already matches the allowlist, yet the
	// CLI refused the commands anyway (compound-matching drift between claude
	// versions, or shapes a prefix rule cannot express). An exact-command rule
	// re-arms the one-key fix. Guarded denials never get one - the global gate
	// keeps a compound hiding "git checkout" out of the allowlist entirely.
	// Exact rules are whitespace-brittle by design (the agent rephrasing
	// "head -50" as "head -n 50" re-denies); the approval callback is the
	// backstop for what they cannot express.
	if len(out) == 0 && len(GuardedCommands(denials)) == 0 {
		for _, d := range denials {
			if d.Tool == "Bash" && exactRuleable(d.Command) {
				add("Bash(" + d.Command + ")")
			}
		}
	}
	return out
}

// RememberOptions returns the rules "Allow & remember" may offer for ONE
// pending approval: the exact-command rule and the generalized per-segment
// head rules, deduplicated within this command. Either can be empty; callers
// must offer only the non-empty ones and must write EXACTLY what they showed
// the human - never re-derive a broader or narrower rule after the fact.
//
// A guarded segment (git state, rm, sudo) or a prose-shaped command yields
// nothing at all: no remembered rule should ever cover those, exact or not.
// A single-segment command with no arguments (its whole text equals its
// head, e.g. "pwd") returns only the general form - an exact rule there
// would duplicate the prefix rule with nothing gained.
func RememberOptions(tool, command string) (exact string, general []string) {
	if tool != "Bash" {
		if knownTools[tool] {
			return "", []string{tool}
		}
		return "", nil
	}
	segs := splitSegments(command)
	if len(segs) == 0 {
		return "", nil
	}
	seen := map[string]bool{}
	for _, seg := range segs {
		head := commandHead(seg)
		if head == "" || neverSuggest[head] || !plausibleHead(head) {
			return "", nil // any guarded/implausible segment voids the whole offer
		}
		rule := "Bash(" + head + ":*)"
		if !seen[rule] {
			seen[rule] = true
			general = append(general, rule)
		}
	}
	if len(segs) == 1 && strings.TrimSpace(segs[0]) == commandHead(segs[0]) {
		return "", general // no-argument single command: exact would duplicate it
	}
	if exactRuleable(command) {
		exact = "Bash(" + command + ")"
	}
	return exact, general
}

// exactRuleable reports whether a denied command can safely become an
// exact-match rule: one line, sane length, no ')' (an inner ')' would break
// the Bash(...) tokenizers on every rule that follows it, in both pie and the
// config sanitizer), and every segment headed by a plausible binary - the
// same bar head rules clear, so prose ("Unable to execute...") and one-off
// absolute-path scripts stay out of the config exactly as before.
func exactRuleable(cmd string) bool {
	if cmd == "" || strings.ContainsAny(cmd, ")\n\r") || !looksLikeCommand(cmd) {
		return false
	}
	for _, seg := range splitSegments(cmd) {
		if head := commandHead(seg); head == "" || !plausibleHead(head) {
			return false
		}
	}
	return true
}

// plausibleHead rejects rule heads that could only have come from prose or a
// mangled parse: capitalized English words ("Unable"), absolute paths to
// one-off scripts, and anything with characters no binary name carries.
// Two-word heads (git <sub>, chmod +x) validate per word.
func plausibleHead(head string) bool {
	for _, w := range strings.Fields(head) {
		if w == "+x" { // chmod's mode token, the one non-name word we emit
			continue
		}
		if !commandToken.MatchString(w) {
			return false
		}
		if strings.HasPrefix(w, "/") {
			return false // an absolute path is this machine's, not a rule's
		}
		if r := w[0]; r >= 'A' && r <= 'Z' && !strings.Contains(w, "/") {
			// Binaries are lowercase; capitalized heads are prose - EXCEPT
			// relative paths, where the capital is a directory's (Compass/gradlew
			// on Android monorepos), and no English sentence contains a slash.
			return false
		}
	}
	return true
}

// splitSegments breaks a shell command on the operators the permission system
// splits on: && || | and ;. Quotes are respected so "echo 'a && b'" stays one
// segment.
func splitSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
			cur.WriteRune(r)
		case r == '"' && !inSingle:
			inDouble = !inDouble
			cur.WriteRune(r)
		case !inSingle && !inDouble && (r == ';' ||
			(r == '&' && i+1 < len(runes) && runes[i+1] == '&') ||
			(r == '|' && i+1 < len(runes) && runes[i+1] == '|')):
			flush()
			if r != ';' {
				i++ // skip the second & or |
			}
		case !inSingle && !inDouble && r == '|':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return segs
}

// commandHead returns the allowlist-relevant head of one command segment: the
// first token, skipping VAR=value environment prefixes (an env-prefixed
// command can't be matched by a prefix rule on the bare binary, but suggesting
// the binary is still the closest useful rule - paired with the prompt telling
// the agent to avoid env prefixes). "chmod +x ..." keeps its subcommand-style
// second token to match the shipped default rule shape.
func commandHead(seg string) string {
	fields := strings.Fields(seg)
	i := 0
	for i < len(fields) && strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "=") {
		i++ // skip VAR=value prefixes
	}
	if i >= len(fields) {
		return ""
	}
	head := fields[i]
	// Match the granularity of the shipped defaults: "chmod +x" and "git <sub>"
	// are two-token rules; everything else is one.
	if (head == "chmod" || head == "git") && i+1 < len(fields) {
		return head + " " + fields[i+1]
	}
	return head
}
