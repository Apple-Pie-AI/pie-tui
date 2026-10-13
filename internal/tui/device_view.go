// Drawing the DEVICE section: its one row in the list, its word in the
// header, and its detail pane - the four states of the mockups (no device,
// starting, running and free, running and used by an agent), plus a USB
// phone and "reserved by you".
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// deviceLine is the row's content for the current state.
type deviceLine struct {
	name, middle, phase, age, branch string
	phaseSty                         lipgloss.Style
}

func (m monitorModel) deviceLine() deviceLine {
	sc := m.device.scan
	d := sc.dev
	switch {
	case d.None():
		return deviceLine{name: "No device running", middle: "agents can't verify on a device", phase: "off", phaseSty: stChrome}
	case d.Kind == store.DeviceUSB && !attachedReady(sc.attached, d.Serial):
		return deviceLine{name: m.deviceName(), middle: "phone not connected", phase: "not connected", phaseSty: stRed}
	case d.State == store.DeviceOff:
		return deviceLine{name: d.AVD, middle: "not running · agents can't verify on it", phase: "off", phaseSty: stChrome}
	case d.State == store.DeviceStarting:
		return deviceLine{name: d.AVD, middle: m.startProgressText(), phase: "starting", phaseSty: stAmber}
	case d.Holder == store.HolderYou:
		return m.youLine()
	case d.Holder != "":
		l := deviceLine{name: m.deviceName(), middle: d.Holder, phase: "in use by agent", phaseSty: stAmber,
			age: Age(time.Unix(d.AcquiredAt, 0))}
		if s := m.holderSession(); s != nil {
			if s.Summary != "" {
				l.middle += " · " + s.Summary
			}
			l.branch = s.Branch
		}
		return l
	}
	return deviceLine{name: m.deviceName(), middle: "no agent is using it", phase: "running · free", phaseSty: stAccent,
		age: Age(time.Unix(d.RunningAt, 0))}
}

// deviceName is the AVD name, or the phone's label.
func (m monitorModel) deviceName() string {
	d := m.device.scan.dev
	if d.Kind == store.DeviceUSB {
		for _, a := range m.device.scan.attached {
			if a.Serial == d.Serial {
				return usbLabel(a)
			}
		}
		return d.Serial + " (USB)"
	}
	return d.AVD
}

// startProgress is the boot estimate: elapsed over the last boot of this AVD,
// capped at 99%. -1 when the AVD was never timed.
func (m monitorModel) startProgress() (pct int, elapsed time.Duration) {
	d := m.device.scan.dev
	elapsed = time.Since(time.Unix(d.StartedAt, 0)).Truncate(time.Second)
	if d.StartedAt == 0 {
		elapsed = 0
	}
	if m.device.scan.bootSecs <= 0 {
		return -1, elapsed
	}
	pct = int(elapsed.Seconds() * 100 / float64(m.device.scan.bootSecs))
	return min(pct, 99), elapsed
}

func (m monitorModel) startProgressText() string {
	pct, el := m.startProgress()
	if pct < 0 {
		return fmt.Sprintf("starting… %d:%02d", int(el.Minutes()), int(el.Seconds())%60)
	}
	return fmt.Sprintf("starting… %d%%", pct)
}

// renderDeviceSection is the section header and its one row.
func (m monitorModel) renderDeviceSection(cw int, cursor bool) string {
	r := dashCols(cw)
	l := m.deviceLine()
	nameSty, midSty := stTitle, stMeta
	if cursor {
		nameSty = stFocus
	}
	mark := " "
	if cursor {
		mark = "▸"
	}
	cells := []string{
		cell(stAccent, cursor, dashCursorW, lipgloss.Left, mark),
		cell(nameSty, cursor, r.id+dashGapW, lipgloss.Left, truncate(l.name, r.id)),
		cell(midSty, cursor, r.title, lipgloss.Left, truncate(l.middle, r.title)),
	}
	if r.phase {
		cells = append(cells, cell(l.phaseSty, cursor, dashPhaseW, lipgloss.Left, truncate(l.phase, dashPhaseW)))
	}
	if r.age {
		cells = append(cells, cell(stChrome, cursor, dashAgeW, lipgloss.Right, l.age))
	}
	out := "\n" + stCyan.Bold(true).Render(letterSpace("DEVICE")) + "\n" + band(cursor, cw, cells...)
	if l.branch != "" {
		line := strings.Repeat(" ", dashCursorW) + stChrome.Render("branch ") + stTitle.Render(l.branch)
		out += "\n" + band(cursor, cw, cell(stTitle, cursor, cw, lipgloss.Left, truncate(line, cw)))
	}
	return out + "\n"
}

