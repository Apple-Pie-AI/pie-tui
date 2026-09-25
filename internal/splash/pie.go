package splash

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// The pie is rasterized rather than hand-drawn as frames: it is an ellipse seen
// from directly above, cut into `sliceCount` wedges, and each wedge can be
// pushed outward along its own bisector. That "spread" parameter is the whole
// opening animation - at 0 it is a whole pie with six defined cuts, at 1 the
// six slices have slid apart far enough to see between them.
const (
	sliceCount = 6

	// cutGap is the half-width of a knife cut, in normalized radius units.
	cutGap = 0.05
	// cutMax caps how much of a wedge the cut may eat. Without it the cuts
	// converge at the center and hollow the middle of the pie out.
	cutMax = 0.22
)

// A pieSize is one rasterization of the pie. Because the pie is drawn from its
// geometry rather than from stored frames, a second size costs nothing but the
// numbers below.
//
// The grid has to be big enough to hold the slices once they have parted, so
// every radius obeys r*(1+spread) < its center, less half a cell - see the test
// that pins it. rx is about twice ry throughout, because terminal cells are
// roughly half as wide as they are tall and the ellipse has to read as a circle.
type pieSize struct {
	w, h   int     // grid size, in cells
	cx, cy float64 // center. On a cell, not a cell boundary, so the cut at 12
	rx, ry float64 // and 6 o'clock lands on a column and the grid mirrors clean.

	// spread is how far a wedge travels at spread=1, in normalized units. It is
	// per-size because travel and radius compete for the same grid: a smaller pie
	// buys back the room to stay round by parting its slices a little less far,
	// which costs nothing legible - the gap only has to read as a gap.
	spread float64
}

var (
	// bigPie is the pie as designed, for a terminal with room to enjoy it.
	bigPie = pieSize{w: 33, h: 17, cx: 16.5, cy: 8.5, rx: 11.0, ry: 5.5, spread: 0.42}

	// smallPie is the same pie for a default 80x24 terminal, where the full-size
	// wordmark, a pie and the prompt cannot all have what they want. The
	// lettering is the point of the screen, so the pie is what gives.
	//
	// It is as large as those 24 rows allow, and that is not fussiness: rasterize
	// six wedges much smaller than this and the tips nearest the center thin out
	// below one cell, so the slices shed loose specks instead of parting cleanly.
	smallPie = pieSize{w: 25, h: 13, cx: 12.5, cy: 6.5, rx: 8.4, ry: 4.2, spread: 0.30}
)

type cell struct {
	r     rune
	color string
}

var blank = cell{r: ' '}

// wedgeAngle is the angular width of one slice.
const wedgeAngle = 2 * math.Pi / sliceCount

// startAngle puts a cut straight up the screen, so the six slices sit as two
// vertical cuts plus four diagonals rather than at some arbitrary rotation.
const startAngle = -math.Pi / 2

// pieGrid rasterizes the pie at the given spread (0 = whole, 1 = fully parted).
func pieGrid(p pieSize, spread float64) [][]cell {
	if spread < 0 {
		spread = 0
	}
	if spread > 1 {
		spread = 1
	}
	grid := make([][]cell, p.h)
	for y := range grid {
		grid[y] = make([]cell, p.w)
		for x := range grid[y] {
			grid[y][x] = pieCell(p, x, y, spread)
		}
	}
	return grid
}

// pieCell decides what a single terminal cell shows. It walks the six wedges
// and asks each one "after you moved, do you cover this cell?" - which is what
// makes the parted slices land in the right place instead of smearing.
func pieCell(p pieSize, x, y int, spread float64) cell {
	// Cell center in normalized pie coordinates.
	px := (float64(x) + 0.5 - p.cx) / p.rx
	py := (float64(y) + 0.5 - p.cy) / p.ry

	for k := range sliceCount {
		bisector := startAngle + (float64(k)+0.5)*wedgeAngle
		d := spread * p.spread
		// Undo this wedge's displacement, then test against the whole pie.
		qx := px - d*math.Cos(bisector)
		qy := py - d*math.Sin(bisector)

		r := math.Hypot(qx, qy)
		if r > 1 {
			continue
		}
		// Which wedge of the un-moved pie is this point in?
		t := math.Mod(math.Atan2(qy, qx)-startAngle, 2*math.Pi)
		if t < 0 {
			t += 2 * math.Pi
		}
		if int(t/wedgeAngle) != k {
			continue
		}
		// Keep the knife cuts visible even when the slices are still touching.
		frac := math.Mod(t, wedgeAngle) / wedgeAngle
		gap := math.Min(cutGap/math.Max(r, 0.05)/wedgeAngle, cutMax)
		if frac < gap || frac > 1-gap {
			continue
		}
		return crustCell(x, y, r)
	}
	return blank
}

// crustCell picks the texture at a point known to be inside the pie: a baked
// rim around the outer arc, and filling - with the odd apple chunk - across the
// rest of the wedge.
func crustCell(x, y int, r float64) cell {
	if r > 0.82 {
		return cell{r: '▓', color: crustColor}
	}
	if mod(x*7+y*13, 17) == 0 {
		return cell{r: '•', color: chunkColor}
	}
	return cell{r: '▒', color: fillingColor}
}

func mod(a, n int) int { return ((a % n) + n) % n }

func rowsOrNone(want bool, rows []string) []string {
	if !want {
		return nil
	}
	return rows
}

// steamRows are the wisps that curl up once the pie is open. They alternate so
// the steam drifts instead of sitting still.
var steamRows = [][]string{
	{"     (  ~   )  (     ", "      )  (  ~        "},
	{"      )  (   ~       ", "     (  ~ )   )      "},
}

// renderPie returns the pie as styled lines: `spread` opens it, `phase` drives
// the steam, and steam only shows once there is a gap for it to rise from.
// `steam` is false on terminals too short to spare the two rows.
func renderPie(p pieSize, spread float64, phase int, steam bool) []string {
	var out []string
	for _, row := range rowsOrNone(steam, steamRows[phase%len(steamRows)]) {
		if spread <= 0.05 {
			out = append(out, "")
			continue
		}
		s := lipgloss.NewStyle().Foreground(lipgloss.Color(steamColor)).Render(row)
		out = append(out, lipgloss.PlaceHorizontal(p.w, lipgloss.Center, s))
	}
	for _, row := range pieGrid(p, spread) {
		out = append(out, renderRow(row))
	}
	return out
}

// renderRow collapses a row into runs of one color so each line costs a handful
// of style applications rather than one per cell.
func renderRow(row []cell) string {
	var b strings.Builder
	var run strings.Builder
	cur := ""
	flush := func() {
		if run.Len() == 0 {
			return
		}
		if cur == "" {
			b.WriteString(run.String())
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(cur)).Render(run.String()))
		}
		run.Reset()
	}
	for _, c := range row {
		if c.color != cur {
			flush()
			cur = c.color
		}
		run.WriteRune(c.r)
	}
	flush()
	return b.String()
}
