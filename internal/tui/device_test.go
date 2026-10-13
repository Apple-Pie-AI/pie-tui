package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Apple-Pie-AI/pie-tui/internal/emulator"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

const holderBranch = "gerardosuarez/ai_task-and-notes/layer-14-task-form-ui"

// deviceModel is the golden dashboard with the DEVICE section in a given
// state and the cursor on its row. Times are relative to now so ages render
// the same on every run ("18m", "4m").
func deviceModel(state string) monitorModel {
	m := dashModel()
	m.width, m.height = 190, 40
	m.cursor = deviceRowIdx()
	now := time.Now().Unix()
	pixel := emulator.AVDInfo{Name: "Pixel_7_API_34", DisplayName: "Pixel 7 API 34", Device: "Pixel 7", API: 34, PlayStore: true}
	sc := deviceScan{
		info: pixel,
		avds: []emulator.AVDInfo{pixel, {Name: "Pixel_Tablet_API_34", API: 34}},
		attached: []emulator.Device{
			{Serial: "emulator-5554", State: "device", Kind: emulator.KindEmulator},
			{Serial: "2A281FDH3001", State: "device", Kind: emulator.KindUSB, Model: "Pixel 7"},
			{Serial: "R58N12ABCD", State: "unauthorized", Kind: emulator.KindUSB},
		},
	}
	running := store.Device{Kind: store.DeviceEmulator, AVD: "Pixel_7_API_34", Serial: "emulator-5554",
		State: store.DeviceRunning, RunningAt: now - 18*60}
	switch state {
	case "none":
		sc.dev = store.Device{Kind: store.DeviceEmulator, AVD: "Pixel_7_API_34", State: store.DeviceOff}
	case "starting":
		sc.dev = store.Device{Kind: store.DeviceEmulator, AVD: "Pixel_7_API_34", State: store.DeviceStarting,
			StartedAt: now - 48, PID: 4242}
		sc.bootSecs = 50
	case "free":
		sc.dev = running
	case "agent":
		sc.dev = running
		sc.dev.Holder, sc.dev.AcquiredAt = "PLEX-64818", now-4*60
		sc.waiters = []store.Waiter{{Ticket: "PLEX-64905", State: store.StateBuilding}}
		// The holder and the next in line are real RUNNING rows, so their
		// phase words render in the list.
		for _, g := range m.groups {
			if g.label == "RUNNING" {
				g.sessions = append(g.sessions,
					store.Session{Ticket: "PLEX-64818", Summary: "PR 14: task-form-ui", Branch: holderBranch,
						Worktree: "/work/.pie/worktrees/PLEX-64818", State: store.StateBuilding,
						PID: os.Getpid(), UpdatedAt: time.Unix(now-4*60, 0)},
					store.Session{Ticket: "PLEX-64905", Summary: "Empty state for notes tab",
						State: store.StateBuilding, PID: os.Getpid(), UpdatedAt: time.Unix(now-60, 0)})
			}
		}
		m.flat = nil
		for _, g := range m.groups {
			m.flat = append(m.flat, g.sessions...)
		}
		m.rows = buildRows(m.groups, m.expanded)
	case "you":
		sc.dev = running
		sc.dev.Holder, sc.dev.AcquiredAt = store.HolderYou, now-2*60
	case "runapp", "takenback":
		sc.dev = running
		sc.dev.Holder, sc.dev.AcquiredAt = store.HolderYou, now-60
		sc.dev.Purpose = store.PurposeRunApp + "PLEX-64818"
		if state == "takenback" {
			sc.dev.Purpose = store.PurposeTakenBack + "PLEX-64818"
		}
		sc.appRun = store.AppRun{Target: "PLEX-64818", Variant: "stagingDebug"}
		for _, g := range m.groups {
			if g.label == "NEEDS YOU" {
				g.sessions = append(g.sessions, store.Session{Ticket: "PLEX-64818", Summary: "PR 14: task-form-ui",
					Branch: holderBranch, Worktree: "/work/.pie/worktrees/PLEX-64818", State: store.StateNeedsYou,
					UpdatedAt: time.Unix(now-60, 0)})
			}
		}
		m.flat = nil
		for _, g := range m.groups {
			m.flat = append(m.flat, g.sessions...)
		}
		m.rows = buildRows(m.groups, m.expanded)
	case "usb":
		sc.dev = store.Device{Kind: store.DeviceUSB, Serial: "2A281FDH3001", State: store.DeviceRunning, RunningAt: now - 18*60}
		sc.android = "14"
	}
	m.device.scan, m.device.scanned = sc, true
	return m
}