// deviceHeaderStatus is the device's word in the dashboard header.
func (m monitorModel) deviceHeaderStatus() string {
	d := m.device.scan.dev
	switch {
	case !m.device.scanned:
		return ""
	case d.None():
		return stChrome.Render("○ no device")
	case d.Kind == store.DeviceUSB && !attachedReady(m.device.scan.attached, d.Serial):
		return stRed.Render("○ " + m.deviceName() + " not connected")
	case d.State == store.DeviceOff:
		return stChrome.Render("○ " + d.AVD + " off")
	case d.State == store.DeviceStarting:
		return stAmber.Render("◐ " + d.AVD + " starting")
	case d.Holder == store.HolderYou && d.RunAppTarget() != "":
		return stCyan.Render("● " + m.deviceName() + " · Run app " + targetLabel(d.RunAppTarget()))
	case d.Holder == store.HolderYou:
		return stCyan.Render("● " + m.deviceName() + " · you")
	case d.Holder != "":
		return stAmber.Render("● " + m.deviceName() + " · " + d.Holder)
	}
	return stAccent.Render("● " + m.deviceName() + " free")
}

// renderDeviceDetail is the right-hand pane while the DEVICE row is selected.
func (m monitorModel) renderDeviceDetail(cw int) string {
	if p := m.device.picker; p != nil {
		return m.renderDevicePicker(p, cw)
	}
	if c := m.device.confirm; c != nil {
		return renderDeviceConfirm(c, cw)
	}
	sc := m.device.scan
	d := sc.dev
	var b strings.Builder
	line := func(s string) { b.WriteString(truncate(s, cw) + "\n") }
	kv := func(k, v string) { line(stChrome.Render(padRight(k, 10)) + stTitle.Render(v)) }
	section := func(s string, sty lipgloss.Style) { b.WriteString("\n" + sty.Render(letterSpace(s)) + "\n") }

	if !m.device.scanned {
		line(stChrome.Render("looking for devices…"))
		return b.String()
	}
	if sc.err != nil {
		line(stTitle.Render("adb not found"))
		line(stChrome.Render("Set android_sdk_path in Edit config, or install the Android SDK platform-tools."))
		return b.String()
	}
	switch {
	case d.None() || (d.Kind == store.DeviceEmulator && d.State == store.DeviceOff):
		head := "No device running"
		if d.AVD != "" {
			head = d.AVD + " is not running"
		}
		line(stTitle.Render(head))
		for _, s := range wrapWords("Start one so agents can install and check the app on a device. "+
			"One device at a time, one agent at a time.", cw) {
			line(stChrome.Render(s))
		}
	case d.State == store.DeviceStarting:
		line(stTitle.Render(d.AVD) + stChrome.Render(serialSuffix(d.Serial)))
		line(stAmber.Render("starting"))
		line(progressBar(m, cw))
		line(stChrome.Render("Waiting for the device to finish booting. Its window is open."))
	default:
		line(stTitle.Render(m.deviceName()) + stChrome.Render(serialSuffix(d.Serial)))
		l := m.deviceLine()
		status := "running · no agent is using it"
		switch {
		case d.Kind == store.DeviceUSB && !attachedReady(sc.attached, d.Serial):
			status = "not connected - plug it in and accept USB debugging"
		case d.Holder == store.HolderYou && d.RunAppTarget() != "":
			status = "running · Run app is using it"
		case d.Holder == store.HolderYou:
			status = "running · reserved by you"
		case d.Holder != "":
			status = "running · in use by an agent"
		case d.Kind == store.DeviceUSB:
			status = "connected · no agent is using it"
		}
		line(l.phaseSty.Render(status))
		b.WriteString("\n")
		if d.Kind == store.DeviceUSB {
			if sc.android != "" {
				kv("android", sc.android)
			}
			kv("serial", d.Serial)
			kv("connected", Age(time.Unix(d.RunningAt, 0)))
		} else {
			if sc.info.Device != "" {
				kv("device", sc.info.Device)
			}
			kv("image", avdMeta(sc.info))
			kv("up", Age(time.Unix(d.RunningAt, 0)))
		}
		if d.Holder == store.HolderYou {
			m.renderYouHold(&b, cw, kv, section)
		}
		if s := m.holderSession(); s != nil {
			section("AGENT", stAmber.Bold(true))
			ticket := s.Ticket
			if s.Summary != "" {
				ticket += " · " + s.Summary
			}
			kv("ticket", ticket)
			if s.Branch != "" {
				for i, seg := range wrapWords(s.Branch, cw-10) {
					if i == 0 {
						kv("branch", seg)
					} else {
						line(strings.Repeat(" ", 10) + stTitle.Render(seg))
					}
				}
			}
			wt := s.Worktree
			if wt == "" {
				wt = paths.WorktreeFor(s.Repo, s.Ticket)
			}
			kv("worktree", tildify(wt))
			kv("stage", fmt.Sprintf("verify · attempt %d", s.Retries+1))
			kv("using it", Age(time.Unix(d.AcquiredAt, 0)))
		} else if d.Holder == "" && d.State == store.DeviceRunning {
			b.WriteString("\n")
			line(stChrome.Render("The next agent that reaches verify will use it."))
		}
		if next := m.nextInLine(); next != "" {
			section("NEXT IN LINE", stChrome)
			line(stTitle.Render(next))
		}
	}
	if m.device.busy != "" {
		b.WriteString("\n")
		line(stAmber.Render(m.device.busy))
	}
	section("ACTIONS", stChrome)
	for i, a := range m.deviceActions() {
		sty := stTitle
		if a.danger {
			sty = stRed
		}
		focused := m.device.focus && i == m.device.actionSel
		mark := "  "
		if focused {
			mark = stAccent.Render("▸ ")
			sty = sty.Bold(true)
		}
		row := mark + sty.Render(truncate(a.label, cw-2))
		if focused {
			row = band(true, cw, cell(stFocus, true, cw, lipgloss.Left, row))
		}
		b.WriteString(row + "\n")
	}
	if !m.device.focus {
		b.WriteString("\n" + stChrome.Render("press enter to choose an action") + "\n")
	}
	return b.String()
}

