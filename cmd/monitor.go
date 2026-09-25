package cmd

import (
	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/tui"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:     "monitor",
		Aliases: []string{"top"},
		Short:   "Live TUI hub: watch agents and run every command (also the default `pie`)",
		RunE:    func(_ *cobra.Command, _ []string) error { return tui.Run(version) },
	})
}
