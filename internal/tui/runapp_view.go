// Drawing Run app: the code and variant pickers, the take-back confirm, and
// the progress screen (steps ✓ ● ○, the log tail, the result).
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Apple-Pie-AI/pie-tui/internal/runapp"
	"github.com/Apple-Pie-AI/pie-tui/internal/store"
)

// raSteps is the run's step order, as the progress screen lists them.
var raSteps = []struct{ id, label string }{
	{runapp.StepPrepare, "Prepare"},
	{runapp.StepDevice, "Device"},
	{runapp.StepSync, "Sync"},
	{runapp.StepBuild, "Build"},
	{runapp.StepReinstall, "Reinstall"},
	{runapp.StepLaunch, "Launch"},
}

func (m monitorModel) renderRunApp(w int) string {
	margin, cw := layout(w)
	ra := m.runApp
	var b strings.Builder
	crumb := "Run app"
	if ra.code.label != "" {
		crumb += " › " + ra.code.label
	}
	if ra.step == raProgress && ra.run.Variant != "" {
		crumb += " · " + ra.run.Variant
	}
	right := m.deviceHeaderStatus()
	b.WriteString(padBetween(stBrand.Render(letterSpace("APPLE PIE"))+"  "+stMeta.Render(crumb), right, cw) + "\n")
	b.WriteString(rule(cw) + "\n\n")

	row := func(i int, label, meta string, dim bool) {
		sel := i == ra.sel
		sty := stTitle
		if dim {
			sty = stChrome
		}
		mark := "  "
		if sel {
			mark, sty = stAccent.Render("▸ "), stFocus
		}
		labelW := max(cw/3, 24)
		line := mark + sty.Render(padRight(truncate(label, labelW-2), labelW)) + stChrome.Render(truncate(meta, cw-labelW-2))
		if sel {
			line = band(true, cw, cell(stFocus, true, cw, lipgloss.Left, line))
		}
		b.WriteString(line + "\n")
	}

	switch ra.step {
	case raPickCode:
		b.WriteString(stTitle.Render("Which code should run on the device?") + "\n\n")
		if len(ra.codes) == 0 {
			b.WriteString(stChrome.Render("no repo configured - Edit config first") + "\n")
		}
		for i, c := range ra.codes {
			row(i, c.label, c.meta, false)
		}
	case raPickVariant:
		b.WriteString(stTitle.Render("Build variant") + stChrome.Render("  (like Android Studio's Build Variants)") + "\n\n")
		for i, c := range ra.choices {
			if c.sync {
				label := "Sync project"
				if len(ra.choices) == 1 {
					label = "Sync project & run"
				}
				row(i, label, c.meta, false)
				continue
			}
			row(i, c.variant, c.meta, !isDebug(c.variant))
		}
		if ra.err != "" {
			b.WriteString("\n" + stRed.Render(truncate(ra.err, cw)) + "\n")
		}
	case raConfirmTakeBack:
		h := m.device.scan.dev.Holder
		b.WriteString(stTitle.Render(h+"'s agent is testing on the device") + "\n")
		for _, s := range wrapWords("Running the app now means taking the device back: its verify stops and the ticket goes to NEEDS YOU (Resume re-queues it).", cw) {
			b.WriteString(stChrome.Render(s) + "\n")
		}
		b.WriteString("\n")
		row(0, "Take it back and run", "", false)
		row(1, "Cancel", "", false)
	case raProgress:
		b.WriteString(m.renderRunAppProgress(cw, row))
	}
	return indent(b.String(), margin)
}

func (m monitorModel) renderRunAppProgress(cw int, row func(int, string, string, bool)) string {
	ra := m.runApp
	r := ra.run
	var b strings.Builder
	cur := -1
	for i, s := range raSteps {
		if s.id == r.Step {
			cur = i
		}
	}
	elapsed := time.Since(time.Unix(r.StartedAt, 0)).Truncate(time.Second)
	b.WriteString(stChrome.Render(fmt.Sprintf("%s · %s", r.Target, elapsed)) + "\n\n")
	for i, s := range raSteps {
		glyph, sty, detail := "○", stChrome, ""
		switch {
		case r.State == store.AppRunDone || i < cur:
			glyph, sty = "✓", stAccent
		case i == cur && r.State == store.AppRunRunning:
			glyph, sty, detail = "●", stAmber, r.Detail
		case i == cur:
			glyph, sty, detail = "✗", stRed, ""
		}
		if r.Step == runapp.StepRepair && s.id == runapp.StepBuild {
			glyph, sty, detail = "↻", stAmber, "Repair · "+r.Detail
		}
		b.WriteString(sty.Render(glyph+" "+padRight(s.label, 11)) + stChrome.Render(truncate(detail, cw-14)) + "\n")
	}
	b.WriteString("\n" + stChrome.Render(letterSpace("LOG")) + "\n")
	for _, ln := range tailLines(r.LogPath, 10) {
		b.WriteString(stChrome.Render(rail) + stMeta.Render(truncate(ln, cw-railW)) + "\n")
	}
	b.WriteString("\n")
	switch r.State {
	case store.AppRunRunning:
		row(0, "Stop", "", false)
		row(1, "Back", "keeps running - the DEVICE row shows it", false)
	case store.AppRunDone:
		msg := "✓ " + r.Detail
		if r.Repaired {
			msg += " (after one repair - learned for next time)"
		}
		b.WriteString(stAccent.Render(truncate(msg, cw)) + "\n\n")
		row(0, "▶ Run again", "", false)
		row(1, "Back", "", false)
	default:
		for _, s := range wrapWords(r.Detail, cw) {
			b.WriteString(stRed.Render(s) + "\n")
		}
		b.WriteString("\n")
		row(0, "▶ Run again", "", false)
		row(1, "Back", "", false)
	}
	return b.String()
}
