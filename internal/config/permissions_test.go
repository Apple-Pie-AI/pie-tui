// Permission-mode resolution and the extras sanitizer that repairs configs
// the old denial suggester polluted.
package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestAutoCapable(t *testing.T) {
	for model, want := range map[string]bool{
		"claude-fable-5":            true,
		"fable":                     true,
		"Claude-Fable-5":            true,
		"claude-opus-5":             true,
		"opus-5":                    true,
		"claude-opus-4-1-20250805":  false, // opus-4 predates the classifier gate
		"claude-sonnet-5":           false,
		"claude-haiku-4-5-20251001": false,
		"":                          false, // unknown default resolves conservatively
	} {
		if got := AutoCapable(model); got != want {
			t.Errorf("AutoCapable(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestPermissionModeFor(t *testing.T) {
	cases := []struct {
		name        string
		permissions string
		model       string
		want        string
	}{
		{"explicit allowlist wins over capable model", "allowlist", "claude-fable-5", "allowlist"},
		{"explicit auto wins over incapable model", "auto", "claude-haiku-4-5", "auto"},
		{"explicit bypass passes through", "bypass", "claude-haiku-4-5", "bypass"},
		{"empty picks auto for capable", "", "claude-fable-5", "auto"},
		{"empty picks allowlist for incapable", "", "claude-haiku-4-5", "allowlist"},
		{"garbage value falls back to model resolution", "yolo", "claude-haiku-4-5", "allowlist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Permissions: tc.permissions}
			if got := c.PermissionModeFor(tc.model); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// The field user's config after three allow-&-re-run rounds: valid rules mixed
// with prose-derived garbage. The sanitizer keeps the former, drops the latter.
func TestSanitizeExtraTools(t *testing.T) {
	clean, dropped := SanitizeExtraTools(
		`Skill Bash(tee:*) Bash(Unable:*) Bash(git diff:*) Bash(/Users/x/run-tests.sh:*) Whatever123! Bash()`)
	if clean != `Skill Bash(tee:*) Bash(git diff:*) Bash(/Users/x/run-tests.sh:*)` {
		t.Errorf("clean = %q", clean)
	}
	if want := []string{"Bash(Unable:*)", "Whatever123!", "Bash()"}; !reflect.DeepEqual(dropped, want) {
		t.Errorf("dropped = %v, want %v", dropped, want)
	}
}

func TestSanitizeExtraToolsEmptyAndClean(t *testing.T) {
	if clean, dropped := SanitizeExtraTools(""); clean != "" || dropped != nil {
		t.Errorf("empty in: got %q %v", clean, dropped)
	}
	in := "Bash(./gradlew:*) Skill"
	if clean, dropped := SanitizeExtraTools(in); clean != in || dropped != nil {
		t.Errorf("already-clean in: got %q %v", clean, dropped)
	}
}

// The pre-Skill/tee default stored verbatim in a config must be treated as
// unset so those users pick up the new defaults on upgrade.
func TestApplyDefaultsUpgradesPrevDefault(t *testing.T) {
	c := &Config{AllowedTools: prevDefaultAllowedTools}
	c.applyDefaults()
	if c.AllowedTools != "" {
		t.Errorf("previous default should be unbaked to empty, got %q", c.AllowedTools)
	}
	if got := c.EffectiveAllowedTools(); got != DefaultAllowedTools {
		t.Errorf("effective should be the new default, got %q", got)
	}
}

// applyDefaults scrubs polluted extras in place, which is what heals a field
// config on its next load.
func TestApplyDefaultsSanitizesExtras(t *testing.T) {
	c := &Config{ExtraAllowedTools: "Bash(Unable:*) Bash(tee:*)"}
	c.applyDefaults()
	if c.ExtraAllowedTools != "Bash(tee:*)" {
		t.Errorf("extras after defaults = %q", c.ExtraAllowedTools)
	}
}

// The exact-command rules "Allow & remember" writes must survive the
// Load->applyDefaults->SanitizeExtraTools round trip. Before validRuleToken
// learned the exact shape, the rule was saved, shown as allowed, and silently
// stripped on the very next Load - an invisible self-destruct.
func TestExactRuleSurvivesLoad(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	exact := "Bash(cd Compass && ./gradlew :compass:compileAgentStagingDebugKotlin 2>&1 | head -50)"
	if err := Save(&Config{ExtraAllowedTools: exact}); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.ExtraAllowedTools != exact {
		t.Fatalf("extra_allowed_tools after round trip = %q, want %q kept", c.ExtraAllowedTools, exact)
	}
	if !strings.Contains(c.EffectiveAllowedTools(), exact) {
		t.Errorf("effective allowlist lost the exact rule")
	}
}

// Exact rules are validated, not waved through: prose and multiline junk are
// still dropped, and prefix-rule validation is unchanged.
func TestValidRuleTokenExactRules(t *testing.T) {
	cases := []struct {
		tok  string
		want bool
	}{
		{"Bash(cd Compass && ./gradlew test)", true},
		{"Bash(JAVA_HOME=/x ./gradlew test)", true},
		{"Bash(ls)", true},
		{"Bash(Unable to execute the test script)", false}, // sentence opener
		{"Bash(git diff:*)", true},                         // prefix rules unchanged
		{"Bash(Unable:*)", false},
		{"Bash()", false},
	}
	for _, tc := range cases {
		if got := validRuleToken(tc.tok); got != tc.want {
			t.Errorf("validRuleToken(%q) = %v, want %v", tc.tok, got, tc.want)
		}
	}
	long := "Bash(" + strings.Repeat("x", 201) + ")"
	if validRuleToken(long) {
		t.Errorf("201-rune exact rule accepted")
	}
}

// SplitRules and ValidRule are the public contract an editor validates
// against; both must agree with the package's own sanitizer.
func TestSplitRulesAndValidRule(t *testing.T) {
	got := SplitRules("Skill Bash(git diff:*) Bash(cd App && ./gradlew test)")
	want := []string{"Skill", "Bash(git diff:*)", "Bash(cd App && ./gradlew test)"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SplitRules = %#v, want %#v", got, want)
	}
	for _, tok := range []string{"Skill", "Bash(git diff:*)", "Bash(cd App && ./gradlew test)"} {
		if !ValidRule(tok) {
			t.Errorf("ValidRule(%q) = false, want true", tok)
		}
	}
	for _, tok := range []string{"Bash(Unable to execute the script)", "Whatever123!", "Bash()"} {
		if ValidRule(tok) {
			t.Errorf("ValidRule(%q) = true, want false", tok)
		}
	}
}
