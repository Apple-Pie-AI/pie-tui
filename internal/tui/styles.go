// One place for every color and every style, so screens compose from named roles
// instead of scattering raw 256-color codes. Add to this; don't invent new
// literals at the call site.
package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color palette - one place for every color so styles compose from named roles
// instead of scattering raw 256-color codes. Add to this, don't invent new
// literals at the call site.
var (
	colorPrimary  = lipgloss.Color("36")  // teal: CTAs, primary buttons, editable values
	colorText     = lipgloss.Color("231") // bright white text
	colorSubtle   = lipgloss.Color("250") // light gray (field labels)
	colorMuted    = lipgloss.Color("245") // dim gray (hints, secondary text)
	colorSep      = lipgloss.Color("240") // separators
	colorSelBg    = lipgloss.Color("236") // selection background
	colorMutedBg  = lipgloss.Color("238") // muted button background
	colorNeedsYou = lipgloss.Color("203") // red    - NEEDS YOU group / errors
	colorRunning  = lipgloss.Color("39")  // cyan   - RUNNING group
	colorReview   = lipgloss.Color("42")  // green  - READY FOR REVIEW group
	colorTracking = lipgloss.Color("141") // violet - TRACKING group (hand checkouts)
	colorAttn     = lipgloss.Color("214") // orange - STOPPED group / warnings / hints
	colorClosed   = lipgloss.Color("135") // purple - CLOSED group (GitHub's merged color)
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	selStyle    = lipgloss.NewStyle().Foreground(colorText).Background(colorSelBg)
	// hintStyle accents the "↵ actions" affordance on the selected row. Same
	// background as selStyle so the row highlight stays continuous under it.
	hintStyle = lipgloss.NewStyle().Foreground(colorAttn).Background(colorSelBg).Bold(true)
	sepStyle  = lipgloss.NewStyle().Foreground(colorSep)
	whyStyle  = lipgloss.NewStyle().Foreground(colorAttn).Bold(true)

	// The command awaiting approval, verbatim: the one thing on that overlay
	// the human is actually judging, so it is the loudest thing on it.
	cmdApprovalStyle = lipgloss.NewStyle().Foreground(colorAttn)

	// The code window under a review comment: a dim line-number gutter, and the
	// commented line called out in amber so the eye lands on it without reading
	// numbers.
	codeNumStyle    = lipgloss.NewStyle().Foreground(cmtGutter)
	codeNumHitStyle = lipgloss.NewStyle().Foreground(cmtAmber).Bold(true)
	codeMarkStyle   = lipgloss.NewStyle().Foreground(cmtAmber).Bold(true)

	// The review-comment list. A skipped comment dims its text and darkens its
	// metadata (no strikethrough - the mock reads state from brightness alone),
	// so the list states what will and will not be touched without any row
	// having to be opened.
	cmtOffStyle = lipgloss.NewStyle().Foreground(cmtDim)
	cmtLocStyle = lipgloss.NewStyle().Foreground(cmtMid)

	// The run bar. Green because it is the good outcome, and it carries the
	// live count - that binding is what makes the screen safe without a
	// confirmation dialog, since the thing you press says what it will do.
	// Selected it becomes the row band with a white label, like every other
	// selected row, rather than inverting to a green block.
	runBarSelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Background(cmtRowBg).Bold(true)
	runBarStyle    = lipgloss.NewStyle().Foreground(cmtGo).Bold(true)
	ctaStyle       = lipgloss.NewStyle().Foreground(colorText).Background(colorPrimary).Bold(true).Padding(0, 1)
	ctaSelStyle    = lipgloss.NewStyle().Foreground(colorPrimary).Background(colorText).Bold(true).Padding(0, 1)

	// Plan-viewer menu buttons: the selected one is a primary-teal button, the
	// rest are clearly muted, so the selection is obvious at a glance.
	planBtnSel = lipgloss.NewStyle().Foreground(colorText).Background(colorPrimary).Bold(true).Padding(0, 2)
	planBtn    = lipgloss.NewStyle().Foreground(colorSubtle).Background(colorMutedBg).Padding(0, 2)
)

// Log-pane line roles. The detail pane tails the ticket log, which used to be
// an undifferentiated wall of white; these give each kind of line a visual
// weight so errors, fixes and chatter separate at a glance.
var (
	logErrStyle  = lipgloss.NewStyle().Foreground(colorNeedsYou)           // ✗ denials, tool errors
	logWarnStyle = lipgloss.NewStyle().Foreground(colorAttn)               // (warn) lines
	logOKStyle   = lipgloss.NewStyle().Foreground(colorReview)             // ✓ / seeded lines
	logFixStyle  = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true) // the boxed fix line
	logHeadStyle = lipgloss.NewStyle().Foreground(colorAttn).Bold(true)    // 🤖 message headers
	logStepStyle = lipgloss.NewStyle().Foreground(colorText).Bold(true)    // "Fix:" / "Manual alternative:" lines
	logToolStyle = lipgloss.NewStyle().Foreground(colorMuted)              // ⚙ / • agent chatter
)

