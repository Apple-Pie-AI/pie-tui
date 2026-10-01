package agent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func modelPickerEnv(t *testing.T) (managed, user string) {
	t.Helper()
	managed = t.TempDir()
	prev := managedSettingsDir
	managedSettingsDir = managed
	t.Cleanup(func() { managedSettingsDir = prev })
	home := t.TempDir()
	t.Setenv("HOME", home)
	return managed, filepath.Join(home, ".claude", "settings.json")
}

func TestClaudeModelPickerNoneDefined(t *testing.T) {
	_, user := modelPickerEnv(t)
	writeJSON(t, user, `{"model":"opus","permissions":{"allow":["Read"]}}`)
	if p, ok := ClaudeModelPicker(); ok {
		t.Fatalf("no file defines modelPicker, got %+v", p)
	}
}

// The user's own settings are read when nothing managed defines a picker;
// rows without a model are dropped, labels kept.
func TestClaudeModelPickerFromUserSettings(t *testing.T) {
	_, user := modelPickerEnv(t)
	writeJSON(t, user, `{"modelPicker":{"options":[
		{"model":"claude-haiku-4-5","label":"Haiku 4.5"},
		{"model":"  ","label":"broken row"},
		{"model":"claude-opus-4-6"}]}}`)
	p, ok := ClaudeModelPicker()
	if !ok || p.File != user || p.ReplaceBuiltIns {
		t.Fatalf("picker = %+v ok=%v, want the user file's rows", p, ok)
	}
	if len(p.Rows) != 2 || p.Rows[0].Label != "Haiku 4.5" || p.Rows[1].Model != "claude-opus-4-6" {
		t.Fatalf("rows = %+v", p.Rows)
	}
}

// Managed beats user, a drop-in beats the managed file, and the winner's
// list is taken whole - never merged with a lower source's.
func TestClaudeModelPickerPrecedence(t *testing.T) {
	managed, user := modelPickerEnv(t)
	writeJSON(t, user, `{"modelPicker":{"options":[{"model":"user-model"}]}}`)
	writeJSON(t, filepath.Join(managed, "managed-settings.json"),
		`{"modelPicker":{"replaceBuiltInOptions":true,"options":[{"model":"managed-model"}]}}`)
	p, _ := ClaudeModelPicker()
	if len(p.Rows) != 1 || p.Rows[0].Model != "managed-model" || !p.ReplaceBuiltIns {
		t.Fatalf("managed must win whole, got %+v", p)
	}
	writeJSON(t, filepath.Join(managed, "managed-settings.d", "10-base.json"), `{"modelPicker":{"options":[{"model":"dropin-10"}]}}`)
	writeJSON(t, filepath.Join(managed, "managed-settings.d", "20-team.json"), `{"modelPicker":{"options":[{"model":"dropin-20"}]}}`)
	if p, _ := ClaudeModelPicker(); len(p.Rows) != 1 || p.Rows[0].Model != "dropin-20" {
		t.Fatalf("the last drop-in must win, got %+v", p)
	}
}

// A garbled file is skipped, not fatal: the next source still applies.
func TestClaudeModelPickerSkipsUnreadable(t *testing.T) {
	managed, user := modelPickerEnv(t)
	writeJSON(t, filepath.Join(managed, "managed-settings.json"), `{not json`)
	writeJSON(t, user, `{"modelPicker":{"options":[{"model":"sonnet"}]}}`)
	if p, ok := ClaudeModelPicker(); !ok || p.Rows[0].Model != "sonnet" {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
}

func TestParseModelCheck(t *testing.T) {
	ok := parseModelCheck([]byte(`{"is_error":false,"result":"ok","modelUsage":{"claude-opus-4-6":{}}}`), nil)
	if ok.Err != "" || ok.Resolved != "claude-opus-4-6" {
		t.Fatalf("success = %+v", ok)
	}
	refused := parseModelCheck([]byte(`{"is_error":true,"result":"There's an issue with the selected model (x). It may not exist or you may not have access to it.","modelUsage":{}}`),
		errors.New("exit status 1"))
	if refused.Err == "" || refused.Resolved != "" {
		t.Fatalf("refusal = %+v", refused)
	}
	if c := parseModelCheck([]byte("boom"), errors.New("exec: claude not found")); c.Err == "" {
		t.Fatal("a launch failure must report an error")
	}
	if c := parseModelCheck([]byte(`{"is_error":false,"modelUsage":{}}`), nil); c.Err == "" {
		t.Fatal("no reported model must not pass as a success")
	}
}

func TestModelCheckMismatch(t *testing.T) {
	for _, tc := range []struct {
		requested, resolved string
		want                bool
	}{
		{"opus", "claude-opus-4-6", false},
		{"claude-haiku-4-5", "claude-haiku-4-5-20251001", false},
		{"opus", "claude-sonnet-5", true},
		{"us.anthropic.claude-opus-4-6-v1", "claude-opus-4-6", false},
		{"company-smart", "claude-sonnet-5", false}, // no family in the name: nothing to compare
	} {
		if got := (ModelCheck{Resolved: tc.resolved}).Mismatch(tc.requested); got != tc.want {
			t.Errorf("Mismatch(%q→%q) = %v, want %v", tc.requested, tc.resolved, got, tc.want)
		}
	}
	if (ModelCheck{Err: "refused"}).Mismatch("opus") {
		t.Error("a refusal is an error, not a mismatch")
	}
}
