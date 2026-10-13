// The DEVICE section's interaction: the ACTIONS list each state offers, the
// device pickers, and the confirm dialogs - all ↑↓ / Enter / Esc, nothing
// else. Enter on the DEVICE row focuses ACTIONS; Esc gives the keyboard back
// to the dashboard list.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// Action ids.
const (
	daStart       = "start"
	daStartOther  = "start-other"
	daUsePhone    = "use-phone"
	daCancelStart = "cancel-start"
	daShow        = "show"
	daRestart     = "restart"
	daSwitch      = "switch"
	daStop        = "stop"
	daOpenTicket  = "open-ticket"
	daTakeBack    = "take-back"
	daRelease     = "release"
	daForget      = "forget"
	daRunApp      = "run-app"
)

type deviceAction struct {
	id, label string
	danger    bool
}

// deviceActions is the ACTIONS list for the device's current state - exactly
// the four mockup states, plus the USB and reserved-by-you variants.
func (m monitorModel) deviceActions() []deviceAction {
	sc := m.device.scan
	d := sc.dev
	if d.Kind == store.DeviceUSB {
		return []deviceAction{
			{id: daRunApp, label: "▶ Run app on this device"},
			{id: daSwitch, label: "Switch to a different device"},
			{id: daForget, label: "Stop using this phone", danger: true},
		}
	}
	switch {
	case d.None() || d.State == store.DeviceOff:
		var acts []deviceAction
		if d.AVD != "" {
			acts = append(acts, deviceAction{id: daStart, label: "Start " + d.AVD})
		}
		acts = append(acts, deviceAction{id: daStartOther, label: "Start a different emulator"})
		if len(m.usbChoices()) > 0 {
			acts = append(acts, deviceAction{id: daUsePhone, label: "Use a connected phone"})
		}
		return acts
	case d.State == store.DeviceStarting:
		return []deviceAction{{id: daCancelStart, label: "Cancel start", danger: true}}
	case d.Holder == store.HolderYou:
		return []deviceAction{
			{id: daRelease, label: "Release to agents"},
			{id: daRunApp, label: "▶ Run app on this device"},
			{id: daShow, label: "Show emulator window"},
			{id: daSwitch, label: "Switch to a different device"},
			{id: daStop, label: "Stop", danger: true},
		}
	case d.Holder != "":
		return []deviceAction{
			{id: daOpenTicket, label: "Open ticket " + d.Holder},
			{id: daShow, label: "Show emulator window"},
			{id: daTakeBack, label: "Take it back from the agent"},
			{id: daStop, label: "Stop", danger: true},
		}
	}
	return []deviceAction{
		{id: daShow, label: "Show emulator window"},
		{id: daRunApp, label: "▶ Run app on this device"},
		{id: daRestart, label: "Restart"},
		{id: daSwitch, label: "Switch to a different device"},
		{id: daStop, label: "Stop", danger: true},
	}
}

// capturing reports that the section owns the keyboard.
func (d deviceState) capturing() bool { return d.focus || d.picker != nil || d.confirm != nil }

// updateDevice routes keys while the section owns the keyboard.
func (m monitorModel) updateDevice(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.device.confirm != nil:
		return m.updateDeviceConfirm(msg)
	case m.device.picker != nil:
		return m.updateDevicePicker(msg)
	}
	acts := m.deviceActions()
	switch msg.String() {
	case "up":
		if m.device.actionSel > 0 {
			m.device.actionSel--
		}
	case "down":
		if m.device.actionSel < len(acts)-1 {
			m.device.actionSel++
		}
	case "esc":
		m.device.focus = false
	case "enter":
		if m.device.actionSel < len(acts) {
			return m.runDeviceAction(acts[m.device.actionSel].id)
		}
	}
	return m, nil
}

// focusDevice gives the ACTIONS list the keyboard (Enter on the DEVICE row).
func (m monitorModel) focusDevice() (tea.Model, tea.Cmd) {
	m.device.focus = true
	m.device.actionSel = 0
	return m, nil
}

// runDeviceAction performs one action, through a confirm or picker when the
// action needs one.
func (m monitorModel) runDeviceAction(id string) (tea.Model, tea.Cmd) {
	d := m.device.scan.dev
	busy := func(s string, cmd tea.Cmd) (tea.Model, tea.Cmd) {
		m.device.busy = s
		m.device.focus = false
		return m, cmd
	}
	switch id {
	case daStart:
		return busy("starting "+d.AVD+"…", m.startDeviceCmd(d.AVD, emulator.StartOpts{}))
	case daStartOther:
		m.device.picker = m.newPicker(pickStart, false)
	case daUsePhone:
		m.device.picker = m.newPicker(pickStart, true)
	case daSwitch:
		if d.Holder != "" && d.Holder != store.HolderYou {
			m.notice = "take the device back from " + d.Holder + " first - one agent at a time"
			return m, nil
		}
		m.device.picker = m.newPicker(pickSwitch, false)
	case daCancelStart:
		return busy("cancelling…", m.cancelStartCmd())
	case daShow:
		return m, showWindowCmd()
	case daRestart:
		return busy("restarting "+d.AVD+"…", m.restartDeviceCmd())
	case daStop:
		if d.Holder != "" && d.Holder != store.HolderYou {
			m.device.confirm = &deviceConfirm{kind: confirmStop,
				title: "Stop " + d.Label() + "?",
				body:  d.Holder + "'s verify agent is running tests on it - its verify stops and the ticket goes to NEEDS YOU.",
				opts:  []string{"Cancel", "Stop it"}}
			return m, nil
		}
		return busy("stopping "+d.Label()+"…", m.stopDeviceCmd())
	case daTakeBack:
		m.device.confirm = &deviceConfirm{kind: confirmTakeBack,
			title: "Take the device back from " + d.Holder + "?",
			body:  "Its verify stops and the ticket goes to NEEDS YOU (\"device taken back\"). Resume re-queues it.",
			opts:  []string{"Cancel", "Take it back"}}
	case daRelease:
		return busy("releasing…", m.releaseDeviceCmd())
	case daForget:
		return busy("", m.forgetDeviceCmd())
	case daRunApp:
		return m.openRunApp("")
	case daOpenTicket:
		m.device.focus = false
		m.syncCursorTo(d.Holder)
		if m.selected() != nil && m.selected().Ticket == d.Holder {
			return m.doAction(actAgentMenu)
		}
		m.notice = d.Holder + " isn't in the visible list"
	}
	return m, nil
}

