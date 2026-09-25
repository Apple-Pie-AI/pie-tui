package splash

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Apple-Pie-AI/pie-tui/internal/sound"
)

// Every geometric guarantee below has to hold at both sizes. A small pie that is
// not six slices, or that clips itself against its own grid, is not a smaller
// pie - it is a bug that only some terminals would ever see.
var pieSizes = map[string]pieSize{"big": bigPie, "small": smallPie}

// filled reports the occupancy of the pie grid, which is what the geometric
// tests below reason about - the texture on top of it is decoration.
func filled(p pieSize, spread float64) [][]bool {
	g := pieGrid(p, spread)
	out := make([][]bool, len(g))
	for y, row := range g {
		out[y] = make([]bool, len(row))
		for x, c := range row {
			out[y][x] = c.r != ' '
		}
	}
	return out
}

// components counts 4-connected blobs of pie. A cut that reads as a cut on
// screen is one that actually severs the grid, so this is the real test of
// "six defined slices".
func components(m [][]bool) int {
	seen := make([][]bool, len(m))
	for y := range m {
		seen[y] = make([]bool, len(m[y]))
	}
	n := 0
	for y := range m {
		for x := range m[y] {
			if !m[y][x] || seen[y][x] {
				continue
			}
			n++
			stack := [][2]int{{x, y}}
			for len(stack) > 0 {
				p := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				px, py := p[0], p[1]
				if py < 0 || py >= len(m) || px < 0 || px >= len(m[py]) {
					continue
				}
				if !m[py][px] || seen[py][px] {
					continue
				}
				seen[py][px] = true
				stack = append(stack, [2]int{px + 1, py}, [2]int{px - 1, py},
					[2]int{px, py + 1}, [2]int{px, py - 1})
			}
		}
	}
	return n
}

// Once open, the pie has to be exactly six separate pieces on screen. (While
// it is still whole the six cuts meet at the center, so it is one piece with
// six visible cuts - see TestWholePieShowsItsCuts.)
func TestOpenPieIsSixSeparateSlices(t *testing.T) {
	for name, p := range pieSizes {
		t.Run(name, func(t *testing.T) {
			for _, spread := range []float64{openTo, 1} {
				if got := components(filled(p, spread)); got != sliceCount {
					t.Errorf("spread %.2f: got %d slices, want %d", spread, got, sliceCount)
				}
			}
		})
	}
}

// A whole pie still has to look sliced: each cut shows up as a gap with pie on
// both sides of it.
func TestWholePieShowsItsCuts(t *testing.T) {
	for name, p := range pieSizes {
		t.Run(name, func(t *testing.T) {
			m := filled(p, 0)
			cuts := 0
			for _, row := range m {
				for x := 1; x < len(row)-1; x++ {
					if row[x] {
						continue
					}
					left, right := false, false
					for i := 0; i < x; i++ {
						left = left || row[i]
					}
					for i := x + 1; i < len(row); i++ {
						right = right || row[i]
					}
					if left && right {
						cuts++
					}
				}
			}
			if cuts < sliceCount {
				t.Errorf("whole pie shows only %d cut cells - the slices are not defined", cuts)
			}
		})
	}
}

// The wedges must travel outward as the pie opens, not just redraw in place.
func TestOpeningMovesSlicesApart(t *testing.T) {
	for name, p := range pieSizes {
		t.Run(name, func(t *testing.T) {
			width := func(spread float64) int {
				m := filled(p, spread)
				lo, hi := len(m[0]), -1
				for y := range m {
					for x := range m[y] {
						if !m[y][x] {
							continue
						}
						if x < lo {
							lo = x
						}
						if x > hi {
							hi = x
						}
					}
				}
				return hi - lo
			}
			whole, open := width(0), width(openTo)
			if open <= whole {
				t.Errorf("opened pie spans %d columns, whole pie spans %d - slices did not move apart", open, whole)
			}
		})
	}
}

