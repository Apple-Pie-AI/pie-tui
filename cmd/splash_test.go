package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/splash"
)

// splashCmd builds a stand-in for the root command carrying just the --splash
// flag, so the hook can be exercised without launching the hub.
func splashCmd(t *testing.T, forced bool) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	c := &cobra.Command{Use: "test"}
	c.Flags().Bool("splash", forced, "")
	var out bytes.Buffer
	c.SetOut(&out)
	return c, &out
}

// The common case: piped output, scripts, CI, the daemon's own child processes.
// Nothing is drawn and - critically - nothing is written to disk, so a scripted
// run never consumes the one first-run splash a human is owed.
func TestSplashSkippedWithoutATerminal(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	c, out := splashCmd(t, false)
	if err := maybeSplash(c); err != nil {
		t.Fatalf("maybeSplash: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("drew something without a terminal: %q", out.String())
	}
	if splash.Seen() {
		t.Error("marked the splash as seen even though it never played")
	}
}

// --splash with nowhere to animate still owes the user the screen, just static.
func TestSplashForcedWithoutATerminalPrintsStatic(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	c, out := splashCmd(t, true)
	if err := maybeSplash(c); err != nil {
		t.Fatalf("maybeSplash: %v", err)
	}
	if !strings.Contains(out.String(), "PRESS") {
		t.Errorf("--splash printed no title screen, got %q", out.String())
	}
}

func TestSplashOptOutEnv(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	t.Setenv("PIE_NO_SPLASH", "1")
	c, out := splashCmd(t, false)
	if err := maybeSplash(c); err != nil {
		t.Fatalf("maybeSplash: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("PIE_NO_SPLASH did not suppress the splash: %q", out.String())
	}
}

// Quitting at the title screen is a clean exit, so Execute must not surface it
// as a command failure.
func TestExecuteSwallowsSplashQuit(t *testing.T) {
	if err := errSplashQuit; err == nil {
		t.Fatal("errSplashQuit is nil")
	}
	saved := rootCmd.RunE
	t.Cleanup(func() { rootCmd.RunE = saved })
	rootCmd.RunE = func(*cobra.Command, []string) error { return errSplashQuit }
	rootCmd.SetArgs(nil)
	t.Cleanup(func() { rootCmd.SetArgs(nil) })
	if err := Execute(); err != nil {
		t.Errorf("Execute surfaced a title-screen quit as an error: %v", err)
	}
}

// The flag has to live on the root's persistent flags so it works on
// subcommands too (`pie run PROJ-1 --splash`).
func TestSplashFlagIsPersistent(t *testing.T) {
	if rootCmd.PersistentFlags().Lookup("splash") == nil {
		t.Fatal("--splash is not registered on the root command's persistent flags")
	}
}
