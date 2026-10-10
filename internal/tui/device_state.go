// The dashboard's DEVICE section: what it knows and how it stays current.
// Golden rule shown on screen: pie works with ONE device and ONE agent uses
// it at a time. The section is one cursor row plus a detail pane with an
// ACTIONS list (device_view.go, device_actions.go); this file is its data.
//
// adb is never called on the render loop or the 1s tick: a separate 2s tick
// runs one scan at a time inside a tea.Cmd (the in-flight guard), and every
// adb/emulator call goes through a package-level seam so tests - and the
// monkey test, which never runs Cmds - never touch a real SDK.
package tui

import (
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/config"
	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/proc"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// deviceState is the DEVICE section's data and interaction state.
type deviceState struct {
	scan     deviceScan
	scanned  bool
	inflight bool

	focus     bool // the ACTIONS list has the keyboard
	actionSel int
	picker    *devicePicker
	confirm   *deviceConfirm
	busy      string // an operation in progress, shown in the detail pane
}

// deviceScan is one look at the device: the store row (truth for state and
// holder) plus what adb and the AVD files say.
type deviceScan struct {
	dev      store.Device
	info     emulator.AVDInfo   // the active AVD
	attached []emulator.Device  // adb devices -l
	avds     []emulator.AVDInfo // every installed AVD, for the pickers
	android  string             // USB: Android version
	waiters  []store.Waiter
	bootSecs int64
	err      error // adb could not run (no SDK)

	// For a device you hold for Run app on the main checkout: what it is.
	mainRepo, mainBranch string
	appRun               store.AppRun // the latest Run app (its variant)
}

type deviceTickMsg struct{}
type deviceScannedMsg struct{ scan deviceScan }

// devicePoll is the scan cadence while the hub is open.
const devicePoll = 2 * time.Second

func deviceTick() tea.Cmd {
	return tea.Tick(devicePoll, func(time.Time) tea.Msg { return deviceTickMsg{} })
}

// scanDevice is the seam every scan goes through.
var scanDevice = realScanDevice

// scanCmd runs one scan off the render loop.
func (m monitorModel) scanCmd() tea.Cmd {
	st := m.store
	if st == nil {
		return nil
	}
	sdk, avdName, repo := "", "", ""
	if cfg, err := config.Load(); err == nil {
		sdk, avdName = cfg.AndroidSDKPath, cfg.AVDName
		if len(cfg.Repos) > 0 {
			repo = cfg.Repos[0].Path
		}
	}
	return func() tea.Msg {
		sc := scanDevice(st, sdk, avdName)
		sc.appRun, _ = st.LatestAppRun()
		if repo != "" && sc.dev.RunAppTarget() == "main" {
			sc.mainRepo, sc.mainBranch = repo, gitCurrentBranch(repo)
		}
		return deviceScannedMsg{scan: sc}
	}
}

// gitCurrentBranch is the checked-out branch of repo ("" if detached/unknown).
func gitCurrentBranch(repo string) string {
	out, err := exec.Command("git", "-C", repo, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sdkPathFromConfig reads android_sdk_path; "" lets the emulator package
// auto-detect.
func sdkPathFromConfig() string {
	if cfg, err := config.Load(); err == nil {
		return cfg.AndroidSDKPath
	}
	return ""
}

// realScanDevice reads the store, asks adb what is attached, and settles the
// active emulator's state against reality: a launch that finished booting is
// marked running, one that died is marked off, and one closed by hand is
// marked off - the dashboard is also the janitor when no runner is.
func realScanDevice(st *store.Store, sdk, avdName string) deviceScan {
	var sc deviceScan
	p := emulator.ResolvePaths(sdk)
	d, _ := st.ActiveDevice()
	if d.None() {
		// The same seeding rule the runner uses: avd_name, else the first AVD.
		if avd := firstOr(emulator.ListAVDs(p), ""); avdName != "" || avd != "" {
			if avdName != "" {
				avd = avdName
			}
			_ = st.SeedDevice(avd)
			d, _ = st.ActiveDevice()
		}
	}
	sc.attached, sc.err = emulator.ListDevices(p)
	if sc.err == nil && d.Kind == store.DeviceEmulator {
		settleEmulator(st, p, d, sc.attached)
		d, _ = st.ActiveDevice()
	}
	if _, ok, _ := st.ReclaimStaleHolder(int64(store.LeaseGrace/time.Second), proc.Alive); ok {
		d, _ = st.ActiveDevice()
	}
	sc.dev = d
	home := emulator.AVDHome()
	if d.Kind == store.DeviceEmulator {
		sc.info = emulator.ReadAVDInfo(home, d.AVD)
		sc.bootSecs = st.BootSecs(d.AVD)
	}
	if d.Kind == store.DeviceUSB && attachedReady(sc.attached, d.Serial) {
		sc.android = emulator.AndroidVersion(p, d.Serial)
	}
	for _, name := range emulator.ListAVDs(p) {
		sc.avds = append(sc.avds, emulator.ReadAVDInfo(home, name))
	}
	sc.waiters, _ = st.DeviceWaiters()
	return sc
}

// settleEmulator reconciles the store's view of the active emulator with adb.
func settleEmulator(st *store.Store, p emulator.SDKPaths, d store.Device, attached []emulator.Device) {
	serial := ""
	for _, a := range attached {
		if a.Kind == emulator.KindEmulator && emulator.AVDName(p, a.Serial) == d.AVD {
			serial = a.Serial
			break
		}
	}
	switch {
	case serial != "" && d.State != store.DeviceRunning && emulator.BootCompleted(p, serial):
		_ = st.MarkRunning(d.AVD, serial)
	case serial == "" && d.State == store.DeviceRunning:
		_ = st.MarkOff() // closed by hand, or crashed
	case serial == "" && d.State == store.DeviceStarting && d.PID > 0 && !proc.Alive(d.PID):
		_ = st.MarkOff() // the launch died before it attached
	}
}

func attachedReady(devs []emulator.Device, serial string) bool {
	for _, d := range devs {
		if d.Serial == serial {
			return d.Ready()
		}
	}
	return false
}

func firstOr(xs []string, def string) string {
	if len(xs) > 0 {
		return xs[0]
	}
	return def
}

// handleDeviceMsg applies the section's messages. The tick re-arms itself
// and starts a scan only when none is in flight.
func (m monitorModel) handleDeviceMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case deviceTickMsg:
		if m.device.inflight {
			return m, deviceTick()
		}
		m.device.inflight = true
		return m, tea.Batch(deviceTick(), m.scanCmd())
	case deviceScannedMsg:
		m.device.inflight = false
		m.device.scan = msg.scan
		m.device.scanned = true
		if n := len(m.deviceActions()); m.device.actionSel >= n {
			m.device.actionSel = max(n-1, 0)
		}
		return m, nil
	case deviceOpMsg:
		m.device.busy = ""
		if msg.err != nil {
			m.notice = msg.notice + ": " + msg.err.Error()
		} else if msg.notice != "" {
			m.notice = msg.notice
		}
		m.device.inflight = true
		return m, m.scanCmd()
	}
	return m, nil
}

// holds reports whether ticket is the device's current holder.
func (d deviceState) holds(ticket string) bool { return d.scan.dev.Holder == ticket }

// waiting reports whether ticket is queued for the device.
func (d deviceState) waiting(ticket string) bool {
	for _, w := range d.scan.waiters {
		if w.Ticket == ticket {
			return true
		}
	}
	return false
}
