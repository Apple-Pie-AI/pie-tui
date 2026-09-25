package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	in := &Config{
		JiraBaseURL:     "https://x.atlassian.net",
		JiraEmail:       "me@x.com",
		Concurrency:     4,
		ModelCommentFix: "claude-opus-5",
		Repos: []Repo{{
			Path:   "/repo",
			Branch: "ai/{ticket}-{slug}",
			Base:   "main",
		}},
	}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.JiraBaseURL != in.JiraBaseURL || got.JiraEmail != in.JiraEmail || got.Concurrency != 4 {
		t.Errorf("scalars round-trip failed: %+v", got)
	}
	if got.ModelCommentFix != "claude-opus-5" {
		t.Errorf("model_comment_fix round-trip failed: %q", got.ModelCommentFix)
	}
	if len(got.Repos) != 1 || got.Repos[0].Path != "/repo" || got.Repos[0].Base != "main" {
		t.Errorf("repo round-trip failed: %+v", got.Repos)
	}
}

// parseSandbox unmarshals SandboxSettingsJSON into a struct we can assert on.
type sbWire struct {
	Sandbox struct {
		Enabled bool `json:"enabled"`
		Network *struct {
			AllowedDomains []string `json:"allowedDomains"`
		} `json:"network"`
		Filesystem *struct {
			AllowWrite []string `json:"allowWrite"`
		} `json:"filesystem"`
	} `json:"sandbox"`
}

func parseSandbox(t *testing.T, s string) sbWire {
	t.Helper()
	var w sbWire
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		t.Fatalf("SandboxSettingsJSON is not valid JSON (%q): %v", s, err)
	}
	return w
}

// The default (zero) config disables the sandbox for Apple Pie's runs and emits
// no network/filesystem allowlists.
func TestSandboxSettingsJSONDefaultDisabled(t *testing.T) {
	c := &Config{}
	w := parseSandbox(t, c.SandboxSettingsJSON())
	if w.Sandbox.Enabled {
		t.Error("default config should disable the sandbox for Apple Pie runs")
	}
	if w.Sandbox.Network != nil || w.Sandbox.Filesystem != nil {
		t.Errorf("disabled sandbox should carry no allowlists, got %+v", w.Sandbox)
	}
}

// When enabled, the payload keeps the sandbox on and merges baked-in Gradle
// defaults with the user's extra domains/paths (with ~ expanded).
func TestSandboxSettingsJSONEnabledMergesDefaults(t *testing.T) {
	c := &Config{
		Sandbox: Sandbox{
			Enabled:        true,
			AllowedDomains: []string{"artifactory.example.com"},
			AllowWrite:     []string{"~/.m2"},
		},
	}
	w := parseSandbox(t, c.SandboxSettingsJSON())
	if !w.Sandbox.Enabled {
		t.Fatal("enabled config should keep the sandbox on")
	}
	if w.Sandbox.Network == nil || w.Sandbox.Filesystem == nil {
		t.Fatalf("enabled sandbox must carry allowlists, got %+v", w.Sandbox)
	}
	domains := strings.Join(w.Sandbox.Network.AllowedDomains, ",")
	for _, want := range []string{"repo.maven.apache.org", "dl.google.com", "artifactory.example.com"} {
		if !strings.Contains(domains, want) {
			t.Errorf("allowedDomains missing %q: %v", want, w.Sandbox.Network.AllowedDomains)
		}
	}
	writes := strings.Join(w.Sandbox.Filesystem.AllowWrite, ",")
	if !strings.Contains(writes, ".gradle") {
		t.Errorf("allowWrite missing the ~/.gradle default: %v", w.Sandbox.Filesystem.AllowWrite)
	}
	if strings.Contains(writes, "~/.m2") || !strings.Contains(writes, ".m2") {
		t.Errorf("user allow_write should be present and ~-expanded: %v", w.Sandbox.Filesystem.AllowWrite)
	}
}

// model_verify falls back to the implementation model when unset - the knob
// exists so certification can run on a stronger model than implementation.
func TestEffectiveModelVerify(t *testing.T) {
	c := &Config{ModelImpl: "haiku"}
	if got := c.EffectiveModelVerify(); got != "haiku" {
		t.Errorf("unset: got %q, want the impl model", got)
	}
	c.ModelVerify = "opus"
	if got := c.EffectiveModelVerify(); got != "opus" {
		t.Errorf("set: got %q, want the verify override", got)
	}
}

