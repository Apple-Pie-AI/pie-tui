package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
)

// resolveSDKBinary finds an Android SDK binary exactly the way the code that
// actually runs it does. It must delegate to emulator.ResolvePaths rather than
// reimplement the lookup: that resolver auto-detects the SDK root from
// $ANDROID_HOME/$ANDROID_SDK_ROOT when android_sdk_path is unset, and prefers
// the SDK copy over PATH. A second, subtly different implementation here meant
// doctor reported a broken emulator setup on machines where runs worked fine.
func resolveSDKBinary(name, sdkPath string) string {
	p := emulator.ResolvePaths(config.Expand(sdkPath))
	var got string
	switch name {
	case "adb":
		got = p.ADB
	case "emulator":
		got = p.Emulator
	default:
		return ""
	}
	// ResolvePaths falls back to the bare name so exec fails with a clear
	// message; for a health check that is a miss, not a hit.
	if got == "" || got == name {
		return ""
	}
	if !filepath.IsAbs(got) {
		if abs, err := exec.LookPath(got); err == nil {
			return abs
		}
		return ""
	}
	if _, err := os.Stat(got); err != nil {
		return ""
	}
	return got
}

func checkSDKBinary(name, sdkPath, hint string) error {
	if p := resolveSDKBinary(name, sdkPath); p != "" {
		return nil
	}
	where := sdkPath
	if where == "" {
		where = "$ANDROID_HOME/$ANDROID_SDK_ROOT or the default SDK location"
	}
	return fmt.Errorf("not found in PATH or %s - %s", where, hint)
}

func checkAVD(avdName, sdkPath string) error {
	emuBin := resolveSDKBinary("emulator", sdkPath)
	if emuBin == "" {
		return errors.New("emulator binary not found - cannot verify AVD")
	}
	out, err := exec.Command(emuBin, "-list-avds").Output()
	if err != nil {
		return fmt.Errorf("emulator -list-avds failed: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == avdName {
			return nil
		}
	}
	return fmt.Errorf("AVD %q not found - run: avdmanager list avd", avdName)
}

func checkGH() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return errors.New("not found in PATH")
	}
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		return errors.New("installed but not authenticated - run: gh auth login")
	}
	return nil
}

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "doctor",
		Short: "Run the connectivity check against the saved config (no prompts)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			fmt.Printf("Checking your Apple Pie setup…\nconfig: %s\n\n", paths.Config())
			fmt.Println("Required")
			for _, r := range cfg.Repos {
				report("repository - "+r.Path, git.ValidateRepo(config.Expand(r.Path)))
			}
			report("claude CLI", exec.Command("claude", "--version").Run())
			report("gh CLI", checkGH())

			fmt.Println("\nOptional (used automatically for UI/instrumentation tasks)")
			report("adb", checkSDKBinary("adb", cfg.AndroidSDKPath, "install Android SDK platform-tools"))
			report("emulator", checkSDKBinary("emulator", cfg.AndroidSDKPath, "install Android SDK emulator package"))
			if cfg.AVDName != "" {
				report("AVD "+cfg.AVDName, checkAVD(cfg.AVDName, cfg.AndroidSDKPath))
			} else {
				fmt.Println("  ℹ avd_name not set in config - tasks needing emulator will fall back to unit tests")
			}
			return nil
		},
	})
}