// The animation must not push a slice off the edge of its grid, or it will be
// clipped mid-open.
func TestOpenPieStaysInsideGrid(t *testing.T) {
	for name, p := range pieSizes {
		t.Run(name, func(t *testing.T) {
			m := filled(p, 1)
			for y := range m {
				for _, x := range []int{0, p.w - 1} {
					if m[y][x] {
						t.Fatalf("pie touches column %d at row %d: grid is too narrow", x, y)
					}
				}
			}
			for _, y := range []int{0, p.h - 1} {
				for x := range m[y] {
					if m[y][x] {
						t.Fatalf("pie touches row %d at column %d: grid is too short", y, x)
					}
				}
			}
		})
	}
}

func TestPieIsSymmetric(t *testing.T) {
	for name, p := range pieSizes {
		t.Run(name, func(t *testing.T) {
			m := filled(p, 0)
			for y := range m {
				for x := range m[y] {
					if m[y][x] != m[y][p.w-1-x] {
						t.Fatalf("not mirrored left-to-right at (%d,%d)", x, y)
					}
					if m[y][x] != m[p.h-1-y][x] {
						t.Fatalf("not mirrored top-to-bottom at (%d,%d)", x, y)
					}
				}
			}
		})
	}
}

// The grid has to be big enough to hold the slices once they have parted. This
// is the arithmetic behind that - it is what makes a new pieSize safe to add.
func TestPieGridHoldsItsSpreadSlices(t *testing.T) {
	for name, p := range pieSizes {
		t.Run(name, func(t *testing.T) {
			if p.cx != float64(p.w)/2 || p.cy != float64(p.h)/2 {
				t.Errorf("center (%.1f,%.1f) is not the middle of a %dx%d grid", p.cx, p.cy, p.w, p.h)
			}
			// Cells are sampled at their centers, so the outermost cell a slice can
			// cover sits half a cell inside the edge: the reach has to clear the
			// center of the edge cell, not the edge itself.
			if reach, room := p.rx*(1+p.spread), p.cx-0.5; reach >= room {
				t.Errorf("slices reach %.2f columns from center, past the %.1f the grid can show", reach, room)
			}
			if reach, room := p.ry*(1+p.spread), p.cy-0.5; reach >= room {
				t.Errorf("slices reach %.2f rows from center, past the %.1f the grid can show", reach, room)
			}
			// Terminal cells are about half as wide as they are tall, so the
			// ellipse only reads as a circle near a 2:1 ratio.
			if r := p.rx / p.ry; r < 1.8 || r > 2.2 {
				t.Errorf("rx/ry is %.2f - the pie will not read as round", r)
			}
		})
	}
}

func TestSpreadTimeline(t *testing.T) {
	if got := spreadAt(openAt - 1); got != 0 {
		t.Errorf("spread before the pie opens = %v, want 0", got)
	}
	if got := spreadAt(openAt + openFor + 50); got != openTo {
		t.Errorf("spread after the pie opens = %v, want %v", got, openTo)
	}
	prev := -1.0
	for f := openAt; f <= openAt+openFor; f++ {
		s := spreadAt(f)
		if s < prev {
			t.Fatalf("spread went backwards at frame %d: %v after %v", f, s, prev)
		}
		prev = s
	}
}

// Every letter of the wordmark needs a glyph, and every glyph needs to be the
// same height, or the rows come out ragged.
func TestGlyphsCoverTitle(t *testing.T) {
	for _, r := range title {
		g, ok := glyphs[r]
		if !ok {
			t.Fatalf("no glyph for %q in title", r)
		}
		if len(g) != glyphH {
			t.Fatalf("glyph %q is %d rows, want %d", r, len(g), glyphH)
		}
		for i, row := range g {
			if len([]rune(row)) != len([]rune(g[0])) {
				t.Fatalf("glyph %q row %d is a different width than row 0", r, i)
			}
		}
	}
}

// The wordmark fills in letter by letter but must never change width, or it
// would slide sideways as it reveals.
func TestTitleWidthIsStableWhileRevealing(t *testing.T) {
	want := titleWidth()
	for letters := 0; letters <= len(title)+2; letters++ {
		for i, row := range renderTitle(letters, 0) {
			if got := len([]rune(stripANSI(row))); got != want {
				t.Fatalf("letters=%d row=%d width %d, want %d", letters, i, got, want)
			}
		}
	}
}

