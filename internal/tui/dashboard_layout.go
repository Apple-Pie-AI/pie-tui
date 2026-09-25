// How the dashboard row divides the content width.
//
// The title is the only column that flexes. Everything else is a fixed width or
// is dropped outright, which is what keeps the ids, the phase words and the ages
// in straight lines down the screen at every size - a column that shrinks by a
// cell per row is worse than one that is not drawn at all.
package tui

// The dashboard row's fixed columns, in display cells: the cursor and its gap,
// the phase word, the age, and the gap that keeps the title off the phase.
const (
	dashCursorW = 2
	dashPhaseW  = 11
	dashAgeW    = 6
	dashGapW    = 1
)

// The content widths each optional column survives down to. Order is the whole
// point: the phase word goes first because the section header above the row
// already states it, then the age, which is the least actionable thing on the
// row - an id you cannot read is a row you cannot act on, so it goes last.
const (
	phaseFloor = 96
	ageFloor   = 72
)

// dashRow is the solved geometry of one dashboard row.
type dashRow struct {
	id, title  int
	phase, age bool
}

// dashCols solves the row for a content width.
func dashCols(content int) dashRow {
	r := dashRow{phase: content >= phaseFloor, age: content >= ageFloor}

	// The id has a range rather than a value. It is truncated from the front, so
	// the meaningful tail of PIE-1-ADD-HELLO-WORLD survives the narrow sizes.
	switch {
	case content >= 110:
		r.id = 26
	case content >= 90:
		r.id = 22
	case content >= 76:
		r.id = 18
	default:
		r.id = 14
	}

	fixed := dashCursorW + r.id + dashGapW
	if r.phase {
		fixed += dashPhaseW
	}
	if r.age {
		fixed += dashAgeW
	}
	if r.title = content - fixed; r.title < 12 {
		r.title = 12
	}
	return r
}

// tooNarrow is whether the terminal is below the width the hub will draw on.
func tooNarrow(termW int) bool { return termW > 0 && termW < MinTerm }

// narrowNotice is the one line drawn instead of a broken screen.
func narrowNotice(termW int) string {
	return stAmber.Render(truncate(
		"Apple Pie needs at least 52 columns. Widen the window.", max(0, termW-2)))
}