// model_comment_fix falls back to the implementation model when unset - the
// knob exists so review-comment fixes can run on a different model than the
// ticket pipeline.
func TestEffectiveModelCommentFix(t *testing.T) {
	c := &Config{ModelImpl: "haiku"}
	if got := c.EffectiveModelCommentFix(); got != "haiku" {
		t.Errorf("unset: got %q, want the impl model", got)
	}
	c.ModelCommentFix = "opus"
	if got := c.EffectiveModelCommentFix(); got != "opus" {
		t.Errorf("set: got %q, want the comment-fix override", got)
	}
}

func TestEffectiveAllowedTools(t *testing.T) {
	cases := []struct {
		name    string
		allowed string
		extra   string
		want    string
	}{
		{"default", "", "", DefaultAllowedTools},
		{"default plus extra", "", "Bash(sh:*)", DefaultAllowedTools + " Bash(sh:*)"},
		{"user override", "Read Bash(make:*)", "", "Read Bash(make:*)"},
		{"user override plus extra", "Read Bash(make:*)", "Bash(sh:*)", "Read Bash(make:*) Bash(sh:*)"},
		{"extra whitespace trimmed", "", "  Bash(sh:*)  ", DefaultAllowedTools + " Bash(sh:*)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{AllowedTools: tc.allowed, ExtraAllowedTools: tc.extra}
			if got := c.EffectiveAllowedTools(); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A config carrying the exact pre-broadening default in allowed_tools was baked
// there by an old TUI save, not authored - it must be treated as unset so the
// user receives the broadened default. A hand-tuned list must be respected.
func TestLegacyBakedAllowlistTreatedAsUnset(t *testing.T) {
	c := &Config{AllowedTools: legacyAllowedTools}
	c.applyDefaults()
	if c.AllowedTools != "" {
		t.Fatalf("legacy baked list should reset to empty, got %q", c.AllowedTools)
	}
	if got := c.EffectiveAllowedTools(); got != DefaultAllowedTools {
		t.Errorf("legacy config should get the new default, got %q", got)
	}

	custom := &Config{AllowedTools: "Read Bash(make:*)"}
	custom.applyDefaults()
	if custom.AllowedTools != "Read Bash(make:*)" {
		t.Errorf("hand-tuned list must survive applyDefaults, got %q", custom.AllowedTools)
	}
}

func TestExtraAllowedToolsRoundTrip(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Config{ExtraAllowedTools: "Bash(sh:*) Bash(make:*)"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ExtraAllowedTools != "Bash(sh:*) Bash(make:*)" {
		t.Errorf("extra_allowed_tools round-trip failed: %q", got.ExtraAllowedTools)
	}
}

// The new default must cover the command shapes the old one denied in
// production (the PLEX-57812 class): gradle, chmod +x, adb, java.
func TestDefaultAllowedToolsCoversBuildCommands(t *testing.T) {
	for _, rule := range []string{
		"Bash(./gradlew:*)", "Bash(gradle:*)", "Bash(cd:*)", "Bash(chmod +x:*)",
		"Bash(adb:*)", "Bash(java:*)",
	} {
		if !strings.Contains(DefaultAllowedTools, rule) {
			t.Errorf("DefaultAllowedTools missing %q", rule)
		}
	}
}

func TestNormalizeAllowedTools(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty stays empty", "", ""},
		{"exact current default unbaked", DefaultAllowedTools, ""},
		{"respaced default unbaked", "  " + strings.ReplaceAll(DefaultAllowedTools, " Bash", "  Bash") + " ", ""},
		{"custom list trimmed and kept", "  Read Bash(make:*)  ", "Read Bash(make:*)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NormalizeAllowedTools(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// A config carrying the CURRENT default verbatim (copy-pasted, or baked by a
// future form) is a bake, not an authored list - applyDefaults must unfreeze
// it so the user keeps receiving default upgrades. The legacy literal keeps
// its own check; hand-tuned lists survive untouched.
func TestApplyDefaultsUnbakesCurrentDefault(t *testing.T) {
	c := &Config{AllowedTools: DefaultAllowedTools}
	c.applyDefaults()
	if c.AllowedTools != "" {
		t.Fatalf("current default not unbaked: %q", c.AllowedTools)
	}
	custom := &Config{AllowedTools: "Read Bash(make:*)"}
	custom.applyDefaults()
	if custom.AllowedTools != "Read Bash(make:*)" {
		t.Errorf("hand-tuned list mangled: %q", custom.AllowedTools)
	}
}