// ---- pickers ---------------------------------------------------------------

const (
	pickStart  = "start"  // nothing running: pick and start
	pickSwitch = "switch" // something running: pick and switch to
)

type pickItem struct {
	label, meta string
	usb         bool
	avd, serial string // serial: the AVD is already running (adopt), or the phone
	disabled    bool
}

type devicePicker struct {
	purpose string
	items   []pickItem
	sel     int
}

// newPicker lists the AVDs and phones pie could use; usbOnly for "Use a
// connected phone". The last row is Cancel.
func (m monitorModel) newPicker(purpose string, usbOnly bool) *devicePicker {
	p := &devicePicker{purpose: purpose}
	if !usbOnly {
		for _, a := range m.device.scan.avds {
			if a.Name == m.device.scan.dev.AVD && m.device.scan.dev.Kind == store.DeviceEmulator {
				continue
			}
			p.items = append(p.items, pickItem{label: a.Name, meta: avdMeta(a), avd: a.Name})
		}
	}
	p.items = append(p.items, m.usbChoices()...)
	return p
}

// usbChoices are the attached phones (other than the active one).
func (m monitorModel) usbChoices() []pickItem {
	var items []pickItem
	for _, a := range m.device.scan.attached {
		if a.Kind != emulator.KindUSB || a.Serial == m.device.scan.dev.Serial {
			continue
		}
		it := pickItem{label: usbLabel(a), usb: true, serial: a.Serial, meta: a.Serial}
		if !a.Ready() {
			it.disabled = true
			it.meta = a.State + " · accept the prompt on the phone"
		}
		items = append(items, it)
	}
	return items
}

func (m monitorModel) updateDevicePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.device.picker
	n := len(p.items) + 1 // + Cancel
	switch msg.String() {
	case "up":
		if p.sel > 0 {
			p.sel--
		}
	case "down":
		if p.sel < n-1 {
			p.sel++
		}
	case "esc":
		m.device.picker = nil
	case "enter":
		if p.sel == len(p.items) {
			m.device.picker = nil
			return m, nil
		}
		it := p.items[p.sel]
		if it.disabled {
			m.notice = it.label + " can't be used: " + it.meta
			return m, nil
		}
		m.device.picker = nil
		d := m.device.scan.dev
		// One emulator at a time: switching away from an emulator pie
		// launched asks whether to stop it.
		if p.purpose == pickSwitch && d.Kind == store.DeviceEmulator && d.State != store.DeviceOff && d.PID > 0 {
			m.device.confirm = &deviceConfirm{kind: confirmSwitch, item: it,
				title: "Stop " + d.AVD + " and switch to " + it.label + "?",
				body:  "Apple Pie works with one emulator at a time.",
				opts:  []string{"Stop " + d.AVD + " and switch", "Switch, keep it running", "Cancel"}}
			return m, nil
		}
		m.device.busy = "switching to " + it.label + "…"
		m.device.focus = false
		return m, m.useDeviceCmd(it, false, p.purpose == pickStart)
	}
	return m, nil
}

// ---- confirms --------------------------------------------------------------

const (
	confirmStop     = "stop"
	confirmTakeBack = "take-back"
	confirmSwitch   = "switch"
)

// deviceConfirm is a destructive-action dialog. For stop and take-back the
// first option is Cancel, so a stray Enter never does the damage.
type deviceConfirm struct {
	kind, title, body string
	opts              []string
	sel               int
	item              pickItem // confirmSwitch: the device switched to
}

func (m monitorModel) updateDeviceConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.device.confirm
	switch msg.String() {
	case "up":
		if c.sel > 0 {
			c.sel--
		}
	case "down":
		if c.sel < len(c.opts)-1 {
			c.sel++
		}
	case "esc":
		m.device.confirm = nil
	case "enter":
		m.device.confirm = nil
		m.device.focus = false
		label := m.device.scan.dev.Label()
		switch {
		case c.kind == confirmStop && c.sel == 1:
			m.device.busy = "stopping " + label + "…"
			return m, m.stopDeviceCmd()
		case c.kind == confirmTakeBack && c.sel == 1:
			m.device.busy = "taking the device back…"
			return m, m.takeBackCmd()
		case c.kind == confirmSwitch && c.sel < 2:
			m.device.busy = "switching to " + c.item.label + "…"
			return m, m.useDeviceCmd(c.item, c.sel == 0, false)
		}
	}
	return m, nil
}

// ---- labels ----------------------------------------------------------------

// avdMeta is an AVD's one-line description: "API 34 · Google Play".
func avdMeta(a emulator.AVDInfo) string {
	s := "API ?"
	if a.API > 0 {
		s = fmt.Sprintf("API %d", a.API)
	}
	if a.PlayStore {
		s += " · Google Play"
	}
	return s
}

// usbLabel names a phone: "Pixel 7 (USB)".
func usbLabel(d emulator.Device) string {
	if d.Model != "" {
		return d.Model + " (USB)"
	}
	return d.Serial + " (USB)"
}
