package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/splash"
)

// errSplashQuit unwinds a "quit at the title screen" back to Execute, which
// swallows it so the CLI exits cleanly instead of printing an error.
var errSplashQuit = errors.New("quit at title screen")

func init() {
	rootCmd.PersistentFlags().Bool("splash", false,
		"replay the Apple Pie title screen before running (it plays itself on first launch)")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return maybeSplash(cmd) }
}

// maybeSplash plays the title screen: unconditionally with --splash, otherwise
// once ever, on the first interactive launch on this machine. It deliberately
// checks for a terminal before touching the filesystem so that piped, scripted
// and daemon-spawned invocations cost nothing.
func maybeSplash(cmd *cobra.Command) error {
	forced, _ := cmd.Flags().GetBool("splash")

	if !splash.Interactive() {
		if !forced {
			return nil
		}
		// Asked for it with nowhere to animate: print the finished screen.
		fmt.Fprintln(cmd.OutOrStdout(), splash.Static(version))
		splash.MarkSeen()
		return nil
	}
	if !forced && (os.Getenv("PIE_NO_SPLASH") != "" || splash.Seen()) {
		return nil
	}

	err := splash.Run(version)
	// Either way they have now seen it, so a first run they escaped out of
	// does not ambush them again on the next launch.
	splash.MarkSeen()
	if errors.Is(err, splash.ErrAborted) {
		return errSplashQuit
	}
	return err
}
