// The DEVICE section's operations. Each runs inside a tea.Cmd and reports a
// deviceOpMsg; the adb/emulator/process calls go through package-level seams
// so tests can replace them.
package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// deviceOpMsg reports a finished operation.
type deviceOpMsg struct {
	notice string
	err    error
}

// Seams over everything that touches the SDK or another process.
var (
	startEmulator = emulator.StartWindowed
	stopSerial    = emulator.StopSerial
	showWindow    = emulator.ShowWindow
	killEmulator  = emulator.Kill
	serialForAVD  = emulator.SerialForAVD
	stopTicketRun = stopRun
)

// stopWait bounds how long a stop/restart waits for the emulator to detach.
var stopWait = 30 * time.Second

func sdkPaths() emulator.SDKPaths { return emulator.ResolvePaths(sdkPathFromConfig()) }

func opDone(notice string, err error) tea.Msg { return deviceOpMsg{notice: notice, err: err} }

// startDeviceCmd makes avd the active device (if it isn't) and launches it
// windowed. Only one launcher wins BeginStart; a second start is a no-op.
func (m monitorModel) startDeviceCmd(avd string, o emulator.StartOpts) tea.Cmd {
	st := m.store
	return func() tea.Msg {
		if d, _ := st.ActiveDevice(); d.Kind != store.DeviceEmulator || d.AVD != avd {
			if err := st.UseEmulator(avd, ""); err != nil {
				return opDone("could not switch to "+avd, err)
			}
		}
		won, err := st.BeginStart(avd, 0)
		if err != nil {
			return opDone("could not start "+avd, err)
		}
		if !won {
			return opDone(avd+" is already starting or running", nil)
		}
		o.LogPath = paths.EmulatorLog()
		pid, exited, err := startEmulator(sdkPaths(), avd, o)
		if err != nil {
			_ = st.MarkOff()
			return opDone("could not start "+avd, err)
		}
		_ = st.SetStartPID(pid)
		go func() { <-exited }() // reaped; a dead launch is noticed by the next scan
		return opDone("starting "+avd+" - its window will open", nil)
	}
}

// cancelStartCmd kills a launch that hasn't finished booting.
func (m monitorModel) cancelStartCmd() tea.Cmd {
	st := m.store
	return func() tea.Msg {
		d, _ := st.ActiveDevice()
		if d.Serial != "" {
			_ = stopSerial(sdkPaths(), d.Serial)
		} else {
			killEmulator(d.PID)
		}
		return opDone("cancelled starting "+d.AVD, st.MarkOff())
	}
}

// stopDeviceCmd shuts the emulator down (snapshot saved). An agent holding it
// is taken back first - the confirm said its verify would stop.
func (m monitorModel) stopDeviceCmd() tea.Cmd {
	st, holder := m.store, m.holderSession()
	return func() tea.Msg {
		if holder != nil {
			takeBack(st, *holder)
		}
		d, _ := st.ActiveDevice()
		if err := stopAndWait(st, d); err != nil {
			return opDone("could not stop "+d.Label(), err)
		}
		return opDone("stopped "+d.Label(), nil)
	}
}

// restartDeviceCmd stops the emulator and launches it again.
func (m monitorModel) restartDeviceCmd() tea.Cmd {
	st := m.store
	start := m.startDeviceCmd
	return func() tea.Msg {
		d, _ := st.ActiveDevice()
		if err := stopAndWait(st, d); err != nil {
			return opDone("could not restart "+d.AVD, err)
		}
		return start(d.AVD, emulator.StartOpts{})()
	}
}

