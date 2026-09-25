package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T3.6 - the callback flags ride along exactly when a config path is set;
// modes that don't take the callback simply leave the field empty.
func TestBuildArgsPermissionPromptFlags(t *testing.T) {
	with := buildArgs("p", Options{AllowedTools: "Bash(ls:*)", PermissionPromptConfig: "/tmp/x.json"})
	joined := strings.Join(with, " ")
	if !strings.Contains(joined, "--permission-prompt-tool mcp__pie__approve") ||
		!strings.Contains(joined, "--mcp-config /tmp/x.json") {
		t.Errorf("args = %q, want the permission prompt flags", joined)
	}
	without := strings.Join(buildArgs("p", Options{AllowedTools: "Bash(ls:*)"}), " ")
	if strings.Contains(without, "permission-prompt-tool") || strings.Contains(without, "mcp-config") {
		t.Errorf("args = %q, want no callback flags when unconfigured", without)
	}
}

// The session is never relocated: claude reads its cwd as the project (which
// settings, hooks and MCP config it loads), so every stage starts at the
// worktree root and no argv carries --add-dir. PLEX-57773 is what a subdir
// cwd did to the ship-comments verify.
func TestBuildArgsNeverRelocatesTheSession(t *testing.T) {
	s := strings.Join(buildArgs("p", Options{WorktreeDir: "/wt", AllowedTools: "Bash(ls:*)"}), " ")
	if strings.Contains(s, "--add-dir") {
		t.Errorf("args = %q, want no --add-dir", s)
	}
}

// T3.7 - the generated mcp-config points claude at this binary's hidden
// mcp-approve command for the right ticket, in the git-excluded .agent dir.
func TestWriteApprovalMCPConfig(t *testing.T) {
	wt := t.TempDir()
	path, err := WriteApprovalMCPConfig(wt, "PLEX-9")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(wt, ".agent") {
		t.Errorf("path = %q, want inside <worktree>/.agent", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	pie, ok := cfg.MCPServers["pie"]
	if !ok {
		t.Fatalf("no 'pie' server in %s", raw)
	}
	self, _ := os.Executable()
	if pie.Command != self {
		t.Errorf("command = %q, want this binary %q", pie.Command, self)
	}
	want := []string{"mcp-approve", "--ticket", "PLEX-9"}
	if len(pie.Args) != 3 || pie.Args[0] != want[0] || pie.Args[1] != want[1] || pie.Args[2] != want[2] {
		t.Errorf("args = %q, want %q", pie.Args, want)
	}
}

// The per-server MCP timeout must ride in the config, or claude's own MCP
// layer aborts an unanswered approval after ~30 minutes - silently capping
// the no-timeout guarantee (field QA: "sent no response or progress for
// 1938s; aborting"). Knob verified live: a 15000 value times out at 15s, so
// the unit is milliseconds and the field is honored.
func TestWriteApprovalMCPConfigSetsLongTimeout(t *testing.T) {
	path, err := WriteApprovalMCPConfig(t.TempDir(), "PLEX-9")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Timeout int64 `json:"timeout"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	got := cfg.MCPServers["pie"].Timeout
	if got != approvalWaitMS {
		t.Fatalf("timeout = %d, want %d", got, int64(approvalWaitMS))
	}
	if got < 24*60*60*1000 {
		t.Fatalf("timeout = %dms - under a day means claude will abort waits pie promised to keep open", got)
	}
}
