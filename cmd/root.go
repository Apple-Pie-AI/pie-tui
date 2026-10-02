// Package cmd wires up the `pie` CLI (Decision 7: headless daemon + CLI).
package cmd

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/tui"
)

// version is overridden at release time via -ldflags -X.
var version = "dev"

var rootCmd = &cobra.Command{
	Use:           "pie",
	Short:         "Apple Pie - orchestrate mobile app development loops with agentic CLIs",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Bare `pie` (no subcommand) opens the TUI hub. Subcommands still work.
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if demo, _ := cmd.Flags().GetBool("demo"); demo {
			return tui.RunDemo(version)
		}
		return tui.Run(version)
	},
}

func init() {
	// Hidden: the hub on fixture tickets and a fast scripted timeline, for
	// recording the launch video (demo/apple-pie.tape). Touches no real state.
	rootCmd.Flags().Bool("demo", false, "open the hub on fixture tickets (for recording demos)")
	_ = rootCmd.Flags().MarkHidden("demo")
}

// Execute runs the root command.
func Execute() error {
	err := rootCmd.Execute()
	if errors.Is(err, errSplashQuit) {
		return nil // quitting the title screen is a clean exit, not a failure
	}
	return err
}
