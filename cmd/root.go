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
	RunE: func(_ *cobra.Command, _ []string) error { return tui.Run(version) },
}

// Execute runs the root command.
func Execute() error {
	err := rootCmd.Execute()
	if errors.Is(err, errSplashQuit) {
		return nil // quitting the title screen is a clean exit, not a failure
	}
	return err
}
