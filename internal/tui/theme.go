// The Apple Pie design system: layout tokens, the palette, and every style the
// review screens draw with.
//
// Two rules hold the rest together. The content fills the terminal, inset by a
// margin on both sides - a wide terminal is width the reviewer already paid
// for, and holding the UI to a fixed column with dead space beside it reads as
// a window the app never noticed. And a terminal has no font sizes, so the only
// scale available is weight × colour × case: exactly one thing on the screen is
// bold, exactly one is filled, and everything else separates by colour alone.
//
// Every style here is built once, at package scope. None is constructed inside
// a View.
package tui

import "github.com/charmbracelet/lipgloss"

// Layout tokens.
const (
	// Margin insets the UI from the terminal edge, on both sides. Content flush
	// against column 0 is the strongest "nobody designed this" signal there is,
	// and content that fills the terminal needs a right edge as much as a left.
	Margin = 6
	// GutterX is the horizontal padding inside a filled row, so a band's text
	// never touches its own edge. It doubles as the narrow terminal's margin.
	GutterX = 2
	// The widths the margin steps down at. A wide terminal can spend 12 cells on
	// whitespace, a laptop-width one 6, then 4, then 2 - whitespace is always the
	// first thing to give.
	RoomyAt = 120
	MidAt   = 90
	TightAt = 76
	// MinTerm is the narrowest terminal worth drawing on. Below it the columns
	// are past their own minimums and the screen would be a lie; the hub prints
	// one honest line asking for a wider window instead.
	MinTerm = 52
)

// layout resolves the margin and content width for a terminal width.
//
// The content is the terminal, less the margin: there is no cap, so a 200-column
// window renders a 188-column screen. The margin is the only thing held back and
// the first thing given up, stepping 6 → 3 → 2 → 1 as the width runs out.
func layout(termW int) (margin, content int) {
	switch {
	case termW >= RoomyAt:
		margin = Margin
	case termW >= MidAt:
		margin = 3
	case termW >= TightAt:
		margin = 2
	default:
		margin = 1
	}
	content = termW - 2*margin
	if content < 1 {
		content = 1
	}
	return margin, content
}

// The palette. Adaptive so a light terminal is not left reading white on white,
// and expressed as hex so Lip Gloss can degrade to ANSI-16 itself rather than
// us hardcoding escapes.
var (
	// ink is body text - the reviewer's actual words.
	ink = lipgloss.AdaptiveColor{Dark: "#C9D1DC", Light: "#22262E"}
	// strong is the one emphasis: the selected row's body.
	strongC = lipgloss.AdaptiveColor{Dark: "#FFFFFF", Light: "#000000"}
	// dim is metadata that must stay readable: locations, authors, counts.
	dim = lipgloss.AdaptiveColor{Dark: "#7C8AA0", Light: "#5B6675"}
	// faint is chrome that must not compete: rules, gutters, key hints.
	faint = lipgloss.AdaptiveColor{Dark: "#5A6577", Light: "#8A93A3"}
	// accent means one thing only: the agent will act on this.
	accent = lipgloss.AdaptiveColor{Dark: "#3DDC84", Light: "#10893E"}
	amberC = lipgloss.AdaptiveColor{Dark: "#E5A24A", Light: "#9A6100"}
	redC   = lipgloss.AdaptiveColor{Dark: "#E86B7E", Light: "#B3261E"}
	cyanC  = lipgloss.AdaptiveColor{Dark: "#62B8E8", Light: "#00618A"}
	// sel fills the selected row and backs the menu. Nothing else paints a
	// background - the screen inherits the terminal's.
	sel = lipgloss.AdaptiveColor{Dark: "#39404F", Light: "#E3E7ED"}
	// The diff washes. A terminal has no alpha, so "10% accent" is a mixed
	// colour rather than a blend; the wash is what makes a diff read as a diff
	// instead of as coloured text.
	addBg = lipgloss.AdaptiveColor{Dark: "#12291D", Light: "#E4F6EA"}
	delBg = lipgloss.AdaptiveColor{Dark: "#2C1620", Light: "#FBE7EB"}
	// onAccent is text sitting on a filled accent background.
	onAccent = lipgloss.AdaptiveColor{Dark: "#0E1117", Light: "#FFFFFF"}
)

// Type roles. Nothing outside this block is bold: if two things are emphasised,
// neither is.
var (
	stBrand  = lipgloss.NewStyle().Bold(true).Foreground(accent)
	stTitle  = lipgloss.NewStyle().Foreground(ink)
	stFocus  = lipgloss.NewStyle().Bold(true).Foreground(strongC)
	stMeta   = lipgloss.NewStyle().Foreground(dim)
	stChrome = lipgloss.NewStyle().Foreground(faint)
	stAccent = lipgloss.NewStyle().Foreground(accent)
	stAmber  = lipgloss.NewStyle().Foreground(amberC)
	stCyan   = lipgloss.NewStyle().Foreground(cyanC)
	stRed    = lipgloss.NewStyle().Foreground(redC)
)

// Row styles. The selected row is a filled band the full content width, which
// is the only way a cursor is findable on a wide screen.
var (
	stRowSel  = lipgloss.NewStyle().Background(sel)
	stBodySel = lipgloss.NewStyle().Bold(true).Foreground(strongC).Background(sel)
)

// The run button: the only bordered element on the screen, and the only filled
// one. That is what makes it unmistakably the primary action.
var (
	stButton = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(faint).
			Foreground(accent).
			Padding(0, GutterX)
	stButtonFocus = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Background(accent).
			Foreground(onAccent).
			Bold(true).
			Padding(0, GutterX)
	stButtonOff = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(faint).
			Foreground(faint).
			Padding(0, GutterX)
)

// The diff: a rail, a right-aligned number, a sign column, then the source. The
// wash spans the full content width so added and removed lines read as bands.
var (
	stDiffRail = lipgloss.NewStyle().Foreground(faint)
	stDiffNum  = lipgloss.NewStyle().Foreground(faint)
	stDiffCtx  = lipgloss.NewStyle().Foreground(dim)
	stDiffAdd  = lipgloss.NewStyle().Foreground(accent).Background(addBg)
	stDiffDel  = lipgloss.NewStyle().Foreground(redC).Background(delBg)
	stDiffHunk = lipgloss.NewStyle().Foreground(faint)
)

// The menu: rounded accent border on the selection background, and the one
// inversion on the screen for its picked item.
var (
	stMenu = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		Background(sel).
		Padding(1, GutterX)
	stMenuItem   = lipgloss.NewStyle().Foreground(ink).Background(sel)
	stMenuQuiet  = lipgloss.NewStyle().Foreground(dim).Background(sel)
	stMenuHint   = lipgloss.NewStyle().Foreground(faint).Background(sel)
	stMenuOn     = lipgloss.NewStyle().Bold(true).Foreground(onAccent).Background(accent)
	stMenuOnHint = lipgloss.NewStyle().Foreground(onAccent).Background(accent)
	stMenuTitle  = lipgloss.NewStyle().Foreground(accent).Background(sel)
)

// letterSpace tracks a string out by a space per character. Lip Gloss has no
// tracking, and the brand needs it to read as a mark rather than as a word.
func letterSpace(s string) string {
	r := []rune(s)
	out := make([]rune, 0, len(r)*2)
	for i, c := range r {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, c)
	}
	return string(out)
}

// rule is a horizontal rule at the content width - never the terminal width.
func rule(w int) string {
	return stChrome.Render(repeat("─", w))
}

func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
