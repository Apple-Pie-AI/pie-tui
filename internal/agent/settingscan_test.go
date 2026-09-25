package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// T2.1 - an ask rule in a user-layer file matches a compound command per
// segment, and the scan names the rule, the file, and its editability.
func TestFindBlockingRuleAskInUserLayer(t *testing.T) {
	dir := t.TempDir()
	user := writeSettings(t, dir, "settings.json",
		`{"permissions":{"ask":["Bash(./gradlew:*)"]}}`)
	layers := []SettingsLayer{{File: user}}
	d := Denial{Tool: "Bash", Command: "cd Compass && ./gradlew :compass:compileAgentStagingDebugKotlin"}
	r := FindBlockingRule(layers, d)
	if r == nil {
		t.Fatal("no blocking rule found, want the ask rule matched per segment")
	}
	if r.Rule != "Bash(./gradlew:*)" || r.Kind != "ask" || r.File != user || r.Managed {
		t.Errorf("rule = %+v", *r)
	}
}

// T2.2 - a deny rule in the managed layer reports managed=true.
func TestFindBlockingRuleDenyInManagedLayer(t *testing.T) {
	dir := t.TempDir()
	managed := writeSettings(t, dir, "managed-settings.json",
		`{"permissions":{"deny":["Bash(adb:*)"]}}`)
	layers := []SettingsLayer{{File: managed, Managed: true}}
	r := FindBlockingRule(layers, Denial{Tool: "Bash", Command: "adb devices"})
	if r == nil || r.Kind != "deny" || !r.Managed {
		t.Fatalf("rule = %+v, want managed deny", r)
	}
}

// T2.3 - when two layers carry a matching rule, the higher-precedence file is
// the one reported.
func TestFindBlockingRulePrecedence(t *testing.T) {
	dir := t.TempDir()
	managed := writeSettings(t, dir, "managed.json", `{"permissions":{"ask":["Bash(./gradlew:*)"]}}`)
	user := writeSettings(t, dir, "user.json", `{"permissions":{"ask":["Bash(./gradlew:*)"]}}`)
	layers := []SettingsLayer{{File: managed, Managed: true}, {File: user}}
	r := FindBlockingRule(layers, Denial{Tool: "Bash", Command: "./gradlew test"})
	if r == nil || r.File != managed || !r.Managed {
		t.Fatalf("rule = %+v, want the managed layer reported first", r)
	}
}

// T2.4 - no match anywhere (including missing/garbled files) yields nil.
func TestFindBlockingRuleNoMatch(t *testing.T) {
	dir := t.TempDir()
	user := writeSettings(t, dir, "settings.json", `{"permissions":{"ask":["Bash(rm:*)"]}}`)
	garbled := writeSettings(t, dir, "broken.json", `{not json`)
	layers := []SettingsLayer{
		{File: filepath.Join(dir, "missing.json")},
		{File: garbled},
		{File: user},
	}
	if r := FindBlockingRule(layers, Denial{Tool: "Bash", Command: "./gradlew test"}); r != nil {
		t.Errorf("rule = %+v, want nil", r)
	}
}

// T2.5 - exact rules (no :*) match only the exact whole command; a bare tool
// name matches every use of that tool.
func TestFindBlockingRuleExactAndBareTool(t *testing.T) {
	dir := t.TempDir()
	user := writeSettings(t, dir, "settings.json",
		`{"permissions":{"deny":["Bash(cd Compass && ./gradlew test)"],"ask":["WebFetch"]}}`)
	layers := []SettingsLayer{{File: user}}

	if r := FindBlockingRule(layers, Denial{Tool: "Bash", Command: "cd Compass && ./gradlew test"}); r == nil || r.Kind != "deny" {
		t.Errorf("exact command: rule = %+v, want the exact deny matched", r)
	}
	if r := FindBlockingRule(layers, Denial{Tool: "Bash", Command: "cd Compass && ./gradlew testDebug"}); r != nil {
		t.Errorf("longer command matched an exact rule: %+v", r)
	}
	if r := FindBlockingRule(layers, Denial{Tool: "WebFetch", Command: "https://x"}); r == nil || r.Kind != "ask" {
		t.Errorf("bare tool name: rule = %+v, want WebFetch ask matched", r)
	}
	// A prefix rule must not match a longer binary name (cd vs cdparanoia).
	writeSettings(t, dir, "settings.json", `{"permissions":{"ask":["Bash(cd:*)"]}}`)
	if r := FindBlockingRule(layers, Denial{Tool: "Bash", Command: "cdparanoia -B"}); r != nil {
		t.Errorf("prefix over-match: %+v", r)
	}
}

// DefaultSettingsLayers lists only files that exist, worktree layers last.
func TestDefaultSettingsLayersExistingOnly(t *testing.T) {
	wt := t.TempDir()
	writeSettings(t, wt, ".claude/settings.local.json", `{}`)
	layers := DefaultSettingsLayers(wt)
	for _, l := range layers {
		if _, err := os.Stat(l.File); err != nil {
			t.Errorf("layer %q listed but does not exist", l.File)
		}
	}
	last := layers[len(layers)-1]
	if filepath.Base(last.File) != "settings.local.json" || last.Managed {
		t.Errorf("last layer = %+v, want the worktree-local file, not managed", last)
	}
}