// An ordinary ~27-row window has to get the full ASCII wordmark. Demanding
// more rows than the layout actually needs silently downgraded it to the
// one-line fallback, which is the bug this guards.
func TestTallWordmarkSurvivesOrdinaryTerminals(t *testing.T) {
	// A row only the tall wordmark can produce, taken from the glyph table so
	// this keeps working the next time the font is redrawn.
	sentinel := strings.TrimSpace(glyphs['a'][3])
	for _, h := range []int{glyphH + steamH + bigPie.h + promptH, 24, 27, 30, 50} {
		s := stripANSI(frameAt(animDone, 100, h, ""))
		if !strings.Contains(s, sentinel) {
			t.Errorf("h=%d fell back to the compact title:\n%s", h, s)
		}
	}
}

// 80x24 is the default terminal, so it is the size that actually has to look
// right: the full-size wordmark, a pie small enough to sit under it, no steam,
// and the version stamp - all inside 24 rows with room to spare.
func TestDefaultTerminalGetsTheFullWordmark(t *testing.T) {
	const w, h = 80, 24
	l := layout(w, h, promptH+versionH)
	if !l.tall {
		t.Error("80x24 fell back to the one-line title")
	}
	if !l.withPie {
		t.Error("80x24 dropped the pie entirely")
	}
	if l.pie != smallPie {
		t.Error("80x24 did not shrink the pie")
	}
	if l.withSteam {
		t.Error("80x24 kept the steam")
	}

	s := stripANSI(frameAt(animDone, w, h, "1.4.0"))
	rows := strings.Count(s, "\n") + 1
	if rows > h {
		t.Errorf("80x24 rendered %d rows:\n%s", rows, s)
	}
	if !strings.Contains(s, strings.TrimSpace(glyphs['a'][3])) {
		t.Errorf("80x24 is missing the tall wordmark:\n%s", s)
	}
	if !strings.Contains(s, "▓") {
		t.Errorf("80x24 is missing the pie:\n%s", s)
	}
	for _, want := range []string{"PRESS", "v1.4.0"} {
		if !strings.Contains(s, want) {
			t.Errorf("80x24 is missing %q:\n%s", want, s)
		}
	}
	// Every row has to fit the width too, or the terminal wraps and the
	// animation smears.
	for i, line := range strings.Split(s, "\n") {
		if n := len([]rune(line)); n > w {
			t.Errorf("row %d is %d columns wide, past the %d available", i, n, w)
		}
	}
}

// Shrinking the pie is a step down, not a cliff: a terminal with room for the
// big pie must still get it.
func TestRoomyTerminalKeepsTheBigPieAndSteam(t *testing.T) {
	l := layout(100, 40, promptH+versionH)
	if l.pie != bigPie || !l.tall || !l.withSteam {
		t.Errorf("a 100x40 terminal got %+v, want the big pie with steam", l)
	}
}

// Whatever it decides to drop, a frame must never be taller than the terminal
// it was drawn for, or the screen scrolls and the animation smears.
func TestFrameNeverOverflowsItsHeight(t *testing.T) {
	for _, h := range []int{6, 12, 20, 24, 26, 27, 50} {
		for _, f := range []int{0, pieAt, animDone} {
			if got := strings.Count(frameAt(f, 100, h, ""), "\n") + 1; got > h {
				t.Errorf("h=%d frame=%d rendered %d rows", h, f, got)
			}
		}
	}
}

// The wordmark must not jump up the screen when the pie shows up part-way in.
func TestLayoutHeightIsStableAcrossTheAnimation(t *testing.T) {
	for _, h := range []int{20, 26, 27, 50} {
		want := strings.Count(frameAt(0, 100, h, ""), "\n")
		for f := 0; f <= animDone; f++ {
			if got := strings.Count(frameAt(f, 100, h, ""), "\n"); got != want {
				t.Fatalf("h=%d: frame %d is %d rows, frame 0 was %d - the layout shifts", h, f, got+1, want+1)
			}
		}
	}
}

