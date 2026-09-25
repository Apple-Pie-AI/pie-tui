package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/ticket"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "logs <TICKET>",
		Short: "Print the session log for a ticket",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			b, err := os.ReadFile(paths.LogFor(ticket.Normalize(args[0])))
			if err != nil {
				return err
			}
			fmt.Print(string(b))
			return nil
		},
	})
}
