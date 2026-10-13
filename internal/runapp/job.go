// One run of the app: prepare → device → sync → build → reinstall → launch,
// with at most one repair and one retry. A run that succeeds without a repair
// makes no agent call at all.
package runapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/runner"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Job is one run.
type Job struct {
	Cfg          *config.Config
	Repo         *config.Repo
	Target       string // "main" or a ticket id
	Dir          string // the checkout: the repo itself, or the ticket's worktree
	Module       string // "" = last used / first app module
	Variant      string // "" = last used / Studio's default
	Store        *store.Store
	ForceSync    bool
	NoRepair     bool
	AnthropicKey string
	Log          io.Writer
	Logf         func(string, ...interface{})
	Step         func(step, detail string) // progress, for the store row
}

func (j Job) logf(format string, a ...interface{}) {
	if j.Logf != nil {
		j.Logf(format, a...)
	}
}

func (j Job) step(step, detail string) {
	j.logf("[run-app] %s: %s", step, detail)
	if j.Step != nil {
		j.Step(step, detail)
	}
}

// Result is what ran.
type Result struct {
	Module, Variant, AppID, Activity, Serial string
	Repaired                                 bool
}

// Seams over the runner helpers.
var (
	ensureDevice         = runner.EnsureDevice
	ensureProjectFilesFn = runner.EnsureProjectFiles
)

// buildTimeout bounds each Gradle invocation.
var buildTimeout = 30 * time.Minute

// Run runs the app on the active device.
func Run(ctx context.Context, j Job) (Result, error) {
	var res Result
	projectDir := j.Repo.Path
	if j.Target != "main" {
		j.step(StepPrepare, "making the worktree buildable")
		projectDir = ensureProjectFilesFn(j.Cfg, j.Repo, j.Dir, j.logf)
	} else {
		j.step(StepPrepare, "main checkout "+projectDir+" (builds your local edits too)")
	}

	j.step(StepDevice, "bringing up the active device")
	serial, err := ensureDevice(ctx, j.Cfg, j.Store, j.logf)
	if err != nil {
		return res, &StepError{Step: StepDevice, Msg: err.Error()}
	}
	release, err := takeDevice(j.Store, j.Target)
	if err != nil {
		return res, err
	}
	defer release()
	res.Serial = serial
	j.step(StepDevice, serial+" ready")

	recipe, err := LoadRecipe(j.Repo.Path)
	if err != nil {
		return res, err
	}
	env := Env{ADB: emulator.ResolvePaths(j.Cfg.AndroidSDKPath).ADB, Log: j.Log, Timeout: buildTimeout,
		ToolEnv: agent.ToolEnv(emulator.RootFor(j.Cfg.AndroidSDKPath), j.logf)}

	attempt := func() error {
		mod, variant, err := sync(ctx, j, env, recipe, projectDir)
		if err != nil {
			return err
		}
		res.Module, res.Variant = mod.Path, variant
		return buildAndLaunch(ctx, j, env, recipe, projectDir, serial, mod, variant, &res)
	}
	err = attempt()
	var se *StepError
	if err != nil && errors.As(err, &se) && se.Repairable && !j.NoRepair && ctx.Err() == nil {
		j.step(StepRepair, se.Step+" failed - asking the agent what changed")
		if rerr := repair(ctx, j, recipe, projectDir, serial, res.Module, res.Variant, se); rerr != nil {
			return res, rerr
		}
		res.Repaired = true
		recipe.SyncHash = "" // a repair may change the Gradle root or flags: scan again
		err = attempt()
	}
	if err != nil {
		return res, err
	}
	recipe.LastModule, recipe.LastVariant = res.Module, res.Variant
	if serr := SaveRecipe(j.Repo.Path, recipe); serr != nil {
		j.logf("[run-app] (warn) could not save the run recipe: %v", serr)
	}
	return res, nil
}

// takeDevice reserves the active device for the human running the app - with
// the target as its purpose, so the dashboard shows which ticket and branch
// are running. An agent holding it is refused (the dashboard offers to take
// it back first). A device that was already yours stays yours afterwards,
// with its previous purpose.
func takeDevice(st *store.Store, target string) (func(), error) {
	d, err := st.ActiveDevice()
	if err != nil {
		return func() {}, err
	}
	purpose := store.PurposeRunApp + target
	if d.Holder == store.HolderYou {
		_ = st.SetHoldPurpose(purpose)
		return func() { _ = st.SetHoldPurpose(d.Purpose) }, nil
	}
	if ok, err := st.ReserveDevice(purpose); err != nil || !ok {
		return func() {}, &StepError{Step: StepDevice,
			Msg: fmt.Sprintf("%s's agent is using the device - wait for it, or take it back from the dashboard", d.Holder)}
	}
	return func() { _, _ = st.ReleaseDevice(store.HolderYou) }, nil
}

// sync scans the Gradle tasks when the build files changed (or never were),
// then picks the module and variant.
func sync(ctx context.Context, j Job, env Env, r *Recipe, projectDir string) (AppModule, string, error) {
	gradleDir := filepath.Join(projectDir, r.GradleDir)
	hash := SyncHash(projectDir)
	if j.ForceSync || r.SyncHash != hash || len(r.Model.Apps) == 0 {
		j.step(StepSync, "./gradlew tasks --all")
		model, err := Sync(ctx, env, gradleDir, r.GradleArgs)
		if err != nil {
			return AppModule{}, "", err
		}
		r.Model, r.SyncHash = model, hash
	} else {
		j.step(StepSync, "cached (build files unchanged)")
	}
	modPath := firstNonEmpty(j.Module, r.LastModule)
	mod, ok := r.Model.Module(modPath)
	if !ok {
		mod = r.Model.Apps[0]
	}
	variant := firstNonEmpty(j.Variant, r.LastVariant)
	if !contains(mod.Variants, variant) {
		if j.Variant != "" {
			return mod, "", &StepError{Step: StepSync, Msg: fmt.Sprintf("module %s has no variant %q (it has %s)",
				mod.Path, j.Variant, strings.Join(mod.Variants, ", "))}
		}
		variant = DefaultVariant(mod.Variants)
	}
	return mod, variant, nil
}

func buildAndLaunch(ctx context.Context, j Job, env Env, r *Recipe, projectDir, serial string, mod AppModule, variant string, res *Result) error {
	gradleDir := filepath.Join(projectDir, r.GradleDir)
	o := r.Overrides[variant]
	task := firstNonEmpty(o.AssembleTask, AssembleTask(mod.Path, variant))
	j.step(StepBuild, "./gradlew "+task)
	if err := Build(ctx, env, gradleDir, task, r.GradleArgs, r.envLines(serial)); err != nil {
		return err
	}
	moduleDir := filepath.Join(gradleDir, filepath.FromSlash(strings.ReplaceAll(strings.TrimPrefix(mod.Path, ":"), ":", "/")))
	out, err := ReadOutput(moduleDir, variant)
	if err != nil {
		return err
	}
	if o.ApplicationID != "" {
		out.ApplicationID = o.ApplicationID
	}
	res.AppID = out.ApplicationID
	j.step(StepReinstall, "removing the old "+out.ApplicationID+" (clears its data), installing the new APK")
	if err := Reinstall(ctx, env, serial, out); err != nil {
		return err
	}
	act := o.LaunchActivity
	if act == "" {
		if act, err = ResolveActivity(ctx, env, serial, out.ApplicationID); err != nil {
			return err
		}
	}
	res.Activity = act
	j.step(StepLaunch, act)
	return Launch(ctx, env, serial, out.ApplicationID, act)
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