// Every note in the score has to land on the exact frame its letter appears.
// This is the join between picture and sound: the score is baked into one clip
// before the animation starts, so nothing at runtime can pull them back together
// if they are written against different timelines here.
func TestScoreLandsWithTheReveal(t *testing.T) {
	runes := []rune(title)
	if len(runes) > sound.Letters() {
		t.Fatalf("the wordmark has %d letters but only %d cues exist", len(runes), sound.Letters())
	}

	s := score()
	if len(s) != len(runes)+1 {
		t.Fatalf("score has %d notes for %d letters plus the chord", len(s), len(runes))
	}
	for i := range runes {
		n := s[i]
		if want := sound.Letter(i); n.Clip != want {
			t.Errorf("note %d is %q, want %q - the phrase is out of order", i, n.Clip, want)
		}
		// The frame this note sounds on must be the frame the renderer first
		// draws that letter.
		frame := int(n.At / tickInterval)
		if got := lettersAt(frame); got != i+1 {
			t.Errorf("note %d sounds on frame %d, where %d glyphs are showing", i, frame, got)
		}
		if frame > 0 && lettersAt(frame-1) != i {
			t.Errorf("note %d sounds on frame %d, a frame after its letter appeared", i, frame)
		}
	}
}

// The pie's chord is part of the same clip, so it has to be written to the frame
// the slices actually part on.
func TestScoreChordLandsWhenThePieOpens(t *testing.T) {
	s := score()
	chord := s[len(s)-1]
	if chord.Clip != sound.Open {
		t.Fatalf("the score ends on %q, want the chord", chord.Clip)
	}
	if got := int(chord.At / tickInterval); got != openAt {
		t.Errorf("the chord is written for frame %d, but the pie opens on %d", got, openAt)
	}
}

// The wordmark holds between "apple" and "Pie". That beat has to be real and it
// has to be silent - it is the whole point of the pause.
func TestTheRestBetweenTheHalvesIsSilent(t *testing.T) {
	appleDone := revealSlot(titleSplit-1) * framesPerLetter
	pieStarts := revealSlot(titleSplit) * framesPerLetter
	if pieStarts-appleDone <= framesPerLetter {
		t.Fatalf("%d ticks between the halves - that is no rest at all", pieStarts-appleDone)
	}
	// Nothing may sound in the gap, and the picture must hold all of "apple"
	// across it - a rest that either half fills in is not a rest.
	for _, n := range score() {
		if f := int(n.At / tickInterval); f > appleDone && f < pieStarts {
			t.Errorf("%q sounds on frame %d, inside the rest", n.Clip, f)
		}
	}
	for f := appleDone + 1; f < pieStarts; f++ {
		if n := lettersAt(f); n != titleSplit {
			t.Errorf("frame %d shows %d glyphs during the rest, want all of %q", f, n, title[:titleSplit])
		}
	}
	// And the whole reveal still has to finish before the pie arrives.
	if last := revealSlot(len([]rune(title))-1) * framesPerLetter; last >= pieAt {
		t.Errorf("the wordmark finishes on frame %d, but the pie lands on %d", last, pieAt)
	}
}

// The score rises through the phrase and never doubles back - the notes have to
// be in ascending time order, or mixing them lays the melody down scrambled.
func TestScoreRunsForward(t *testing.T) {
	s := score()
	for i := 1; i < len(s); i++ {
		if s[i].At <= s[i-1].At {
			t.Errorf("note %d lands at %v, not after note %d at %v", i, s[i].At, i-1, s[i-1].At)
		}
	}
	if last := s[len(s)-1].At; last > time.Duration(animDone)*tickInterval {
		t.Errorf("the score runs to %v, past the end of the animation", last)
	}
}

// A stalled terminal must drop frames, not replay them late: the soundtrack is
// one clip already running and it will not wait for the picture.
func TestFrameDueCatchesUpAfterAStall(t *testing.T) {
	start := time.Now().Add(-20 * tickInterval)
	if got := frameDue(start, 3); got != 20 {
		t.Errorf("after a 20-tick stall on frame 3 the clock reads %d, want 20", got)
	}
	// On time, it is an ordinary single step - and never runs backwards.
	if got := frameDue(time.Now(), 5); got != 6 {
		t.Errorf("an on-time tick advanced frame 5 to %d, want 6", got)
	}
}