// The four states of the mockups, plus a USB phone and "reserved by you",
// rendered whole. Run `go test ./internal/tui -update` to refresh.
func TestGoldenDeviceStates(t *testing.T) {
	for _, state := range []string{"none", "starting", "free", "agent", "you", "usb", "runapp", "takenback"} {
		t.Run(state, func(t *testing.T) {
			m := deviceModel(state)
			got := stripAnsi(m.View())
			path := filepath.Join("testdata", "device-"+state+".golden")
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run: go test ./internal/tui -update)", err)
			}
			if got != string(want) {
				t.Errorf("device-%s changed. Run `go test ./internal/tui -update` if intended.\n--- want ---\n%s\n--- got ---\n%s", state, want, got)
			}
		})
	}
}

// What each state must say - the contract the goldens freeze.
func TestDeviceStatesContent(t *testing.T) {
	for state, want := range map[string][]string{
		"none":     {"○ Pixel_7_API_34 off", "D E V I C E", "Pixel_7_API_34 is not running", "Start Pixel_7_API_34", "Start a different emulator", "Use a connected phone"},
		"starting": {"◐ Pixel_7_API_34 starting", "starting… 96%", "Waiting for the device to finish booting", "Cancel start"},
		"free":     {"● Pixel_7_API_34 free", "no agent is using it", "running · free", "API 34 · Google Play", "The next agent that reaches verify will use it.", "Show emulator window", "Restart", "Switch to a different device", "Stop"},
		"agent": {"● Pixel_7_API_34 · PLEX-64818", "in use by agent", "branch " + holderBranch, "A G E N T", "PR 14: task-form-ui",
			"worktrees/PLEX-64818", "verify · attempt 1", "N E X T   I N   L I N E", "PLEX-64905 · Empty state for notes tab",
			"Open ticket PLEX-64818", "Take it back from the agent", "verifying on device", "waiting for device"},
		"you": {"● Pixel_7_API_34 · you", "reserved by you", "Release to agents"},
		// You hold it for a reason: the row and pane say which ticket and branch.
		"runapp": {"● Pixel_7_API_34 · Run app PLEX-64818", "Run app · PLEX-64818 · PR 14: task-form-ui", "running app",
			"branch " + holderBranch, "R U N   A P P", "worktrees/PLEX-64818", "stagingDebug", "Run app is using it"},
		"takenback": {"● Pixel_7_API_34 · you", "taken back from PLEX-64818", "branch " + holderBranch,
			"T A K E N   B A C K", "Resume PLEX-64818 to put it back in the queue"},
		"usb": {"● Pixel 7 (USB) free", "connected · no agent is using it", "android", "2A281FDH3001", "Stop using this phone"},
	} {
		out := stripAnsi(deviceModel(state).View())
		for _, w := range want {
			if !strings.Contains(out, w) {
				t.Errorf("%s: missing %q\n%s", state, w, out)
			}
		}
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func send(m monitorModel, keys ...string) (monitorModel, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var mm tea.Model
		mm, cmd = m.Update(key(k))
		m = mm.(monitorModel)
	}
	return m, cmd
}

// Enter on the DEVICE row focuses ACTIONS; Esc gives the keyboard back
// instead of quitting the hub.
func TestDeviceFocusRoundTrip(t *testing.T) {
	m, _ := send(deviceModel("free"), "enter")
	if !m.device.focus {
		t.Fatal("enter on the DEVICE row should focus ACTIONS")
	}
	m, _ = send(m, "down", "down")
	if m.device.actionSel != 2 {
		t.Errorf("actionSel = %d, want 2", m.device.actionSel)
	}
	m, cmd := send(m, "esc")
	if m.device.focus {
		t.Error("esc should unfocus ACTIONS")
	}
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("esc in ACTIONS quit the hub")
		}
	}
}

// Take it back and Stop-while-held both confirm, with Cancel first: a stray
// Enter never stops an agent.
func TestDestructiveActionsConfirmWithCancelFirst(t *testing.T) {
	m := deviceModel("agent")
	m, _ = send(m, "enter", "down", "down") // Take it back
	m, _ = send(m, "enter")
	if m.device.confirm == nil || m.device.confirm.kind != confirmTakeBack || m.device.confirm.opts[0] != "Cancel" {
		t.Fatalf("take back should confirm with Cancel first, got %+v", m.device.confirm)
	}
	m, cmd := send(m, "enter") // Cancel
	if m.device.confirm != nil || cmd != nil {
		t.Error("Cancel must close the dialog and do nothing")
	}

	m, _ = send(deviceModel("agent"), "enter", "down", "down", "down", "enter") // Stop
	if m.device.confirm == nil || m.device.confirm.kind != confirmStop {
		t.Fatalf("stop on a held device should confirm, got %+v", m.device.confirm)
	}
}

