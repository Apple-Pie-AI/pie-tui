// Writing the --mcp-config that points claude's permission prompts at pie.
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// approvalWaitMS is the per-server MCP tool-call timeout claude honors for
// the approval server, in milliseconds. Claude's own MCP layer aborts a tool
// call that produces no response for ~30 minutes ("sent no response or
// progress for 1938s; aborting" - hit in field QA by a prompt left pending
// overnight), which silently capped pie's no-timeout guarantee. 7 days is
// "effectively forever" for a single question while still being a number:
// the knob is verified live (a 15000 value times out at exactly 15s), and
// every longer-lived cleanup (stop, restart, the dashboard reaper) fires
// well before it could matter.
const approvalWaitMS = 7 * 24 * 60 * 60 * 1000

// WriteApprovalMCPConfig writes the mcp-config file that makes claude spawn
// `pie mcp-approve --ticket <ticket>` (this same binary) as its permission
// prompt server, and returns its path. It lives in the worktree's .agent/
// scratch dir, which is git-excluded per worktree and cleaned up with it.
func WriteApprovalMCPConfig(worktreeDir, ticket string) (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	// Integration tests run inside `go test`, whose executable has no
	// mcp-approve command; they build the real binary and name it here.
	if override := os.Getenv("PIE_SELF_BIN"); override != "" {
		self = override
	}
	cfg := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"pie": map[string]interface{}{
				"command": self,
				"args":    []string{"mcp-approve", "--ticket", ticket},
				"timeout": approvalWaitMS,
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(worktreeDir, ".agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "mcp-approve.json")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
