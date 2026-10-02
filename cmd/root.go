// Package cmd wires up the `pie` CLI (Decision 7: headless daemon + CLI).
package cmd

import (
	"errors"
	"strings"

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
		if demo, _ := cmd.Flags().GetString("demo"); demo != "" {
			return tui.RunDemo(version, demo)
		}
		return tui.Run(version)
	},
}

func init() {
	// Hidden: the hub on fixture tickets and a fast scripted timeline, for
	// recording the launch videos (demo/*.tape). Touches no real state.
	// Bare --demo is the pipeline story; --demo=review is the PR-comments one.
	rootCmd.Flags().String("demo", "", "open the hub on fixture tickets (for recording demos): "+
		strings.Join(tui.DemoScenarios(), ", "))
	rootCmd.Flags().Lookup("demo").NoOptDefVal = "pipeline"
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
