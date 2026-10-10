package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/runapp"
	"github.com/Apple-Pie-AI/pie-tui/internal/secrets"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// `pie run-app` builds, installs and launches the app on the active device -
// Android Studio's Run, deterministically. The dashboard spawns it detached
// (--job names the row it follows); it works the same from a shell.
func init() {
	var target, module, variant, repoPath, job string
	var sync, noRepair bool
	c := &cobra.Command{
		Use:   "run-app",
		Short: "Build, install and launch the app on the active device (main checkout or a ticket)",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			repo, err := cfg.Resolve(repoPath)
			if err != nil {
				return err
			}
			if err := paths.EnsureDirs(); err != nil {
				return err
			}
			st, err := store.Open(paths.StateDB())
			if err != nil {
				return err
			}
			defer st.Close()

			dir := repo.Path
			if target != "main" {
				dir = paths.WorktreeFor(repo.Path, target)
				if !paths.WorktreeReady(dir) {
					return fmt.Errorf("%s has no worktree - nothing to build", target)
				}
			}
			if job == "" {
				job = time.Now().Format("0102-150405")
			}
			logPath := paths.RunAppLog(target)
			logFile, err := os.Create(logPath)
			if err != nil {
				return err
			}
			defer logFile.Close()
			if err := st.CreateAppRun(store.AppRun{ID: job, Target: target, Dir: dir, Module: module,
				Variant: variant, PID: os.Getpid(), LogPath: logPath}, proc.Alive); err != nil {
				return err
			}
			out := io.Writer(logFile)
			if term := os.Getenv("PIE_RUN_APP_QUIET"); term == "" {
				out = io.MultiWriter(logFile, os.Stdout)
			}
			logf := func(format string, a ...interface{}) { fmt.Fprintf(out, format+"\n", a...) }

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			res, err := runapp.Run(ctx, runapp.Job{
				Cfg: cfg, Repo: repo, Target: target, Dir: dir, Module: module, Variant: variant,
				Store: st, ForceSync: sync, NoRepair: noRepair, AnthropicKey: secrets.Get(secrets.Anthropic),
				Log: out, Logf: logf,
				Step: func(step, detail string) { _ = st.SetAppRunStep(job, step, detail) },
			})
			_ = st.SetAppRunChoice(job, res.Module, res.Variant, res.Serial)
			switch {
			case ctx.Err() != nil:
				_ = st.FinishAppRun(job, store.AppRunStopped, "stopped", res.Repaired)
				return errors.New("stopped")
			case err != nil:
				_ = st.FinishAppRun(job, store.AppRunFailed, err.Error(), res.Repaired)
				logf("[run-app] failed: %v", err)
				return err
			}
			done := fmt.Sprintf("%s (%s) running on %s", res.AppID, res.Variant, res.Serial)
			_ = st.FinishAppRun(job, store.AppRunDone, done, res.Repaired)
			logf("[run-app] ✓ %s", done)
			return nil
		},
	}
	c.Flags().StringVar(&target, "target", "main", `what to build: "main" (the repo checkout) or a ticket id (its worktree)`)
	c.Flags().StringVar(&module, "module", "", "app module, e.g. :app (default: last used, else the first)")
	c.Flags().StringVar(&variant, "variant", "", "build variant, e.g. stagingDebug (default: last used, else debug)")
	c.Flags().StringVar(&repoPath, "repo", "", "which configured repo (default: the first)")
	c.Flags().BoolVar(&sync, "sync", false, "rescan the Gradle tasks even if no build file changed")
	c.Flags().BoolVar(&noRepair, "no-repair", false, "never ask an agent to repair a failed step")
	c.Flags().StringVar(&job, "job", "", "job id (set by the dashboard)")
	_ = c.Flags().MarkHidden("job")
	rootCmd.AddCommand(c)
}
