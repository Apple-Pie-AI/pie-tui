// Package daemon runs a periodic cleanup loop for the background agent fleet.
// Ticket intake is done via `pie run` (Phase 1); the daemon owns cleanup
// and emulator lifecycle only (Decisions 2, 5, 8 - JQL polling deferred).
package daemon

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/git"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
	"github.com/Apple-Pie-AI/pie-tui/internal/vcs"
)

// Run starts the poll loop and blocks until ctx is cancelled.
// When once is true it runs a single cycle and returns (handy for live testing).
func Run(ctx context.Context, cfg *config.Config, once bool) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	st, err := store.Open(paths.StateDB())
	if err != nil {
		return err
	}
	defer st.Close()

	logf("daemon started · poll %ds", cfg.PollInterval)

	poll := func() {
		cleanupResolved(st)
		reclaimStaleLeases(st, cfg)
		idleShutdownEmulators(st, cfg)
	}

	poll()

	if once {
		logf("once: cycle complete")
		return nil
	}

	ticker := time.NewTicker(time.Duration(cfg.PollInterval) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("shutting down")
			shutdownEmulators(st, cfg)
			logf("daemon stopped")
			return nil
		case <-ticker.C:
			poll()
		}
	}
}

// mergeCheckable reports whether a session's PR is worth asking GitHub about:
// any idle row with a PR, not just review. A ticket parked in needs-you (or
// failed, or stopped) whose PR someone merged used to sit there forever -
// only review rows were checked. Active rows stay excluded: their state has
// one writer, the live pipeline (see the OpenComments comment in
// store/sessions.go), and merged/closed rows are already resolved.
func mergeCheckable(s store.Session) bool {
	return s.PRURL != "" && s.Repo != "" &&
		!store.IsActive(s.State) &&
		s.State != store.StateMerged && s.State != store.StateClosed
}

// cleanupResolved marks the session merged/closed once its PR is no longer
// open, then removes the worktree (Decision 7). The state flip is a
// compare-and-swap and it comes FIRST: the user can re-run the ticket between
// the List above and this write, and in that order the CAS loses cleanly -
// no state stomped, and crucially no worktree yanked from under a run that
// just started using it.
func cleanupResolved(st *store.Store) {
	sessions, err := st.List()
	if err != nil {
		return
	}
	for _, s := range sessions {
		// The sweep: a row can reach merged/closed WITHOUT this loop now (the
		// TUI's PR-update check, commentsPreflight), and mergeCheckable skips
		// resolved rows - so any resolved row still recording a worktree or
		// branch is reclaimed here, no gh call needed. Rows spared for dirty
		// worktrees land here too, and retry once a human cleans up.
		if (s.State == store.StateMerged || s.State == store.StateClosed) &&
			(s.Worktree != "" || s.Branch != "") && s.Repo != "" {
			reclaim(st, s, s.State, logf)
			continue
		}
		if !mergeCheckable(s) {
			continue
		}
		state, err := vcs.PRState(s.Repo, s.PRURL)
		if err != nil {
			continue
		}
		switch state {
		case "MERGED", "CLOSED":
			next := store.StateClosed
			if state == "MERGED" {
				next = store.StateMerged
			}
			swapped, err := st.SetStateIf(s.Ticket, s.State, next, s.Retries)
			if err != nil || !swapped {
				continue // the row moved on (a re-run claimed it) - not ours to touch
			}
			reclaim(st, s, next, logf)
		}
	}
}

// reclaim tears down a resolved row's worktree and local branch (dirty
// worktrees are spared - agent worktrees are clean once shipped, but a hand
// checkout can have edits in flight when someone merges the PR) and clears
// the row's fields on success so the next poll has nothing to redo. Sparing
// keeps the fields, which is what makes the retry-after-cleanup work.
func reclaim(st *store.Store, s store.Session, state string, lg func(string, ...interface{})) {
	removed, err := git.ReclaimResolved(s.Repo, s.Worktree, s.Branch)
	switch {
	case err != nil:
		lg("[%s] (warn) reclaim: %v", s.Ticket, err)
	case !removed:
		lg("[%s] PR %s but the worktree has uncommitted changes - leaving it in place", s.Ticket, state)
	default:
		_ = st.ClearWorktree(s.Ticket)
		lg("[%s] %s - worktree and branch reclaimed", s.Ticket, state)
	}
}

// reclaimStaleLeases hands back an AVD whose holding run is gone.
//
// `pie run` releases its own lease on the way out, but only when it gets to run
// its defers: a SIGKILL, a panic or a power cut leaks it. Nothing else here
// clears a leaked one - idleShutdownEmulators below only looks at ready rows, so
// a busy row with a dead holder was invisible to the janitor and every later
// ticket needing that AVD queued behind it. It must run BEFORE the idle check,
// which is what turns a reclaimed lease into a shutdown on the same poll.
func reclaimStaleLeases(st *store.Store, cfg *config.Config) {
	if cfg.AVDName == "" {
		return
	}
	holder, ok, err := st.ReclaimStaleLease(cfg.AVDName, store.LeaseGrace, proc.Alive)
	if err == nil && ok {
		logf("[emulator] reclaimed a stale lease from %s - its driver process is gone", holder)
	}
}

// idleShutdownEmulators kills any emulator idle past cfg.EmulatorIdleTimeout minutes.
func idleShutdownEmulators(st *store.Store, cfg *config.Config) {
	if cfg.AVDName == "" {
		return
	}
	state, pid, err := st.EmulatorState(cfg.AVDName)
	if err != nil || state != store.EmulatorReady {
		return
	}
	lastUsed, err := st.EmulatorLastUsed(cfg.AVDName)
	if err != nil {
		return
	}
	idleSecs := int64(cfg.EmulatorIdleTimeout) * 60
	if time.Now().Unix()-lastUsed < idleSecs {
		return
	}
	logf("[emulator] idle timeout - shutting down %s (pid %d)", cfg.AVDName, pid)
	emulator.Kill(pid)
	_ = st.SetEmulatorIdle(cfg.AVDName)
}

// shutdownEmulators kills all running emulators on daemon shutdown.
func shutdownEmulators(st *store.Store, cfg *config.Config) {
	if cfg.AVDName == "" {
		return
	}
	state, pid, err := st.EmulatorState(cfg.AVDName)
	if err != nil || state == store.EmulatorIdle || pid == 0 {
		return
	}
	logf("[emulator] daemon shutdown - killing %s (pid %d)", cfg.AVDName, pid)
	emulator.Kill(pid)
	_ = st.SetEmulatorIdle(cfg.AVDName)
}

// logf is the daemon-level log (stdout + daemon.log).
func logf(format string, a ...interface{}) {
	line := fmt.Sprintf(format, a...)
	fmt.Println(line)
	if f, err := os.OpenFile(paths.DaemonLog(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "%s  %s\n", time.Now().Format(time.RFC3339), line)
		f.Close()
	}
}
