// The one repair a run may get: an agent session told what failed, which may
// teach the recipe how to run the app - never change the project. Its answer
// is validated, and a session that touched any project file is rejected (not
// reverted: the main checkout may hold the developer's own work).
package runapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
)

// runAgent is the seam tests replace.
var runAgent = agent.Run

// RepairError is a repair that found the run can't be fixed by pie: the
// project's code is broken, or a human must act.
type RepairError struct {
	Status, Explanation, SuggestedCommand string
}

func (e *RepairError) Error() string {
	s := e.Explanation
	if e.SuggestedCommand != "" {
		s += " (try: " + e.SuggestedCommand + ")"
	}
	if e.Status == agent.RunSourceError {
		return "the project's code doesn't build or run - this is a code problem, not a run problem: " + s
	}
	return "needs you: " + s
}

// repair runs one repair session and, on "ok", folds its corrections into r.
func repair(ctx context.Context, j Job, r *Recipe, projectDir, serial, module, variant string, se *StepError) error {
	git.ExcludeAgentDir(projectDir)
	before := tree(projectDir)
	start := time.Now().Truncate(time.Second)
	recipeJSON, _ := json.MarshalIndent(r, "", "  ")
	prompt := agent.BuildRunRepairPrompt(agent.RunRepairInput{
		ProjectDir: projectDir, Serial: serial, Module: module, Variant: variant,
		RecipeJSON: string(recipeJSON),
		Failure: agent.RunFailure{Step: se.Step, Explanation: se.Msg, Argv: se.Argv,
			ExitCode: se.ExitCode, Tail: se.Tail},
	})
	res, err := runAgent(ctx, prompt, agent.Options{
		WorktreeDir:    gitTop(projectDir),
		AllowedTools:   agent.RunTools,
		Model:          j.Cfg.EffectiveModelRun(),
		MaxBudgetUSD:   j.Cfg.BudgetFor(config.StageRun),
		AnthropicKey:   j.AnthropicKey,
		SettingsJSON:   agent.RunSettingsJSON(j.Cfg.SandboxSettingsJSON()),
		AndroidSDKRoot: emulator.RootFor(j.Cfg.AndroidSDKPath),
		ExtraEnv:       []string{"ANDROID_SERIAL=" + serial},
		Logf:           j.logf,
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if after := tree(projectDir); after != before {
		return fmt.Errorf("the repair agent changed project files - its answer was rejected and the files left as they are for you to inspect (git status)")
	}
	if !agent.ContractFresh(projectDir, "run.json", start) {
		if res.BudgetExceeded {
			return fmt.Errorf("the repair session reached its budget (%s) - raise max_budget_run_usd", res.ErrorText)
		}
		if err != nil {
			return fmt.Errorf("the repair session failed: %v %s", err, res.ErrorText)
		}
		return errors.New("the repair session ended without an answer (.agent/run.json)")
	}
	c, err := agent.ReadRunContract(projectDir)
	if err != nil {
		return fmt.Errorf("unreadable repair answer: %w", err)
	}
	if c.Status != agent.RunRepairOK {
		return &RepairError{Status: c.Status, Explanation: c.Explanation, SuggestedCommand: c.SuggestedCommand}
	}
	next := *r
	apply(&next, c, variant)
	if err := next.Validate(); err != nil {
		return fmt.Errorf("the repair agent's correction was rejected: %w", err)
	}
	next.note(se.Step, strings.TrimSpace(c.Explanation))
	*r = next
	j.logf("[run-app] repair: %s", c.Explanation)
	return nil
}

// apply folds a contract's corrections into the recipe.
func apply(r *Recipe, c *agent.RunContract, variant string) {
	if c.GradleDir != "" {
		r.GradleDir = c.GradleDir
	}
	if len(c.GradleArgs) > 0 {
		r.GradleArgs = c.GradleArgs
	}
	if len(c.Env) > 0 {
		r.Env = c.Env
	}
	if c.AssembleTask != "" || c.ApplicationID != "" || c.LaunchActivity != "" {
		if r.Overrides == nil {
			r.Overrides = map[string]Override{}
		}
		o := r.Overrides[variant]
		if c.AssembleTask != "" {
			o.AssembleTask = c.AssembleTask
		}
		if c.ApplicationID != "" {
			o.ApplicationID = c.ApplicationID
		}
		if c.LaunchActivity != "" {
			o.LaunchActivity = c.LaunchActivity
		}
		r.Overrides[variant] = o
	}
	if c.Notes != "" {
		r.Notes = strings.TrimSpace(r.Notes + "\n" + c.Notes)
	}
}

// tree fingerprints the checkout's changes (ignoring .agent/): if a repair
// session changes it, the session edited the project.
func tree(dir string) string {
	st, _ := exec.Command("git", "-C", dir, "status", "--porcelain=v1", "-uall").Output()
	diff, _ := exec.Command("git", "-C", dir, "diff").Output()
	var keep []string
	for _, l := range strings.Split(string(st), "\n") {
		if !strings.Contains(l, ".agent/") {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n") + "\x00" + string(diff)
}

// gitTop is the checkout root - the session always runs there, like every
// other stage (a session cwd in a subdirectory loads a different project).
func gitTop(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return dir
	}
	return strings.TrimSpace(string(out))
}
