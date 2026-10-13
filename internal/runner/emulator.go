// The device a run tests on. Golden rule: pie works with ONE active device (an
// emulator or a USB phone, chosen in the dashboard) and ONE agent uses it at a
// time - tickets queue for it first-come first-served. Emulators open
// WINDOWED: the user watches what agents do. Every failure here degrades the
// run to unit tests; an unavailable device is a worse reason to abandon a fix
// than a narrower test pass.
package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/agent"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// The wait budgets. Vars rather than consts only so tests can narrow them.
var (
	// emulatorWaitTimeout bounds how long a run waits for the device (its
	// turn in the queue, or a user who stopped it) before verifying with unit
	// tests only.
	emulatorWaitTimeout = 20 * time.Minute
	// emulatorPollInterval is how often a waiter retries.
	emulatorPollInterval = 5 * time.Second
	// deviceBootTimeout bounds one emulator boot.
	deviceBootTimeout = 5 * time.Minute
	// holderGraceSecs keeps a fresh holder from being reclaimed before it
	// recorded its pid.
	holderGraceSecs = int64(store.LeaseGrace / time.Second)
)

// errNoDevice means nobody picked a device and there is no AVD to fall back to.
var errNoDevice = errors.New("no active device - pick one in the dashboard's DEVICE row")

// deviceBoot is the outcome of bringing the active device up.
type deviceBoot struct {
	serial string
	err    error
}

// startDeviceEarly brings the active device up in the background, so an
// emulator boot overlaps the implement stage instead of blocking verify. nil
// when the run has no store (nothing to coordinate through).
func startDeviceEarly(ctx context.Context, t Task, logf logFn) <-chan deviceBoot {
	if t.Store == nil {
		return nil
	}
	ch := make(chan deviceBoot, 1)
	go func() {
		serial, err := ensureDeviceRunning(ctx, t, logf)
		ch <- deviceBoot{serial, err}
	}()
	return ch
}

// acquireForRun is the whole sequence for a run that re-enters an existing
// worktree (--resume, --address-comments, change rework): bring the device up,
// wait for this ticket's turn, return the serial the agent must use. release
// is always safe to call.
func acquireForRun(ctx context.Context, t Task, plan *agent.Plan, logf logFn) (serial string, release func()) {
	if plan == nil || !plan.NeedsEmulator {
		return "", func() {}
	}
	return claimDevice(ctx, t, startDeviceEarly(ctx, t, logf), logf)
}

// claimDevice waits for the boot started by startDeviceEarly, then queues for
// the device and takes it when this ticket is at the head. "" = unit tests.
func claimDevice(ctx context.Context, t Task, boot <-chan deviceBoot, logf logFn) (string, func()) {
	noop := func() {}
	if boot == nil {
		return "", noop
	}
	// Join the queue before the boot finishes: arrival order is when the
	// ticket asked, not when the emulator happened to come up.
	_ = t.Store.SetDeviceWait(t.Ticket)
	leave := func() { _ = t.Store.ClearDeviceWait(t.Ticket) }
	var b deviceBoot
	select {
	case b = <-boot:
	case <-ctx.Done():
		leave()
		return "", noop
	}
	if b.err != nil {
		leave()
		logf("[%s] (warn) device unavailable: %v - falling back to unit tests", t.Ticket, b.err)
		return "", noop
	}
	if !acquireDevice(ctx, t, logf) {
		leave()
		logf("[%s] (warn) device still busy after %s - falling back to unit tests", t.Ticket, emulatorWaitTimeout)
		return "", noop
	}
	d, _ := t.Store.ActiveDevice()
	serial := d.Serial
	if serial == "" {
		serial = b.serial
	}
	logf("[%s] device %s acquired (%s)", t.Ticket, d.Label(), serial)
	return serial, func() { _, _ = t.Store.ReleaseDevice(t.Ticket) }
}

// acquireDevice waits for this ticket's turn: it is first in line AND the
// device is running and free. A holder whose run died is reclaimed on the way.
func acquireDevice(ctx context.Context, t Task, logf logFn) bool {
	deadline := time.Now().Add(emulatorWaitTimeout)
	waiting := false
	for {
		if ok, _ := t.Store.AcquireDevice(t.Ticket, proc.Alive); ok {
			return true
		}
		if holder, ok, err := t.Store.ReclaimStaleHolder(holderGraceSecs, proc.Alive); err == nil && ok {
			logf("[device] reclaimed the device from %s - its run is gone", holder)
			continue
		}
		if time.Now().After(deadline) {
			return false
		}
		if !waiting {
			logf("[%s] waiting for device…", t.Ticket)
			waiting = true // one log line, not one per poll
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(emulatorPollInterval):
		}
	}
}