// Switching while an agent holds the device is blocked: take it back first.
func TestSwitchWhileHeldIsBlocked(t *testing.T) {
	m := deviceModel("agent")
	m, _ = m.runDeviceActionModel(daSwitch)
	if m.device.picker != nil || !strings.Contains(m.notice, "take the device back from PLEX-64818 first") {
		t.Errorf("picker=%v notice=%q", m.device.picker, m.notice)
	}
}

// The picker lists the other AVDs and phones; an unauthorized phone is shown
// but can't be picked.
func TestPickerListsAVDsAndPhones(t *testing.T) {
	m := deviceModel("free")
	p := m.newPicker(pickSwitch, false)
	var labels []string
	for _, it := range p.items {
		labels = append(labels, it.label)
	}
	got := strings.Join(labels, ",")
	if got != "Pixel_Tablet_API_34,Pixel 7 (USB),R58N12ABCD (USB)" {
		t.Errorf("picker items = %s", got)
	}
	if !p.items[2].disabled {
		t.Error("an unauthorized phone must not be pickable")
	}
}

// Switching away from an emulator pie launched asks whether to stop it.
func TestSwitchAwayFromLaunchedEmulatorAsks(t *testing.T) {
	m := deviceModel("free")
	m.device.scan.dev.PID = 4242
	m.device.picker = m.newPicker(pickSwitch, false)
	m, _ = send(m, "enter") // Pixel_Tablet_API_34
	if m.device.confirm == nil || m.device.confirm.kind != confirmSwitch || len(m.device.confirm.opts) != 3 {
		t.Fatalf("switch should ask stop/keep/cancel, got %+v", m.device.confirm)
	}
}

// runDeviceActionModel unwraps runDeviceAction for tests.
func (m monitorModel) runDeviceActionModel(id string) (monitorModel, tea.Cmd) {
	mm, cmd := m.runDeviceAction(id)
	return mm.(monitorModel), cmd
}

// Take it back, against a real store: the hold moves to you BEFORE the ticket
// is parked and its run stopped, so no waiter can slip in between.
func TestTakeBackMovesTheHoldThenParks(t *testing.T) {
	m, st := approvalModel(t) // PLEX-1: an active session driven by this process
	_ = st.UseEmulator("Pixel_7", "emulator-5554")
	_ = st.SetDeviceWait("PLEX-1")
	if ok, _ := st.AcquireDevice("PLEX-1", func(int) bool { return true }); !ok {
		t.Fatal("setup")
	}
	var holderWhenStopped string
	old := stopTicketRun
	stopTicketRun = func(s store.Session) {
		d, _ := st.ActiveDevice()
		holderWhenStopped = d.Holder
	}
	defer func() { stopTicketRun = old }()

	m.reload()
	m.device.scan.dev, _ = st.ActiveDevice()
	msg := m.takeBackCmd()()
	if op, ok := msg.(deviceOpMsg); !ok || op.err != nil {
		t.Fatalf("take back = %#v", msg)
	}
	if holderWhenStopped != store.HolderYou {
		t.Errorf("holder when the run was stopped = %q, want you (moved first)", holderWhenStopped)
	}
	if s, _ := st.Get("PLEX-1"); s.State != store.StateNeedsYou {
		t.Errorf("ticket state = %q, want needs-you", s.State)
	}
	if d, _ := st.ActiveDevice(); d.Holder != store.HolderYou {
		t.Errorf("holder = %q, want you", d.Holder)
	}
}

// Ops never shell out on the update loop: Start returns a Cmd, and the
// seam is only called when that Cmd runs.
func TestStartGoesThroughTheSeam(t *testing.T) {
	m, st := approvalModel(t)
	_ = st.UseEmulator("Pixel_7", "")
	called := ""
	old := startEmulator
	startEmulator = func(p emulator.SDKPaths, avd string, o emulator.StartOpts) (int, <-chan error, error) {
		called = avd
		ch := make(chan error, 1)
		ch <- nil
		return 4242, ch, nil
	}
	defer func() { startEmulator = old }()
	m.device.scan.dev, _ = st.ActiveDevice()
	m.device.scanned = true
	mm, cmd := m.runDeviceAction(daStart)
	if called != "" {
		t.Fatal("the emulator started on the update loop")
	}
	if cmd == nil || mm.(monitorModel).device.busy == "" {
		t.Fatal("start should return a Cmd and show progress")
	}
	cmd()
	if called != "Pixel_7" {
		t.Errorf("seam called with %q", called)
	}
	if d, _ := st.ActiveDevice(); d.State != store.DeviceStarting || d.PID != 4242 {
		t.Errorf("device = %+v, want starting with the pid", d)
	}
}
