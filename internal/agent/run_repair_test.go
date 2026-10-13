package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRunRepairPrompt(t *testing.T) {
	p := BuildRunRepairPrompt(RunRepairInput{ProjectDir: "/repo", Serial: "emulator-5554", Module: ":app",
		Variant: "stagingDebug", RecipeJSON: `{"version":1}`,
		Failure: RunFailure{Step: "build", Argv: []string{"/repo/gradlew", ":app:assembleStagingDebug"}, ExitCode: 1,
			Tail: "e: Unresolved reference: Foo"}})
	for _, want := range []string{"Do NOT change any file", "/repo/.agent/run.json", ":app:assembleStagingDebug",
		"Unresolved reference: Foo", "-s emulator-5554", `"status":"source_error"`, "Never uninstall, wipe or clear data"} {
		if !strings.Contains(p, want) {
			t.Errorf("repair prompt missing %q", want)
		}
	}
}

// The repair session's edits are denied by the CLI, on top of the sandbox.
func TestRunSettingsJSONDeniesEdits(t *testing.T) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(RunSettingsJSON(`{"sandbox":{"enabled":false}}`)), &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["sandbox"]; !ok {
		t.Error("the sandbox posture must be kept")
	}
	var got []string
	for _, d := range m["permissions"].(map[string]interface{})["deny"].([]interface{}) {
		got = append(got, d.(string))
	}
	for _, want := range []string{"Edit", "MultiEdit", "Bash(adb uninstall:*)", "Bash(git commit:*)"} {
		if !strings.Contains(strings.Join(got, " "), want) {
			t.Errorf("deny rules missing %q: %v", want, got)
		}
	}
}