// styleLogLine assigns a style to one already-wrapped log line by its content.
// Called after wrapping, never before - ANSI escapes would corrupt the width
// math. First match wins; anything unrecognized stays plain.
func styleLogLine(seg string) string {
	t := strings.TrimLeft(seg, " \t")
	hasPrefix := func(prefixes ...string) bool {
		for _, p := range prefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
		return false
	}
	switch {
	case hasPrefix("🤖"):
		return logHeadStyle.Render(seg)
	case hasPrefix("✗"), strings.Contains(seg, "✗ permission denied"), strings.Contains(seg, "✗ tool error"):
		return logErrStyle.Render(seg)
	case hasPrefix("┌", "│", "└"):
		return logFixStyle.Render(seg)
	case hasPrefix("Fix:", "Manual alternative:", "Not fixed by this:"):
		return logStepStyle.Render(seg)
	case strings.Contains(seg, "(warn)"):
		return logWarnStyle.Render(seg)
	case hasPrefix("✓"), strings.Contains(seg, "✓"), strings.Contains(seg, "] seeded "):
		return logOKStyle.Render(seg)
	case hasPrefix("⚙", "•"):
		return logToolStyle.Render(seg)
	}
	return seg
}

var (
	formLabelStyle = lipgloss.NewStyle().Foreground(colorSubtle)
	formValStyle   = lipgloss.NewStyle().Foreground(colorPrimary)
	formLabelFocus = lipgloss.NewStyle().Foreground(colorText).Bold(true)
	formValFocus   = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	formMarkFocus  = lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	okStyle        = lipgloss.NewStyle().Foreground(colorReview) // green - a Pie-ready/"good" flag
)

// The review-comment screens' palette, from the 2026 mock. Applied as STYLE
// only - the layout, copy and keys are exactly the pre-existing ones. Hex
// rather than 256-color because each hue carries one meaning: green is "the
// agent will act / acted", amber is the reviewer's voice and the commented
// line, cyan flags a note, dim is skipped or chrome. The green is Android's
// own #3DDC84, so it belongs to the domain rather than to whatever the user's
// terminal theme decided colour 42 should be.
var (
	cmtGo     = lipgloss.Color("#3DDC84") // will fix / done / run bar
	cmtAmber  = lipgloss.Color("#FFB454") // the reviewer's voice, the hit line
	cmtCyan   = lipgloss.Color("#58C4F0") // note flag / in progress
	cmtRed    = lipgloss.Color("#FF6B81") // failed
	cmtInk    = lipgloss.Color("#D5DAE5") // body text
	cmtMid    = lipgloss.Color("#8B96AC") // secondary text
	cmtDim    = lipgloss.Color("#6B7488") // chrome, locations, authors, keys, rules
	cmtOffIsh = lipgloss.Color("#454D61") // unchecked rows - darker than dim
	cmtRowBg  = lipgloss.Color("#2A3242") // the selected row's band, and nothing else on the screen
	cmtGutter = lipgloss.Color("#3B4356") // code line numbers
	cmtCab    = lipgloss.Color("#0E1117") // terminal bg, for text ON green
)

var (
	cmtBrandSty = lipgloss.NewStyle().Foreground(cmtGo).Bold(true)
	cmtInkSty   = lipgloss.NewStyle().Foreground(cmtInk)
	cmtMidSty   = lipgloss.NewStyle().Foreground(cmtMid)
	cmtDimSty   = lipgloss.NewStyle().Foreground(cmtDim)
	cmtOffSty   = lipgloss.NewStyle().Foreground(cmtOffIsh)
	cmtGoSty    = lipgloss.NewStyle().Foreground(cmtGo)
	cmtCyanSty  = lipgloss.NewStyle().Foreground(cmtCyan)
	cmtRedSty   = lipgloss.NewStyle().Foreground(cmtRed)
	cmtAmberSty = lipgloss.NewStyle().Foreground(cmtAmber)

	// The selected row: a green spine down the left edge and a raised band -
	// the terminal rendering of the mock's inset box-shadow. The spine is a
	// PAINTED cell (green background on a space), not the ▌ glyph: a glyph
	// stops at the font's height, so stacked rows showed a gap between the row
	// and its rail, while a background tiles the full cell and the two lines
	// join into one bar.
	// The selection spine is a box-drawing glyph, not a painted cell, so the
	// green edge is a thin line with the row band showing through the rest of
	// its cell. Box-drawing (not a block element) because terminals stretch
	// those across line spacing - the row's and rail's spines join up.
	cmtSpineSty = lipgloss.NewStyle().Foreground(cmtGo).Background(cmtRowBg)
	cmtBandSty  = lipgloss.NewStyle().Background(cmtRowBg)

	// The menu's chosen item: dark text on green, the one inversion on the
	// screen, so "this is what enter will do" needs no legend.
	cmtActOnSty = lipgloss.NewStyle().Foreground(cmtCab).Background(cmtGo).Bold(true)

	// The menu paints the same background as the selected row - the only two
	// things on the screen that paint one at all.
	cmtMenuBgSty = lipgloss.NewStyle().Background(cmtRowBg)
	cmtActOffSty = lipgloss.NewStyle().Foreground(cmtMid).Background(cmtRowBg)
)
