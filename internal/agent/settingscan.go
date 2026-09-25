// Locating WHICH settings file blocked a denied command. Flavor() says an ask
// or deny rule fired; this scan says where it lives, so the park message can
// name the exact rule and file instead of sending the user log-spelunking.
// Read-only by design - pie never edits the user's Claude settings.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SettingsLayer is one Claude Code settings file, in precedence order.
type SettingsLayer struct {
	File    string
	Managed bool // org-managed: not editable by the user
}

// BlockingRule is a settings rule that matches a denied command.
type BlockingRule struct {
	Rule    string // e.g. "Bash(./gradlew:*)"
	Kind    string // "ask" or "deny"
	File    string
	Managed bool
}

// DefaultSettingsLayers returns the settings files Claude Code layers for a
// session in this worktree, highest precedence first, existing files only.
// (Precedence here means blame order for the message - org policy first.)
func DefaultSettingsLayers(worktree string) []SettingsLayer {
	var candidates []SettingsLayer
	switch runtime.GOOS {
	case "darwin":
		candidates = append(candidates, SettingsLayer{File: "/Library/Application Support/ClaudeCode/managed-settings.json", Managed: true})
	default:
		candidates = append(candidates, SettingsLayer{File: "/etc/claude-code/managed-settings.json", Managed: true})
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, SettingsLayer{File: filepath.Join(home, ".claude", "settings.json")})
	}
	if worktree != "" {
		candidates = append(candidates,
			SettingsLayer{File: filepath.Join(worktree, ".claude", "settings.json")},
			SettingsLayer{File: filepath.Join(worktree, ".claude", "settings.local.json")})
	}
	var out []SettingsLayer
	for _, c := range candidates {
		if _, err := os.Stat(c.File); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// FindBlockingRule scans the layers in order for an ask/deny rule matching the
// denial and returns the first hit, or nil. Missing or garbled files are
// skipped - this is a best-effort diagnosis, never a gate.
func FindBlockingRule(layers []SettingsLayer, d Denial) *BlockingRule {
	for _, layer := range layers {
		raw, err := os.ReadFile(layer.File)
		if err != nil {
			continue
		}
		var doc struct {
			Permissions struct {
				Ask  []string `json:"ask"`
				Deny []string `json:"deny"`
			} `json:"permissions"`
		}
		if json.Unmarshal(raw, &doc) != nil {
			continue
		}
		// Deny first within a layer: it is the stronger verdict.
		for _, group := range []struct {
			kind  string
			rules []string
		}{{"deny", doc.Permissions.Deny}, {"ask", doc.Permissions.Ask}} {
			for _, rule := range group.rules {
				if ruleMatchesDenial(rule, d) {
					return &BlockingRule{Rule: rule, Kind: group.kind, File: layer.File, Managed: layer.Managed}
				}
			}
		}
	}
	return nil
}

// AllowlistPermits reports whether an allowlist string (EffectiveAllowedTools
// shape) permits this tool call outright: non-Bash tools need their bare name
// present; a Bash command needs EVERY compound segment matched by some
// Bash(...) rule (prefix on a token boundary, or exact), mirroring the CLI's
// per-segment check. It is the approval callback's auto-allow test - the
// human already pre-approved these shapes when they allowlisted them.
func AllowlistPermits(allowed, tool, command string) bool {
	toks := allowRuleToken.FindAllString(allowed, -1)
	if tool != "Bash" {
		for _, t := range toks {
			if t == tool {
				return true
			}
		}
		return false
	}
	if command == "" {
		return false
	}
	bashRule := func(seg string) bool {
		for _, t := range toks {
			if strings.HasPrefix(t, "Bash(") && strings.HasSuffix(t, ")") &&
				segMatchesRuleInner(t[5:len(t)-1], seg) {
				return true
			}
		}
		return false
	}
	// An exact whole-command rule allows the compound as one unit.
	if bashRule(command) {
		return true
	}
	segs := splitSegments(command)
	if len(segs) == 0 {
		return false
	}
	for _, seg := range segs {
		if !bashRule(seg) {
			return false
		}
	}
	return true
}

// segMatchesRuleInner matches one command segment against a rule's inner text:
// "prefix:*" on a token boundary, otherwise exact.
func segMatchesRuleInner(inner, seg string) bool {
	if prefix, ok := strings.CutSuffix(inner, ":*"); ok {
		return seg == prefix || strings.HasPrefix(seg, prefix+" ")
	}
	return seg == inner
}

// ruleMatchesDenial approximates Claude Code's rule matching closely enough
// for diagnosis: a bare tool name matches every use of that tool; a
// Tool(prefix:*) rule matches any compound segment whose command starts at
// that prefix on a token boundary; a Tool(exact) rule matches only the whole
// verbatim command.
func ruleMatchesDenial(rule string, d Denial) bool {
	open := strings.IndexByte(rule, '(')
	if open < 0 {
		return rule == d.Tool
	}
	if !strings.HasSuffix(rule, ")") || rule[:open] != d.Tool {
		return false
	}
	inner := rule[open+1 : len(rule)-1]
	if strings.HasSuffix(inner, ":*") {
		for _, seg := range splitSegments(d.Command) {
			if segMatchesRuleInner(inner, seg) {
				return true
			}
		}
		return false
	}
	return d.Command == inner
}
