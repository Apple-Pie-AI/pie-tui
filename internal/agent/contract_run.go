// The Run app repair contract: what a repair session writes to
// <project>/.agent/run.json after a deterministic step failed.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Repair statuses.
const (
	RunRepairOK      = "ok"           // the recipe below fixes it; pie retries
	RunSourceError   = "source_error" // the project's code is broken - not a run problem
	RunRepairBlocked = "blocked"      // needs a human (or something destructive)
)

// RunContract is .agent/run.json.
type RunContract struct {
	Status           string `json:"status"`
	Explanation      string `json:"explanation"`
	SuggestedCommand string `json:"suggested_command,omitempty"` // for the human; pie never runs it

	// The recipe corrections (status ok). Empty fields mean "keep".
	GradleDir      string            `json:"gradle_dir,omitempty"`
	GradleArgs     []string          `json:"gradle_args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	AssembleTask   string            `json:"assemble_task,omitempty"`
	ApplicationID  string            `json:"application_id,omitempty"`
	LaunchActivity string            `json:"launch_activity,omitempty"`
	Notes          string            `json:"notes,omitempty"`
}

// RunContractPath is where a repair session writes its contract.
func RunContractPath(projectDir string) string {
	return filepath.Join(projectDir, ".agent", "run.json")
}

// ReadRunContract reads the contract.
func ReadRunContract(projectDir string) (*RunContract, error) {
	b, err := os.ReadFile(RunContractPath(projectDir))
	if err != nil {
		return nil, err
	}
	var c RunContract
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// RunSettingsJSON adds the repair session's deny rules to the --settings
// payload (the sandbox posture), so edits are refused by the CLI itself.
func RunSettingsJSON(sandboxJSON string) string {
	m := map[string]interface{}{}
	if sandboxJSON != "" {
		_ = json.Unmarshal([]byte(sandboxJSON), &m)
	}
	m["permissions"] = map[string]interface{}{"deny": RunDenyRules}
	b, err := json.Marshal(m)
	if err != nil {
		return sandboxJSON
	}
	return string(b)
}