// ensureDeviceRunning makes the active device usable and returns its serial.
// A USB phone must be attached and authorized. An emulator is adopted only if
// it is THIS AVD - any other running emulator belongs to the user - or else
// launched windowed by exactly one runner while the others wait for it.
func ensureDeviceRunning(ctx context.Context, t Task, logf logFn) (string, error) {
	st := t.Store
	p := emulator.ResolvePaths(t.Cfg.AndroidSDKPath)
	d, err := activeOrSeed(st, t, p, logf)
	if err != nil {
		return "", err
	}
	if d.Kind == store.DeviceUSB {
		return usbReady(p, d.Serial)
	}
	if serial, ok := emulator.SerialForAVD(p, d.AVD); ok {
		if d.State != store.DeviceRunning {
			_ = st.MarkRunning(d.AVD, serial) // launched outside pie, or our boot finished: adopt
		}
		if emulator.BootCompleted(p, serial) {
			return serial, nil
		}
	} else if d.State == store.DeviceRunning {
		_ = st.MarkOff() // the store says running but it's gone (stopped by hand, crashed)
	}
	if won, _ := st.BeginStart(d.AVD, 0); won {
		logf("[device] starting %s (windowed)", d.AVD)
		pid, exited, err := emulator.StartWindowed(p, d.AVD, emulator.StartOpts{LogPath: paths.EmulatorLog()})
		if err != nil {
			_ = st.MarkOff()
			return "", err
		}
		_ = st.SetStartPID(pid)
		go func() {
			if werr := <-exited; werr != nil {
				logf("[device] %s exited: %v (see %s)", d.AVD, werr, paths.EmulatorLog())
			}
		}()
	} else {
		logf("[device] %s is starting - waiting for it", d.AVD)
	}
	bootCtx, cancel := context.WithTimeout(ctx, deviceBootTimeout)
	defer cancel()
	serial, err := emulator.WaitAVDReady(bootCtx, p, d.AVD)
	if err != nil {
		if cur, _ := st.ActiveDevice(); cur.State == store.DeviceStarting && cur.AVD == d.AVD {
			_ = st.MarkOff()
		}
		return "", err
	}
	_ = st.MarkRunning(d.AVD, serial)
	logf("[device] %s ready (%s)", d.AVD, serial)
	return serial, nil
}

// activeOrSeed returns the active device, seeding it once from config
// avd_name (or the only/first AVD, so instrumented tests work with zero
// config) when the user never chose one.
func activeOrSeed(st *store.Store, t Task, p emulator.SDKPaths, logf logFn) (store.Device, error) {
	d, err := st.ActiveDevice()
	if err != nil {
		return d, err
	}
	if d.None() {
		if avd := resolveAVD(t.Cfg.AVDName, p, logf); avd != "" {
			_ = st.SeedDevice(avd)
			d, err = st.ActiveDevice()
		}
	}
	if err == nil && d.None() {
		err = errNoDevice
	}
	return d, err
}

// usbReady checks the phone is attached and authorized.
func usbReady(p emulator.SDKPaths, serial string) (string, error) {
	devs, err := emulator.ListDevices(p)
	if err != nil {
		return "", fmt.Errorf("adb: %w", err)
	}
	for _, dv := range devs {
		if dv.Serial == serial {
			if dv.Ready() {
				return serial, nil
			}
			return "", fmt.Errorf("phone %s is %s", serial, dv.State)
		}
	}
	return "", fmt.Errorf("phone %s is not connected", serial)
}

// resolveAVD is the AVD to seed the active device with: config avd_name, else
// the first AVD `emulator -list-avds` reports. "" when there is none.
func resolveAVD(configured string, p emulator.SDKPaths, logf func(string, ...interface{})) string {
	if configured != "" {
		return configured
	}
	avds := emulator.ListAVDs(p)
	if len(avds) == 0 {
		return ""
	}
	logf("[device] no device chosen and no avd_name configured - using AVD %q", avds[0])
	return avds[0]
}

// orNone renders an empty serial for log lines.
func orNone(serial string) string {
	if serial == "" {
		return "none (unit tests only)"
	}
	return serial
}
