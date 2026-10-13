// The deterministic steps of a run: build, read the APK's applicationId,
// reinstall (old version removed first), launch. Every command runs as an
// argv - never through a shell - with its output in the run's log and its
// tail kept for the error (and the repair agent).
package runapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Steps, as the progress screen names them.
const (
	StepPrepare   = "prepare"
	StepDevice    = "device"
	StepSync      = "sync"
	StepBuild     = "build"
	StepReinstall = "reinstall"
	StepLaunch    = "launch"
	StepRepair    = "repair"
)

// StepError is a failed step: what ran, how it ended, and whether an agent
// could plausibly fix it (a build or task problem - not a crash or a full
// device).
type StepError struct {
	Step       string
	Argv       []string
	ExitCode   int
	Tail       string
	Repairable bool
	Msg        string
}

func (e *StepError) Error() string {
	if e.Msg != "" {
		return e.Step + ": " + e.Msg
	}
	return fmt.Sprintf("%s: `%s` exited %d", e.Step, strings.Join(e.Argv, " "), e.ExitCode)
}

// Env is what the steps run with.
type Env struct {
	ADB     string    // absolute adb path
	ToolEnv []string  // process env: JAVA_HOME + SDK on PATH (agent.ToolEnv)
	Log     io.Writer // the run's log
	Timeout time.Duration
}

// tailLines bounds the output kept for an error.
const tailLines = 150

// run executes argv in dir, streaming to the log; extra is appended to the
// environment (ANDROID_SERIAL, recipe env). It returns the full output.
func (e Env) run(ctx context.Context, dir, step string, repairable bool, extra string, argv ...string) (string, error) {
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append([]string{}, e.ToolEnv...)
	for _, kv := range strings.Split(extra, "\n") {
		if kv != "" {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	// Its own group, cancelled as a group: Gradle forks a daemon and workers.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = 10 * time.Second
	var buf bytes.Buffer
	w := io.Writer(&buf)
	if e.Log != nil {
		fmt.Fprintf(e.Log, "$ %s\n", strings.Join(argv, " "))
		w = io.MultiWriter(&buf, e.Log)
	}
	cmd.Stdout, cmd.Stderr = w, w
	err := cmd.Run()
	out := buf.String()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return out, &StepError{Step: step, Argv: argv, ExitCode: code, Tail: tail(out, tailLines), Repairable: repairable}
	}
	return out, nil
}

// AssembleTask is the Gradle task that builds variant of module.
func AssembleTask(module, variant string) string {
	if module == ":" || module == "" {
		return "assemble" + upperFirst(variant)
	}
	return module + ":assemble" + upperFirst(variant)
}

// Build runs the assemble task from the Gradle root.
func Build(ctx context.Context, e Env, gradleDir, task string, args []string, extraEnv string) error {
	argv := append([]string{wrapperPath(gradleDir), task}, args...)
	_, err := e.run(ctx, gradleDir, StepBuild, true, extraEnv, argv...)
	return err
}

// Output is what the build produced for one variant.
type Output struct {
	ApplicationID string
	APKs          []string
}

// outputMetadata is AGP's build/outputs/apk/<…>/output-metadata.json.
type outputMetadata struct {
	ApplicationID string `json:"applicationId"`
	VariantName   string `json:"variantName"`
	Elements      []struct {
		OutputFile string `json:"outputFile"`
	} `json:"elements"`
}

// ReadOutput finds the variant's APKs and applicationId from the metadata AGP
// writes next to them - the same facts Studio reads from its model.
func ReadOutput(moduleDir, variant string) (Output, error) {
	root := filepath.Join(moduleDir, "build", "outputs", "apk")
	var found Output
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "output-metadata.json" || found.ApplicationID != "" {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		var md outputMetadata
		if json.Unmarshal(b, &md) != nil || md.VariantName != variant {
			return nil
		}
		found.ApplicationID = md.ApplicationID
		for _, el := range md.Elements {
			found.APKs = append(found.APKs, filepath.Join(filepath.Dir(p), el.OutputFile))
		}
		return nil
	})
	if found.ApplicationID == "" || len(found.APKs) == 0 {
		return found, &StepError{Step: StepBuild, Repairable: true,
			Msg: fmt.Sprintf("the build finished but no APK for variant %q was found under %s", variant, root)}
	}
	return found, nil
}

// Reinstall removes the old version of the app, then installs the new APK(s).
// Removing first is deliberate: it clears "insufficient storage" and
// signature-mismatch failures - at the cost of the app's data on the device.
func Reinstall(ctx context.Context, e Env, serial string, o Output) error {
	_, _ = e.run(ctx, "", StepReinstall, false, "", e.ADB, "-s", serial, "uninstall", o.ApplicationID)
	argv := []string{e.ADB, "-s", serial, "install", "-t"}
	if len(o.APKs) > 1 {
		argv = []string{e.ADB, "-s", serial, "install-multiple", "-t"}
	}
	out, err := e.run(ctx, "", StepReinstall, false, "", append(argv, o.APKs...)...)
	if err == nil && strings.Contains(out, "Failure [") {
		err = &StepError{Step: StepReinstall, Argv: argv, Tail: tail(out, 20)}
	}
	if err != nil {
		var se *StepError
		if errors.As(err, &se) && strings.Contains(out, "INSUFFICIENT_STORAGE") {
			se.Msg = "the device is out of storage even after removing the old version - free space on it or wipe the emulator"
		}
		return err
	}
	return nil
}

// ResolveActivity asks the device for the app's LAUNCHER activity.
func ResolveActivity(ctx context.Context, e Env, serial, appID string) (string, error) {
	argv := []string{e.ADB, "-s", serial, "shell", "cmd", "package", "resolve-activity", "--brief",
		"-c", "android.intent.category.LAUNCHER", appID}
	out, err := e.run(ctx, "", StepLaunch, true, "", argv...)
	if err != nil {
		return "", err
	}
	if act := lastLine(out); strings.Contains(act, "/") {
		return act, nil
	}
	return "", &StepError{Step: StepLaunch, Argv: argv, Repairable: true, Tail: tail(out, 20),
		Msg: appID + " has no LAUNCHER activity"}
}

// launchSettle is how long a launched app must stay alive to count as running.
var launchSettle = 2 * time.Second

// Launch starts activity and checks the app is still alive a moment later; a
// crash dumps the error log into the run's log and is not repairable - it is
// the app's problem, not the run's.
func Launch(ctx context.Context, e Env, serial, appID, activity string) error {
	argv := []string{e.ADB, "-s", serial, "shell", "am", "start", "-W", "-n", activity}
	out, err := e.run(ctx, "", StepLaunch, true, "", argv...)
	if err == nil && strings.Contains(out, "Error:") {
		err = &StepError{Step: StepLaunch, Argv: argv, Repairable: true, Tail: tail(out, 20)}
	}
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(launchSettle):
	}
	if pid, _ := e.run(ctx, "", StepLaunch, false, "", e.ADB, "-s", serial, "shell", "pidof", appID); strings.TrimSpace(pid) == "" {
		_, _ = e.run(ctx, "", StepLaunch, false, "", e.ADB, "-s", serial, "logcat", "-d", "-t", "300", "*:E")
		return &StepError{Step: StepLaunch, Msg: appID + " started and then exited - it likely crashed (its error log is in the run log)"}
	}
	return nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