// nextInLine is the first queued ticket that isn't the holder.
func (m monitorModel) nextInLine() string {
	for _, w := range m.device.scan.waiters {
		if w.Ticket == m.device.scan.dev.Holder {
			continue
		}
		label := w.Ticket
		for _, s := range m.flat {
			if s.Ticket == w.Ticket && s.Summary != "" {
				label += " · " + s.Summary
			}
		}
		return label
	}
	return ""
}

func serialSuffix(serial string) string {
	if serial == "" {
		return ""
	}
	return " · " + serial
}

// progressBar draws the boot estimate across the pane.
func progressBar(m monitorModel, cw int) string {
	pct, el := m.startProgress()
	w := max(cw-2, 10)
	if pct < 0 {
		return stChrome.Render(fmt.Sprintf("first boot of this device - %d:%02d elapsed", int(el.Minutes()), int(el.Seconds())%60))
	}
	fill := w * pct / 100
	return stAmber.Render(strings.Repeat("█", fill)) + stChrome.Render(strings.Repeat("░", w-fill))
}

func (m monitorModel) renderDevicePicker(p *devicePicker, cw int) string {
	var b strings.Builder
	b.WriteString(stTitle.Render(truncate("Choose the device Apple Pie will use", cw)) + "\n")
	b.WriteString(stChrome.Render(truncate("one device at a time, one agent at a time", cw)) + "\n\n")
	if len(p.items) == 0 {
		b.WriteString(stChrome.Render(truncate("no other AVDs or phones found", cw)) + "\n")
	}
	rows := append(append([]pickItem(nil), p.items...), pickItem{label: "Cancel"})
	for i, it := range rows {
		focused := i == p.sel
		sty := stTitle
		if it.disabled {
			sty = stChrome
		}
		mark := "  "
		if focused {
			mark = stAccent.Render("▸ ")
		}
		text := mark + sty.Render(padRight(it.label, 22)) + stChrome.Render(it.meta)
		if focused {
			text = band(true, cw, cell(stFocus, true, cw, lipgloss.Left, truncate(text, cw)))
		} else {
			text = truncate(text, cw)
		}
		b.WriteString(text + "\n")
	}
	return b.String()
}

