package runapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// fakeProject is a git checkout whose gradlew lists two variants of :app and
// "builds" by writing AGP's output-metadata.json. With $NEED_FLAG set, the
// build fails unless that flag is passed - a project Studio's logic alone
// can't run, which a repair must teach.
func fakeProject(t *testing.T) (dir, gradleLog string) {
	t.Helper()
	dir = t.TempDir()
	gradleLog = filepath.Join(t.TempDir(), "gradle.log")
	gw := `#!/bin/sh
echo "$*" >> "` + gradleLog + `"
case "$*" in
*"tasks --all"*)
  echo "app:installDebug - Installs the Debug build."
  echo "app:installStagingDebug - Installs the StagingDebug build."
  exit 0 ;;
esac
if [ -n "$NEED_FLAG" ]; then
  case "$*" in *"$NEED_FLAG"*) ;; *) echo "FAILURE: Build failed - missing $NEED_FLAG"; exit 1 ;; esac
fi
variant=debug; app=com.acme
case "$1" in *StagingDebug) variant=stagingDebug; app=com.acme.staging ;; esac
out="app/build/outputs/apk/$variant"
mkdir -p "$out"
touch "$out/app-$variant.apk"
printf '{"applicationId":"%s","variantName":"%s","elements":[{"outputFile":"app-%s.apk"}]}' "$app" "$variant" "$variant" > "$out/output-metadata.json"
echo "BUILD SUCCESSFUL"
`
	for p, body := range map[string]string{
		"gradlew":              gw,
		"settings.gradle.kts":  `include(":app")`,
		"app/build.gradle.kts": `android { }`,
		".gitignore":           "app/build/\n",
	} {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		mode := os.FileMode(0o644)
		if p == "gradlew" {
			mode = 0o755
		}
		if err := os.WriteFile(full, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir, gradleLog
}

// harness wires a Job against the fake project, a stub adb, a real store
// with a running device, and a stubbed repair agent.
type harness struct {
	j          Job
	gradleLog  string
	adbLog     string
	agentCalls int
	answer     string // the run.json the stub agent writes ("" = none)
	edit       bool   // the stub agent also edits a project file
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	t.Setenv("PIE_HOME", t.TempDir())
	old := launchSettle
	launchSettle = time.Millisecond
	t.Cleanup(func() { launchSettle = old })

	dir, gradleLog := fakeProject(t)
	adb, adbLog := fakeAdb(t)
	sdk := t.TempDir()
	_ = os.MkdirAll(filepath.Join(sdk, "platform-tools"), 0o755)
	_ = os.Symlink(adb, filepath.Join(sdk, "platform-tools", "adb"))

	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_ = st.UseEmulator("Pixel_7", "emulator-5554")

	h := &harness{gradleLog: gradleLog, adbLog: adbLog}
	oldDev, oldAgent := ensureDevice, runAgent
	ensureDevice = func(context.Context, *config.Config, *store.Store, func(string, ...interface{})) (string, error) {
		return "emulator-5554", nil
	}
	runAgent = func(ctx context.Context, prompt string, o agent.Options) (agent.Result, error) {
		h.agentCalls++
		if h.edit {
			_ = os.WriteFile(filepath.Join(dir, "app", "build.gradle.kts"), []byte("android { hacked }"), 0o644)
		}
		if h.answer != "" {
			_ = os.MkdirAll(filepath.Join(dir, ".agent"), 0o755)
			_ = os.WriteFile(agent.RunContractPath(dir), []byte(h.answer), 0o644)
		}
		return agent.Result{SessionID: "s"}, nil
	}
	t.Cleanup(func() { ensureDevice, runAgent = oldDev, oldAgent })

	h.j = Job{Cfg: &config.Config{AndroidSDKPath: sdk}, Repo: &config.Repo{Path: dir}, Target: "main",
		Dir: dir, Store: st, Log: &strings.Builder{}}
	return h
}

func (h *harness) run(t *testing.T) (Result, error) {
	t.Helper()
	return Run(context.Background(), h.j)
}

func count(path, sub string) int {
	b, _ := os.ReadFile(path)
	return strings.Count(string(b), sub)
}

// Studio's logic, no agent: sync → build → reinstall (uninstall first) →
// launch. The recipe remembers the choice; the device is handed back.
func TestRunFirstTimeNeedsNoAgent(t *testing.T) {
	h := newHarness(t)
	res, err := h.run(t)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if h.agentCalls != 0 {
		t.Fatalf("agent called %d times on a project Studio's logic can run", h.agentCalls)
	}
	if res.Module != ":app" || res.Variant != "debug" || res.AppID != "com.acme" || res.Serial != "emulator-5554" {
		t.Errorf("result = %+v", res)
	}
	if count(h.gradleLog, "tasks --all") != 1 || count(h.gradleLog, ":app:assembleDebug") != 1 {
		t.Errorf("gradle calls:\n%s", read(h.gradleLog))
	}
	adb := read(h.adbLog)
	if strings.Index(adb, "uninstall com.acme") > strings.Index(adb, "install -t") {
		t.Errorf("the old version must be removed before installing:\n%s", adb)
	}
	if !strings.Contains(adb, "am start -W -n com.acme.staging/com.acme.MainActivity") {
		t.Errorf("launch:\n%s", adb)
	}
	r, _ := LoadRecipe(h.j.Repo.Path)
	if r.LastVariant != "debug" || r.SyncHash == "" || len(r.Model.Apps) != 1 {
		t.Errorf("recipe = %+v", r)
	}
	if d, _ := h.j.Store.ActiveDevice(); d.Holder != "" {
		t.Errorf("device left held by %q", d.Holder)
	}

	// Second run: the scan is cached, the remembered variant reused.
	h.j.Variant = "stagingDebug"
	if res, err := h.run(t); err != nil || res.AppID != "com.acme.staging" {
		t.Fatalf("second run = %+v, %v", res, err)
	}
	if n := count(h.gradleLog, "tasks --all"); n != 1 {
		t.Errorf("tasks --all ran %d times - the sync should be cached", n)
	}
	h.j.Variant = ""
	if res, _ := h.run(t); res.Variant != "stagingDebug" {
		t.Errorf("last variant not remembered: %q", res.Variant)
	}
}

// A project that needs a flag: one repair teaches it, the retry succeeds,
// and every later run replays it with no agent.
func TestRepairTeachesTheRecipe(t *testing.T) {
	h := newHarness(t)
	t.Setenv("NEED_FLAG", "-Pacme.ci=true")
	h.answer = `{"status":"ok","gradle_args":["-Pacme.ci=true"],"explanation":"the build needs -Pacme.ci=true","notes":"CI flag required"}`
	res, err := h.run(t)
	if err != nil || !res.Repaired {
		t.Fatalf("repaired run = %+v, %v", res, err)
	}
	if h.agentCalls != 1 {
		t.Fatalf("agent calls = %d, want exactly 1", h.agentCalls)
	}
	r, _ := LoadRecipe(h.j.Repo.Path)
	if len(r.GradleArgs) != 1 || r.GradleArgs[0] != "-Pacme.ci=true" || len(r.History) != 1 || !strings.Contains(r.Notes, "CI flag") {
		t.Fatalf("recipe = %+v", r)
	}
	if _, err := h.run(t); err != nil || h.agentCalls != 1 {
		t.Fatalf("replay: err=%v agentCalls=%d - a learned recipe must run without the agent", err, h.agentCalls)
	}
}

// A broken project is reported as such, and the recipe is left alone.
func TestRepairReportsSourceErrors(t *testing.T) {
	h := newHarness(t)
	t.Setenv("NEED_FLAG", "-Pnever")
	h.answer = `{"status":"source_error","explanation":"LoginViewModel.kt:42 - unresolved reference"}`
	_, err := h.run(t)
	var re *RepairError
	if !errors.As(err, &re) || !strings.Contains(err.Error(), "code problem") || !strings.Contains(err.Error(), "LoginViewModel.kt:42") {
		t.Fatalf("err = %v", err)
	}
	if r, _ := LoadRecipe(h.j.Repo.Path); len(r.GradleArgs) != 0 || len(r.History) != 0 {
		t.Errorf("a source error must not change the recipe: %+v", r)
	}
}

// A repair session that edits the project is rejected - and not reverted.
func TestRepairThatEditsFilesIsRejected(t *testing.T) {
	h := newHarness(t)
	t.Setenv("NEED_FLAG", "-Pnever")
	h.edit = true
	h.answer = `{"status":"ok","gradle_args":["-Pnever"],"explanation":"x"}`
	_, err := h.run(t)
	if err == nil || !strings.Contains(err.Error(), "changed project files") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(h.j.Repo.Path, "app", "build.gradle.kts")); !strings.Contains(string(b), "hacked") {
		t.Error("the edit must be left for the developer to inspect, not reverted")
	}
}

func TestNoRepairAndHeldDevice(t *testing.T) {
	h := newHarness(t)
	t.Setenv("NEED_FLAG", "-Pnever")
	h.j.NoRepair = true
	var se *StepError
	if _, err := h.run(t); !errors.As(err, &se) || se.Step != StepBuild || h.agentCalls != 0 {
		t.Fatalf("--no-repair: err=%v calls=%d", err, h.agentCalls)
	}

	// An agent's verify holds the device: Run app refuses before building.
	st := h.j.Store
	_, _ = st.Claim("PLEX-1", "/r", "x")
	_ = st.SetState("PLEX-1", store.StateBuilding, 0)
	_ = st.SetPID("PLEX-1", os.Getpid())
	_ = st.SetDeviceWait("PLEX-1")
	_, _ = st.AcquireDevice("PLEX-1", func(int) bool { return true })
	before := count(h.gradleLog, "assemble")
	if _, err := h.run(t); err == nil || !strings.Contains(err.Error(), "PLEX-1's agent is using the device") {
		t.Fatalf("held device: %v", err)
	}
	if count(h.gradleLog, "assemble") != before {
		t.Error("nothing may build while an agent holds the device")
	}
}

func TestUnknownVariant(t *testing.T) {
	h := newHarness(t)
	h.j.Variant = "prodRelease"
	if _, err := h.run(t); err == nil || !strings.Contains(err.Error(), "has no variant") {
		t.Fatalf("err = %v", err)
	}
}

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }

// While Run app runs, the device says it is Run app on that target; after,
// it is free again - and a device you had taken back goes back to that.
func TestRunAppHoldsWithItsPurpose(t *testing.T) {
	h := newHarness(t)
	h.j.Target = "PLEX-12"
	h.j.Dir = h.j.Repo.Path
	var during string
	h.j.Step = func(step, _ string) {
		if step == StepBuild {
			d, _ := h.j.Store.ActiveDevice()
			during = d.RunAppTarget()
		}
	}
	old := ensureProjectFilesFn
	ensureProjectFilesFn = func(_ *config.Config, repo *config.Repo, _ string, _ func(string, ...interface{})) string {
		return repo.Path
	}
	defer func() { ensureProjectFilesFn = old }()
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if during != "PLEX-12" {
		t.Errorf("during the build the device said %q, want Run app on PLEX-12", during)
	}
	if d, _ := h.j.Store.ActiveDevice(); d.Holder != "" || d.Purpose != "" {
		t.Errorf("after: %+v", d)
	}

	_, _ = h.j.Store.ReserveDevice(store.PurposeTakenBack + "PLEX-9")
	if _, err := h.run(t); err != nil {
		t.Fatal(err)
	}
	if d, _ := h.j.Store.ActiveDevice(); d.Holder != store.HolderYou || d.TakenBackFrom() != "PLEX-9" {
		t.Errorf("a device you had taken back should stay yours, as before: %+v", d)
	}
}