// stopAndWait asks the emulator to quit and waits for it to detach.
func stopAndWait(st *store.Store, d store.Device) error {
	if d.Serial != "" {
		if err := stopSerial(sdkPaths(), d.Serial); err != nil {
			return err
		}
		deadline := time.Now().Add(stopWait)
		for time.Now().Before(deadline) {
			if _, up := serialForAVD(sdkPaths(), d.AVD); !up {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	} else {
		killEmulator(d.PID)
	}
	return st.MarkOff()
}

// takeBackCmd gives the device to you and parks the agent's ticket.
func (m monitorModel) takeBackCmd() tea.Cmd {
	st, holder := m.store, m.holderSession()
	return func() tea.Msg {
		if holder == nil {
			return opDone("no agent is using the device", nil)
		}
		takeBack(st, *holder)
		return opDone("took the device back from "+holder.Ticket+" - it is parked in NEEDS YOU; Resume re-queues it", nil)
	}
}

// takeBack moves the hold to you FIRST (atomic, so no waiter can slip in
// between), then parks the ticket - state written before the signal, like
// Pause - and stops its run. Its own deferred release then no-ops.
func takeBack(st *store.Store, s store.Session) {
	purpose := store.PurposeTakenBack + s.Ticket
	if ok, _ := st.TakeDevice(s.Ticket, store.HolderYou, purpose); !ok {
		_, _ = st.ReserveDevice(purpose)
	}
	_ = st.SetState(s.Ticket, store.StateNeedsYou, s.Retries)
	appendTicketLog(s.Ticket, fmt.Sprintf("[%s] needs-you: device taken back by you - Resume re-queues it for the device", s.Ticket))
	stopTicketRun(s)
	_ = st.ExpirePendingApprovals(s.Ticket)
	_ = st.ClearDeviceWait(s.Ticket)
}

// releaseDeviceCmd hands your hold back to the agents' queue.
func (m monitorModel) releaseDeviceCmd() tea.Cmd {
	st := m.store
	return func() tea.Msg {
		if ok, err := st.ReleaseDevice(store.HolderYou); err != nil || !ok {
			return opDone("could not release the device", err)
		}
		return opDone("released the device - the next agent in line can use it", nil)
	}
}

// useDeviceCmd makes a picked device the active one. stopCurrent first stops
// the emulator pie launched (one emulator at a time); then a picked emulator
// is started when start is set and it isn't already running.
func (m monitorModel) useDeviceCmd(it pickItem, stopCurrent, start bool) tea.Cmd {
	st := m.store
	launch := m.startDeviceCmd
	return func() tea.Msg {
		if stopCurrent {
			if d, _ := st.ActiveDevice(); d.State != store.DeviceOff {
				if err := stopAndWait(st, d); err != nil {
					return opDone("could not stop "+d.Label(), err)
				}
			}
		}
		if it.usb {
			if err := st.UseUSB(it.serial); err != nil {
				return opDone("could not use "+it.label, err)
			}
			return opDone("using "+it.label, nil)
		}
		if err := st.UseEmulator(it.avd, it.serial); err != nil {
			return opDone("could not use "+it.avd, err)
		}
		if start && it.serial == "" {
			return launch(it.avd, emulator.StartOpts{})()
		}
		return opDone("using "+it.avd, nil)
	}
}

// forgetDeviceCmd stops using a phone (it is never powered off).
func (m monitorModel) forgetDeviceCmd() tea.Cmd {
	st := m.store
	return func() tea.Msg { return opDone("stopped using the phone", st.ForgetDevice()) }
}

// showWindowCmd raises the emulator window.
func showWindowCmd() tea.Cmd {
	return func() tea.Msg {
		if err := showWindow(); err != nil {
			return opDone("could not show the emulator window", err)
		}
		return opDone("", nil)
	}
}

// holderSession is the ticket session holding the device, if an agent does.
func (m monitorModel) holderSession() *store.Session {
	h := m.device.scan.dev.Holder
	if h == "" || h == store.HolderYou {
		return nil
	}
	for i := range m.flat {
		if m.flat[i].Ticket == h {
			s := m.flat[i]
			return &s
		}
	}
	return &store.Session{Ticket: h}
}