func TestStaticShowsFinishedScreen(t *testing.T) {
	s := stripANSI(Static(""))
	for _, want := range []string{"PRESS", "ENTER", "START"} {
		if !strings.Contains(s, want) {
			t.Errorf("static splash is missing %q:\n%s", want, s)
		}
	}
	if !strings.Contains(s, "▓") {
		t.Error("static splash is missing the pie")
	}
}

// The wordmark is the logo's two-tone lockup: everything left of the P is olive,
// everything from the P on is orange-red. The gloss may light either half, but it
// must never carry a column across the split into the other half's color.
func TestWordmarkKeepsItsTwoTone(t *testing.T) {
	olive := map[string]bool{appleInk: true, appleGloss: true}
	orange := map[string]bool{pieInk: true, pieGloss: true}
	for frame := 0; frame <= animDone; frame++ {
		for col := range titleWidth() {
			got := sweepColor(col, frame)
			if col < splitCol() && !olive[got] {
				t.Fatalf("frame %d col %d (apple) inked %s", frame, col, got)
			}
			if col >= splitCol() && !orange[got] {
				t.Fatalf("frame %d col %d (Pie) inked %s", frame, col, got)
			}
		}
	}
}

// The gloss has to actually travel: sit still and it is just a bright stripe.
func TestGlossSweepsTheWholeWordmark(t *testing.T) {
	lit := map[int]bool{}
	for frame := 0; frame <= animDone; frame++ {
		for col := range titleWidth() {
			if c := sweepColor(col, frame); c == appleGloss || c == pieGloss {
				lit[col] = true
			}
		}
	}
	if len(lit) != titleWidth() {
		t.Errorf("the gloss lit %d of %d columns over the animation", len(lit), titleWidth())
	}
}

func TestVersionIsStampedUnderThePrompt(t *testing.T) {
	s := stripANSI(Static("1.4.0"))
	if !strings.Contains(s, "v1.4.0") {
		t.Errorf("release version is missing its v:\n%s", s)
	}
	// A dev build is already named, not numbered - it must not become "vdev".
	if s := stripANSI(Static("dev")); !strings.Contains(s, "dev") || strings.Contains(s, "vdev") {
		t.Errorf("dev build stamped wrong:\n%s", s)
	}
	if s := stripANSI(Static("")); strings.Contains(s, "v1.4.0") {
		t.Error("an empty version still drew a stamp")
	}
}

// The version line costs rows, so the layout has to budget for it - a stamped
// frame must still fit the terminal it was drawn for.
func TestVersionFitsTheHeightBudget(t *testing.T) {
	for _, h := range []int{6, 12, 20, 24, 27, 32, 50} {
		for _, f := range []int{0, pieAt, animDone} {
			s := frameAt(f, 100, h, "1.4.0")
			if got := strings.Count(s, "\n") + 1; got > h {
				t.Errorf("h=%d frame=%d rendered %d rows", h, f, got)
			}
			if !strings.Contains(stripANSI(s), "v1.4.0") {
				t.Errorf("h=%d frame=%d dropped the version stamp", h, f)
			}
		}
	}
}

func TestSeenRoundTrip(t *testing.T) {
	t.Setenv("PIE_HOME", t.TempDir())
	if Seen() {
		t.Fatal("Seen() is true before the splash has ever played")
	}
	MarkSeen()
	if !Seen() {
		t.Fatal("Seen() is false right after MarkSeen()")
	}
}

// MarkSeen must not explode when it cannot write - a broken marker means the
// splash replays, which is far better than a CLI that refuses to start.
func TestMarkSeenSurvivesUnwritableHome(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Setenv("PIE_HOME", f.Name()) // a file where a directory should be
	MarkSeen()
	if Seen() {
		t.Error("Seen() reports true despite the marker never being written")
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
