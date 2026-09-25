// Emulator coordination. Concurrent tickets share one AVD, so booting is a race
// that exactly one runner wins and the rest wait out; the store holds the lease.
package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// acquireForRun boots and claims the shared AVD for a run that re-enters an
// existing worktree (--resume, --address-comments), reporting whether the
// verify stage can actually use an emulator. Every failure degrades to unit
// tests rather than stopping the run: an unavailable emulator is a worse reason
// to abandon a fix than a narrower test pass.
//
// release is always safe to call, so callers can `defer release()` without
// checking needsEmu first. Shared with resumeShip rather than copied - the boot/
// wait/acquire/degrade sequence has four failure arms, and two hand-maintained
// copies of it would drift.
func acquireForRun(ctx context.Context, t Task, plan *agent.Plan, logf logFn) (needsEmu bool, release func()) {
	release = func() {}

	sdkPaths := emulator.ResolvePaths(t.Cfg.AndroidSDKPath)
	avdName := ""
	if plan != nil && plan.NeedsEmulator {
		avdName = resolveAVD(t.Cfg.AVDName, sdkPaths, logf)
	}
	if avdName == "" || t.Store == nil {
		return false, release
	}

	_ = t.Store.RegisterEmulator(avdName)
	ready := make(chan error, 1)
	go bootOrWait(ctx, avdName, sdkPaths, t.Store, logf, ready)
	logf("[%s] waiting for emulator…", t.Ticket)

	if err := waitForBoot(ctx, ready); err != nil {
		logf("[%s] (warn) emulator unavailable: %v - falling back to unit tests", t.Ticket, err)
		return false, release
	}
	if !acquireEmulator(ctx, t.Store, avdName, t.Ticket, logf) {
		logf("[%s] (warn) emulator still busy after %s - falling back to unit tests",
			t.Ticket, emulatorWaitTimeout)
		return false, release
	}
	return true, func() { _, _ = t.Store.ReleaseEmulator(avdName, t.Ticket) }
}

// The wait budget for the shared AVD. Vars rather than consts only so tests can
// narrow them to milliseconds - nothing in the binary reassigns them.
var (
	// emulatorWaitTimeout bounds how long a run waits for the shared AVD before
	// giving up and verifying with unit tests only. Without a deadline a single
	// leaked lease stalls every later ticket forever.
	emulatorWaitTimeout = 20 * time.Minute
	// emulatorPollInterval is how often a waiter retries the acquire.
	emulatorPollInterval = 5 * time.Second
)

// bootOrWait either reuses an already-running emulator, starts a new one, or
// waits for another runner that is already booting it. It sends exactly one
// value on done when the emulator reaches state=ready or on error.
// resolveAVD picks the AVD to use for instrumented tests: the configured one
// (config avd_name) when set, otherwise the first AVD found via
// `emulator -list-avds` - so instrumented tests work with zero config for anyone
// who has at least one AVD. Returns "" when none is available (the caller then
// falls back to unit tests).
func resolveAVD(configured string, p emulator.SDKPaths, logf func(string, ...interface{})) string {
	if configured != "" {
		return configured
	}
	avds := emulator.ListAVDs(p)
	if len(avds) == 0 {
		return ""
	}
	logf("[emulator] no avd_name configured - auto-detected AVD %q", avds[0])
	return avds[0]
}

func bootOrWait(ctx context.Context, avdName string, p emulator.SDKPaths, st *store.Store, logf func(string, ...interface{}), done chan<- error) {
	// If any emulator is already attached via adb, skip booting entirely.
	if emulator.AlreadyRunning(p) {
		logf("[emulator] already running - reusing")
		_ = st.MarkEmulatorReady(avdName) // idle/booting → ready; never steals a peer's lease
		done <- nil
		return
	}

	state, _, err := st.EmulatorState(avdName)
	if err != nil {
		done <- fmt.Errorf("emulator state: %w", err)
		return
	}

	if state == store.EmulatorIdle {
		logf("[emulator] booting %s", avdName)
		pid, err := emulator.Start(p, avdName)
		if err != nil {
			done <- err
			return
		}
		won, err := st.SetEmulatorBooting(avdName, pid)
		if err != nil {
			done <- err
			return
		}
		if !won {
			// Another runner beat us to the start; kill our orphan.
			emulator.Kill(pid)
			logf("[emulator] start race lost - waiting for peer's boot")
		}
	}

	// Wait regardless of who launched it. Derived from the run's context so
	// cancelling the ticket also abandons the boot wait.
	bootCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err := emulator.WaitReady(bootCtx, p); err != nil {
		done <- err
		return
	}
	_ = st.MarkEmulatorReady(avdName) // booting → ready
	logf("[emulator] ready")
	done <- nil
}

// waitForBoot blocks until the background boot finishes, the run is cancelled,
// or the wait budget runs out. Returns the boot error (nil = ready).
func waitForBoot(ctx context.Context, ready <-chan error) error {
	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// acquireEmulator claims the shared AVD for ticket, waiting out whichever peer
// currently holds it. It returns false when the run is cancelled or the wait
// exceeds emulatorWaitTimeout - the caller then degrades to unit tests rather
// than blocking indefinitely. On success the caller owns the lease and must
// release it.
//
// Both entrypoints that need the AVD (the full pipeline and --resume) go through
// this; each used to spin its own unbounded `for { … time.Sleep(5s) }`.
func acquireEmulator(ctx context.Context, st *store.Store, avdName, ticket string, logf func(string, ...interface{})) bool {
	deadline := time.Now().Add(emulatorWaitTimeout)
	waiting := false
	for {
		if ok, _ := st.AcquireEmulator(avdName, ticket); ok {
			return true
		}
		// The lease is held. Before sleeping, check whether its holder is actually
		// still running: a runner killed mid-verify never reaches its deferred
		// release, and that stale row would otherwise strand the AVD for good.
		reclaimStaleLease(st, avdName, logf)

		if time.Now().After(deadline) {
			return false
		}
		if !waiting {
			logf("[%s] emulator busy, waiting…", ticket)
			waiting = true // the wait is one log line, not one every 5 seconds
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(emulatorPollInterval):
		}
	}
}

// reclaimStaleLease clears a lease whose holder is gone. The liveness rule lives
// in the store (it needs the emulator↔session join); this is the runner's name
// for it plus the log line.
func reclaimStaleLease(st *store.Store, avdName string, logf func(string, ...interface{})) {
	holder, ok, err := st.ReclaimStaleLease(avdName, store.LeaseGrace, proc.Alive)
	if err == nil && ok {
		logf("[emulator] reclaimed a stale lease from %s - its driver process is gone", holder)
	}
}
