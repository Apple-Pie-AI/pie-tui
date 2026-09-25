// Why a command was refused, not just that it was. The same permission_denials
// entry can mean four different things - a missing allowlist rule, an explicit
// deny in the user's own Claude settings, an ask rule reaching for a human who
// isn't there, or the cd containment guard - and each demands a different
// remediation. The CLI never labels them; the only tell is the raw tool_result
// error text, which each cause phrases differently.
package agent

import "strings"

// DenialFlavor is the classified cause of one Denial.
type DenialFlavor string

const (
	// FlavorAllowlistGap: a command (or compound segment) matched no allow
	// rule. The one flavor a pie config change can fix.
	FlavorAllowlistGap DenialFlavor = "allowlist-gap"
	// FlavorDenyRule: an explicit deny rule in the user's/org's Claude Code
	// settings. Absolute - deny rules block even the permission callback.
	FlavorDenyRule DenialFlavor = "deny-rule"
	// FlavorAskRule: an ask rule in the user's/org's Claude Code settings
	// wanted interactive confirmation. Headless, that auto-denies unless a
	// permission callback answers - no flag overrides it (proven: it beats
	// --allowed-tools, bypassPermissions, and --dangerously-skip-permissions).
	FlavorAskRule DenialFlavor = "ask-rule"
	// FlavorCdContainment: Claude Code's own working-directory guard refused a
	// cd outside the session's allowed directories.
	FlavorCdContainment DenialFlavor = "cd-containment"
	// FlavorHumanDenied: the human pressed Deny on the approval prompt. Not a
	// config gap and not a CLI quirk - a person said no, and the park must
	// say that instead of "configuration issue". (There is no timed-out
	// sibling: a pending prompt waits for the human indefinitely - pie never
	// answers a permission question on the user's behalf.)
	FlavorHumanDenied DenialFlavor = "human-denied"
	// FlavorUnknown: no error text was captured, or the text matches no known
	// fingerprint (e.g. a CLI update rephrased them - the repro-tagged suite
	// exists to catch that).
	FlavorUnknown DenialFlavor = "unknown"
)

// CallbackDeniedText is pie's own callback verdict, fingerprinted exactly
// like the CLI's errors: approve.Decide writes this string and Flavor reads
// it back. Reword both together or classification silently degrades to
// Unknown - which is how a human's deliberate Deny got reported as
// "Configuration issue - one keystroke fixes it" during QA.
const CallbackDeniedText = "denied from the pie dashboard"

// Fingerprints observed verbatim on claude 2.1.232 (repro'd 2026-08-19 against
// a live CLI; see repro_flavors_test.go). Substring-matched so minor wording
// drift around them does not silently reclassify to Unknown.
const (
	fpAllowlistGap  = "requires approval"
	fpDenyRule      = "has been denied"
	fpAskRule       = "haven't granted it yet"
	fpCdContainment = "was blocked"
)

// Flavor classifies this denial from its captured error text.
func (d Denial) Flavor() DenialFlavor {
	e := d.ErrorText
	switch {
	case e == "":
		return FlavorUnknown
	// Pie's own verdict first: it is an exact string we wrote ourselves,
	// so it can never be a rephrased CLI message.
	case strings.Contains(e, CallbackDeniedText):
		return FlavorHumanDenied
	case strings.Contains(e, fpAskRule):
		return FlavorAskRule
	case strings.Contains(e, fpDenyRule):
		return FlavorDenyRule
	case strings.Contains(e, fpAllowlistGap):
		return FlavorAllowlistGap
	case strings.HasPrefix(e, "cd in ") && strings.Contains(e, fpCdContainment):
		return FlavorCdContainment
	default:
		return FlavorUnknown
	}
}

// DominantFlavor is the flavor the park message should lead with when a run
// carries mixed denials: the first classified one wins (stream order mirrors
// what the agent hit first), falling back to Unknown.
func DominantFlavor(denials []Denial) DenialFlavor {
	for _, d := range denials {
		if f := d.Flavor(); f != FlavorUnknown {
			return f
		}
	}
	return FlavorUnknown
}
