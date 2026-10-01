// Model choices for the stage pickers: the /model list a company curates in
// Claude Code's own settings (modelPicker), Claude Code's family aliases, and
// a one-turn check of what a model name actually runs. Read-only - pie never
// edits the user's Claude settings.
package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// ModelAliases are Claude Code's family aliases, offered unless the settings
// replace the built-in lineup. Each resolves to the newest model in that
// family the account has, after any company remapping. fable is left out:
// not every account has it, and a curated list or a saved model brings it in
// where it exists.
var ModelAliases = []string{"opus", "sonnet", "haiku"}

// PickerModel is one modelPicker row: model is anything --model accepts (an
// alias, a model id, or a Bedrock/Vertex/gateway id); label and description
// are the company's own wording, empty when not given.
type PickerModel struct {
	Model       string `json:"model"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// ModelPicker is the curated list and where it came from.
type ModelPicker struct {
	Rows []PickerModel
	// ReplaceBuiltIns means /model shows only these rows, not Claude Code's
	// own lineup - pie then leaves out the aliases too.
	ReplaceBuiltIns bool
	File            string
}

// managedSettingsDir is where org-managed Claude Code settings live. A
// variable so tests can point it at a temp dir.
var managedSettingsDir = func() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/ClaudeCode"
	}
	return "/etc/claude-code"
}()

// modelPickerSources lists the settings files that may define modelPicker,
// highest precedence first: managed drop-ins (later names win), the managed
// file, then the user's settings. Claude Code never honors modelPicker from a
// project checkout, so the worktree's own settings are not read.
func modelPickerSources() []string {
	dropIns, _ := filepath.Glob(filepath.Join(managedSettingsDir, "managed-settings.d", "*.json"))
	sort.Sort(sort.Reverse(sort.StringSlice(dropIns)))
	out := append(dropIns, filepath.Join(managedSettingsDir, "managed-settings.json"))
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Join(home, ".claude", "settings.json"))
	}
	return out
}

// ClaudeModelPicker returns the modelPicker of the highest-precedence settings
// file that defines one - like Claude Code, no merging across sources. It
// returns false when none does. Settings Claude Code fetches from a server
// are not on disk and so not seen here; /model still shows those.
func ClaudeModelPicker() (ModelPicker, bool) {
	for _, file := range modelPickerSources() {
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var doc struct {
			ModelPicker *struct {
				Options               []PickerModel `json:"options"`
				ReplaceBuiltInOptions bool          `json:"replaceBuiltInOptions"`
			} `json:"modelPicker"`
		}
		if json.Unmarshal(raw, &doc) != nil || doc.ModelPicker == nil {
			continue
		}
		p := ModelPicker{ReplaceBuiltIns: doc.ModelPicker.ReplaceBuiltInOptions, File: file}
		for _, r := range doc.ModelPicker.Options {
			if r.Model = strings.TrimSpace(r.Model); r.Model != "" {
				p.Rows = append(p.Rows, r)
			}
		}
		return p, true
	}
	return ModelPicker{}, false
}

// ModelCheck is what CheckModel learned about a model name.
type ModelCheck struct {
	Resolved string // the model Claude Code actually ran, e.g. "claude-opus-4-6"
	Err      string // why it could not run, in Claude Code's words; "" on success
}

// Mismatch reports whether a model other than the requested family ran.
// Claude Code can swap a model its availableModels policy disallows for an
// allowed one instead of failing, so a run that succeeded is not proof the
// requested model was used.
func (c ModelCheck) Mismatch(requested string) bool {
	if c.Err != "" || c.Resolved == "" {
		return false
	}
	fam := modelFamily(requested)
	return fam != "" && fam != modelFamily(c.Resolved)
}

func modelFamily(model string) string {
	m := strings.ToLower(model)
	for _, f := range []string{"opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(m, f) {
			return f
		}
	}
	return ""
}

// CheckModel runs one minimal turn on model to learn whether it works and what
// it resolves to. A name Claude Code refuses fails in seconds at no cost; one
// it accepts costs a fraction of a cent to a few cents (no tools, a one-line
// system prompt). It runs outside any repo so no project settings apply, and
// authenticates like a ticket run.
func CheckModel(ctx context.Context, model, anthropicKey string) ModelCheck {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", "-p", "ok",
		"--model", model,
		"--output-format", "json",
		"--max-turns", "1",
		"--system-prompt", "Reply with ok.",
		"--no-session-persistence",
		"--tools", "")
	cmd.Dir = os.TempDir()
	cmd.Env = os.Environ()
	if anthropicKey != "" {
		cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+anthropicKey)
	}
	out, runErr := cmd.Output()
	return parseModelCheck(out, runErr)
}

func parseModelCheck(out []byte, runErr error) ModelCheck {
	var res struct {
		IsError    bool                       `json:"is_error"`
		Result     string                     `json:"result"`
		ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		if runErr != nil {
			return ModelCheck{Err: "could not run claude: " + runErr.Error()}
		}
		return ModelCheck{Err: "unreadable reply from claude"}
	}
	if res.IsError {
		msg := strings.TrimSpace(res.Result)
		if msg == "" {
			msg = "claude reported an error"
		}
		return ModelCheck{Err: msg}
	}
	var used []string
	for m := range res.ModelUsage {
		used = append(used, m)
	}
	sort.Strings(used)
	if len(used) == 0 {
		return ModelCheck{Err: "claude did not report which model ran"}
	}
	return ModelCheck{Resolved: strings.Join(used, ", ")}
}
