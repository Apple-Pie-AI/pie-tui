package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/approve"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// mcp-approve is not a user command: claude spawns it (per the mcp-config
// agent.WriteApprovalMCPConfig writes) as the stdio MCP server behind
// --permission-prompt-tool. Its lifecycle is claude's: stdin closes, it exits.
var mcpApproveCmd = &cobra.Command{
	Use:    "mcp-approve",
	Short:  "internal: permission-prompt MCP server for a running agent session",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		ticket, _ := cmd.Flags().GetString("ticket")
		if ticket == "" {
			return fmt.Errorf("mcp-approve: --ticket is required")
		}
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		st, err := store.Open(paths.StateDB())
		if err != nil {
			return err
		}
		defer st.Close()
		srv := &approve.Server{
			Ticket:  ticket,
			Allowed: cfg.EffectiveAllowedTools(),
			Policy:  cfg.ApprovalPolicy,
			Store:   st,
			// Re-read the config per decision: "Allow & remember" writes its
			// rule mid-session, and this server outlives that write.
			Refresh: func() (string, string) {
				fresh, err := config.Load()
				if err != nil {
					return cfg.EffectiveAllowedTools(), cfg.ApprovalPolicy
				}
				return fresh.EffectiveAllowedTools(), fresh.ApprovalPolicy
			},
			// A pending question has no timeout - it waits for the human (see
			// the Server doc). The wait ends structurally when the claude
			// session that asked dies: orphaned children reparent to pid 1,
			// which is the liveness signal.
			ParentAlive: func() bool { return os.Getppid() != 1 },
			// Decisions go into the ticket's own log file. Permission-prompt
			// invocations are made by the CLI's permission layer, not the
			// model, so they never appear in the session stream (verified
			// live) - without this line an auto-approved run would be
			// indistinguishable in `pie logs` from one that was never asked.
			// O_APPEND line writes interleave safely with the runner's own.
			Logf: approveLogf(ticket),
		}
		// stdin closing means claude exited: expire any prompt still pending
		// so the dashboard never shows a question no agent is waiting behind.
		defer srv.ExpirePending()
		return srv.Serve(os.Stdin, os.Stdout)
	},
}

// approveLogf appends "  ⏸ <line>" to the ticket's session log, falling back
// to stderr when the log cannot be opened - diagnostics must never break the
// approval flow.
func approveLogf(ticket string) func(string, ...interface{}) {
	return func(format string, args ...interface{}) {
		line := "  ⏸ " + fmt.Sprintf(format, args...) + "\n"
		logPath := paths.LogFor(ticket)
		_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprint(os.Stderr, line)
			return
		}
		defer f.Close()
		_, _ = f.WriteString(line)
	}
}

func init() {
	mcpApproveCmd.Flags().String("ticket", "", "ticket whose session this server answers for")
	rootCmd.AddCommand(mcpApproveCmd)
}