func renderDeviceConfirm(c *deviceConfirm, cw int) string {
	var b strings.Builder
	b.WriteString(stTitle.Render(truncate(c.title, cw)) + "\n")
	for _, s := range wrapWords(c.body, cw) {
		b.WriteString(stChrome.Render(s) + "\n")
	}
	b.WriteString("\n")
	for i, o := range c.opts {
		if i == c.sel {
			b.WriteString(band(true, cw, cell(stFocus, true, cw, lipgloss.Left, truncate(stAccent.Render("▸ ")+o, cw))) + "\n")
		} else {
			b.WriteString("  " + truncate(o, cw-2) + "\n")
		}
	}
	return b.String()
}

// deviceSectionLines is how many lines the DEVICE section takes in the list.
func (m monitorModel) deviceSectionLines() int {
	if m.deviceLine().branch != "" {
		return 4
	}
	return 3
}

// targetLabel names a Run app target: a ticket id, or the main checkout.
func targetLabel(t string) string {
	if t == "main" {
		return "main checkout"
	}
	return t
}

// sessionFor finds a ticket's session among the dashboard's rows.
func (m monitorModel) sessionFor(ticket string) *store.Session {
	for i := range m.flat {
		if m.flat[i].Ticket == ticket {
			s := m.flat[i]
			return &s
		}
	}
	return nil
}

// youLine is the DEVICE row while you hold it: what for (Run app on which
// ticket, or taken back from which agent), with that ticket's branch on the
// row's second line - never a bare "reserved by you" when there is more to say.
func (m monitorModel) youLine() deviceLine {
	d := m.device.scan.dev
	l := deviceLine{name: m.deviceName(), middle: "reserved by you", phase: "in use by you", phaseSty: stCyan,
		age: Age(time.Unix(d.AcquiredAt, 0))}
	ticket := ""
	switch {
	case d.RunAppTarget() != "":
		t := d.RunAppTarget()
		l.middle, l.phase = "Run app · "+targetLabel(t), "running app"
		if t == "main" {
			l.branch = m.device.scan.mainBranch
		} else {
			ticket = t
		}
	case d.TakenBackFrom() != "":
		ticket = d.TakenBackFrom()
		l.middle = "reserved by you · taken back from " + ticket
	}
	if s := m.sessionFor(ticket); s != nil {
		if s.Summary != "" && d.RunAppTarget() != "" {
			l.middle += " · " + s.Summary
		}
		l.branch = s.Branch
	}
	return l
}

// renderYouHold is the detail pane's block for a device you hold: the ticket
// (or checkout), branch, worktree and - for Run app - the variant.
func (m monitorModel) renderYouHold(b *strings.Builder, cw int, kv func(k, v string), section func(string, lipgloss.Style)) {
	d := m.device.scan.dev
	t, title := d.RunAppTarget(), "RUN APP"
	if t == "" {
		if t = d.TakenBackFrom(); t == "" {
			return
		}
		title = "TAKEN BACK"
	}
	section(title, stCyan.Bold(true))
	if t == "main" {
		kv("code", "main checkout")
		if br := m.device.scan.mainBranch; br != "" {
			kv("branch", br)
		}
		if r := m.device.scan.mainRepo; r != "" {
			kv("checkout", tildify(r))
		}
	} else {
		label := t
		s := m.sessionFor(t)
		if s != nil && s.Summary != "" {
			label += " · " + s.Summary
		}
		kv("ticket", label)
		if s != nil {
			if s.Branch != "" {
				for i, seg := range wrapWords(s.Branch, cw-10) {
					if i == 0 {
						kv("branch", seg)
					} else {
						b.WriteString(strings.Repeat(" ", 10) + stTitle.Render(seg) + "\n")
					}
				}
			}
			wt := s.Worktree
			if wt == "" {
				wt = paths.WorktreeFor(s.Repo, s.Ticket)
			}
			kv("worktree", tildify(wt))
		}
	}
	if ar := m.device.scan.appRun; d.RunAppTarget() != "" && ar.Target == t && ar.Variant != "" {
		kv("variant", ar.Variant)
	}
	if d.TakenBackFrom() != "" {
		b.WriteString(stChrome.Render(truncate("Resume "+t+" to put it back in the queue.", cw)) + "\n")
	}
}
