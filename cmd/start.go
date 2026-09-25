package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/daemon"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
)

func init() {
	var once bool
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start the cleanup daemon: reclaim worktrees for resolved PRs, idle down the emulator (foreground; background with &)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := paths.EnsureDirs(); err != nil {
				return err
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}

			// Single-instance guard via pidfile.
			if pid, ok := proc.DaemonAlive(); ok {
				return fmt.Errorf("daemon already running (pid %d) - use `pie stop`", pid)
			}
			if err := proc.WritePid(os.Getpid()); err != nil {
				return err
			}
			defer proc.RemovePid()

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			return daemon.Run(ctx, cfg, once)
		},
	}
	startCmd.Flags().BoolVar(&once, "once", false, "poll a single cycle, run claimed tickets, then exit")
	rootCmd.AddCommand(startCmd)

	rootCmd.AddCommand(&cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			pid, ok := proc.ReadPid()
			if !ok {
				return fmt.Errorf("no daemon pidfile at %s - is it running?", paths.PidFile())
			}
			if !proc.Alive(pid) {
				proc.RemovePid()
				return fmt.Errorf("daemon (pid %d) not running; cleaned up stale pidfile", pid)
			}
			if !proc.IsPieProcess(pid) {
				proc.RemovePid()
				return fmt.Errorf(
					"pid %d is not an Apple Pie daemon (stale pidfile from a killed daemon, "+
						"pid since reused); cleaned up the pidfile without signalling it", pid)
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return fmt.Errorf("signal pid %d: %w", pid, err)
			}
			fmt.Printf("sent stop to daemon (pid %d)\n", pid)
			return nil
		},
	})
}
