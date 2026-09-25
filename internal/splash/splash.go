// Package splash draws the first-run title screen: the "applePie" wordmark in
// the logo's own two-tone lettering, revealed a letter at a time, a top-down
// apple pie that cuts itself into six slices, and a blinking PRESS ENTER TO
// START.
//
// Picture and sound run off one clock. The frame schedule is absolute - frame N
// is due at start + N*tickInterval - and the soundtrack is mixed from that same
// schedule into a single clip before the animation starts, so neither can drift
// against the other no matter how slow the terminal is. See score().
//
// It plays once, the first time the CLI is run on a machine, and after that
// only when asked for with `--splash`.
package splash

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"

	"github.com/Apple-Pie-AI/pie-tui/internal/paths"
	"github.com/Apple-Pie-AI/pie-tui/internal/sound"
)

// ErrAborted means the user quit out of the splash (esc / q / ctrl+c) rather
// than starting. Callers should exit instead of continuing into the app.
var ErrAborted = errors.New("splash: aborted")

// The timeline, in ticks. One tick is tickInterval.
const (
	tickInterval = 70 * time.Millisecond

	framesPerLetter = 2 // wordmark reveals one letter every N ticks

	// restBeats is how long the wordmark holds after "apple" before "Pie"
	// starts, counted in letter beats. The title used to be two words and the
	// space bought this pause for free; the logo sets it as one word, so the
	// rest has to live in the timing instead of in the lettering.
	restBeats = 2

	pieAt      = 22 // whole pie appears
	openAt     = 28 // slices start sliding apart
	openFor    = 26 // ...over this many ticks
	promptAt   = 56 // PRESS ENTER starts blinking
	blinkEvery = 8

	// openTo is how far the slices travel. A full 1.0 flings them off into the
	// corners; this is "served on the table" far.
	openTo = 0.46

	// The gloss band that travels across the wordmark: how many columns it
	// lights at once, and how many columns it moves per tick.
	glossW     = 5
	glossSpeed = 2

	// The screen's height budget, in rows: wordmark + steam lane + pie +
	// the PRESS ENTER line and its hint, and the version set off below it.
	// Layout degrades against these.
	promptH  = 2
	steamH   = 2
	versionH = 2
)

var animDone = promptAt

// phraseClip is the name the whole soundtrack is mixed under - see score().
const phraseClip = "phrase"

type (
	tickMsg  time.Time
	startMsg time.Time
)

type model struct {
	frame   int
	start   time.Time // frame N is due at start + N*tickInterval
	w, h    int
	aborted bool
	version string
	snd     *sound.Player // nil when this machine has no way to make noise
}

// Init stamps the clock from inside the program rather than at construction, so
// the phrase starts when the animation does and not while the alt screen is
// still being set up.
func (m model) Init() tea.Cmd {
	return func() tea.Msg { return startMsg(time.Now()) }
}

// tickAt schedules a frame against an absolute deadline. Chaining relative
// timers instead folds each frame's render cost into the next interval, and
// those milliseconds accumulate - by the end of the phrase the picture has
// slipped behind the soundtrack, which is baked at fixed offsets and cannot slip
// with it.
func tickAt(start time.Time, frame int) tea.Cmd {
	due := start.Add(time.Duration(frame) * tickInterval)
	return tea.Tick(time.Until(due), func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case startMsg:
		m.start = time.Time(msg)
		// One process, one stream, started the moment the clock starts.
		m.snd.Play(phraseClip)
		return m, tickAt(m.start, 1)
	case tickMsg:
		m.frame = frameDue(m.start, m.frame)
		return m, tickAt(m.start, m.frame+1)
	case tea.KeyMsg:
		switch msg.String() {
		case "enter", " ":
			return m, tea.Quit
		case "ctrl+c", "esc", "q":
			m.aborted = true
			return m, tea.Quit
		default:
			// Any other key is impatience, not a decision: jump to the end of
			// the animation so the prompt is there to press.
			if m.frame < animDone {
				// The phrase is a melody written against the reveal. Once the
				// reveal is skipped it is scoring a screen that no longer
				// exists, so cut it - but the pie still opens, so let its chord
				// stand in for the rest of the score.
				m.snd.Stop()
				if m.frame < openAt {
					m.snd.Play(sound.Open)
				}
				m.frame = animDone
				// Rebase the clock, or every later frame's absolute deadline is
				// still sitting in the animation we just skipped and the prompt
				// stops blinking until the timeline catches up.
				m.start = time.Now().Add(-time.Duration(m.frame) * tickInterval)
			}
			return m, nil
		}
	}
	return m, nil
}

// frameDue is the frame the clock has actually reached, never going backwards
// and never standing still. A stalled terminal drops frames here rather than
// playing them late: the soundtrack is already running and will not wait.
func frameDue(start time.Time, cur int) int {
	next := cur + 1
	if due := int(time.Since(start) / tickInterval); due > next {
		return due
	}
	return next
}

// score is the splash's whole soundtrack: a note as each letter lands, and the
// chord when the pie parts. It is built from revealSlot - the same schedule that
// draws the lettering - so the phrase and the picture cannot be written against
// different timelines.
func score() []sound.Note {
	s := make([]sound.Note, 0, len([]rune(title))+1)
	for i := range []rune(title) {
		s = append(s, sound.Note{
			Clip: sound.Letter(i),
			At:   time.Duration(revealSlot(i)*framesPerLetter) * tickInterval,
		})
	}
	return append(s, sound.Note{Clip: sound.Open, At: openAt * tickInterval})
}

func (m model) View() string {
	body := frameAt(m.frame, m.w, m.h, m.version)
	if m.w == 0 || m.h == 0 {
		return body
	}
	return lipgloss.Place(m.w, m.h, lipgloss.Center, lipgloss.Center, body)
}

// revealSlot is the beat a letter lands on. "apple" fills in a letter a beat;
// then the word holds for restBeats before "Pie" picks it up again.
func revealSlot(i int) int {
	if i >= titleSplit {
		return i + restBeats
	}
	return i
}

// lettersAt is how many glyphs of the wordmark are showing on this frame.
func lettersAt(frame int) int {
	n := 0
	for i := range []rune(title) {
		if frame >= revealSlot(i)*framesPerLetter {
			n++
		}
	}
	return n
}

// A screenLayout is what a given terminal can actually hold.
type screenLayout struct {
	pie                      pieSize
	tall, withPie, withSteam bool
}

// layout picks the biggest thing that fits, in a deliberate order of preference:
// every option that keeps the wordmark full size comes before any option that
// shrinks it. Tall lettering is the point of the screen and a big pie is not, so
// when a default 80x24 terminal cannot have both, the pie is what gives.
//
// The steam only ever rides with the big pie. Once the pie has been shrunk we are
// already fitting the screen into a box it does not want to be in, and two rows
// of decoration are not what to spend the remaining space on.
func layout(w, h, footH int) screenLayout {
	fits := func(rows int) bool { return h <= 0 || h >= rows }

	// A wordmark wider than the terminal has to collapse whatever the height is.
	if w <= 0 || w >= titleWidth() {
		switch {
		case fits(glyphH + steamH + bigPie.h + footH):
			return screenLayout{bigPie, true, true, true}
		case fits(glyphH + bigPie.h + footH):
			return screenLayout{bigPie, true, true, false}
		case fits(glyphH + smallPie.h + footH):
			return screenLayout{smallPie, true, true, false}
		case fits(glyphH + footH):
			return screenLayout{bigPie, true, false, false}
		}
	}
	// Nothing fits the tall wordmark: one-line title, same order underneath.
	switch {
	case fits(1 + steamH + bigPie.h + footH):
		return screenLayout{bigPie, false, true, true}
	case fits(1 + bigPie.h + footH):
		return screenLayout{bigPie, false, true, false}
	case fits(1 + smallPie.h + footH):
		return screenLayout{smallPie, false, true, false}
	}
	return screenLayout{bigPie, false, false, false}
}

// frameAt composes one frame of the splash for a terminal of size w x h, with
// version stamped under the prompt (empty for no stamp). It is pure, which is
// what lets Static() reuse it for non-interactive output.
func frameAt(frame, w, h int, version string) string {
	// The footer is the prompt plus, when we have one, the version line and the
	// blank row that sets it apart.
	footH := promptH
	if version != "" {
		footH += versionH
	}

	l := layout(w, h, footH)

	letters := lettersAt(frame)
	var blocks []string
	if l.tall {
		blocks = append(blocks, strings.Join(renderTitle(letters, frame), "\n"))
	} else {
		blocks = append(blocks, compactTitle(letters, frame))
	}

	if l.withPie {
		if frame >= pieAt {
			blocks = append(blocks, strings.Join(renderPie(l.pie, spreadAt(frame), frame/4, l.withSteam), "\n"))
		} else {
			// Hold the pie's space from the start so the wordmark doesn't jump
			// up the screen the moment the pie arrives.
			rows := l.pie.h
			if l.withSteam {
				rows += steamH
			}
			blocks = append(blocks, strings.Repeat("\n", rows-1))
		}
	}

	blocks = append(blocks, prompt(frame))
	if version != "" {
		blocks = append(blocks, "", versionLine(version))
	}
	return lipgloss.JoinVertical(lipgloss.Center, blocks...)
}

// versionLine stamps the build under the prompt, dim enough to read as a
// footnote. Release builds carry a bare "1.4.0" (GoReleaser strips the v), so
// put it back; a dev build stays "dev".
func versionLine(v string) string {
	if r := []rune(v)[0]; r >= '0' && r <= '9' {
		v = "v" + v
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(dimColor)).Render(v)
}

// spreadAt eases the slices apart: fast at first, settling at the end, the way
// a knife-lifted slice actually moves.
func spreadAt(frame int) float64 {
	if frame < openAt {
		return 0
	}
	t := float64(frame-openAt) / openFor
	if t > 1 {
		t = 1
	}
	return openTo * (1 - math.Pow(1-t, 3))
}

func prompt(frame int) string {
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(hintColor))
	if frame < promptAt {
		return lipgloss.JoinVertical(lipgloss.Center, "",
			hintStyle.Render("any key to skip  ·  esc to quit"))
	}
	hint := hintStyle.Render("esc to quit")
	text := "PRESS  ENTER  TO  START"
	style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(promptColor))
	if (frame/blinkEvery)%2 == 1 {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color(dimColor))
	}
	return lipgloss.JoinVertical(lipgloss.Center, style.Render(text), hint)
}

// ---- wordmark --------------------------------------------------------------

func titleWidth() int {
	w := 0
	for _, r := range title {
		w += lipgloss.Width(glyphFor(r)[0])
	}
	return w
}

func glyphFor(r rune) []string {
	if g, ok := glyphs[r]; ok {
		return g
	}
	return glyphs[' ']
}

// renderTitle draws the wordmark with the first `letters` glyphs revealed.
// Hidden glyphs are still padded out, so the word never slides sideways as it
// fills in - it just lights up left to right.
func renderTitle(letters, frame int) []string {
	rows := make([][]rune, glyphH)
	for _, r := range title {
		g := glyphFor(r)
		show := letters > 0
		letters--
		for i := range glyphH {
			if show {
				rows[i] = append(rows[i], []rune(g[i])...)
			} else {
				rows[i] = append(rows[i], []rune(strings.Repeat(" ", lipgloss.Width(g[0])))...)
			}
		}
	}
	out := make([]string, glyphH)
	for i, row := range rows {
		cells := make([]cell, len(row))
		for x, r := range row {
			cells[x] = cell{r: r, color: sweepColor(x, frame)}
		}
		out[i] = renderRow(cells)
	}
	return out
}

func compactTitle(letters, frame int) string {
	var cells []cell
	// The compact line is one column per letter, but it is colored by where that
	// letter sits in the *tall* wordmark, so the ink splits at the P and the
	// gloss crosses it on the same beat either way.
	col := 0
	for i, r := range title {
		if i >= letters {
			break
		}
		cells = append(cells, cell{r: r, color: sweepColor(col, frame)})
		cells = append(cells, cell{r: ' '})
		col += lipgloss.Width(glyphFor(r)[0])
	}
	return renderRow(cells)
}

// splitCol is the column where the olive "apple" ends and the orange-red "Pie"
// begins - the logo's two-tone break, measured in wordmark columns.
func splitCol() int {
	w := 0
	for _, r := range []rune(title)[:titleSplit] {
		w += lipgloss.Width(glyphFor(r)[0])
	}
	return w
}

// sweepColor inks a column in whichever half of the logo it falls in, lit to
// that half's brighter tint while the gloss band is passing over it. At rest the
// wordmark is exactly the printed two-tone; the band is what keeps it alive.
func sweepColor(col, frame int) string {
	ink, lit := appleInk, appleGloss
	if col >= splitCol() {
		ink, lit = pieInk, pieGloss
	}
	if d := col - glossAt(frame); d >= 0 && d < glossW {
		return lit
	}
	return ink
}

// glossAt is the left edge of the highlight band on this frame. It enters a full
// band-width off the left edge and runs the same distance past the right, so the
// sweep slides on and off the word instead of popping in mid-letter.
func glossAt(frame int) int {
	return mod(frame*glossSpeed, titleWidth()+glossW*2) - glossW
}

// ---- entry points ----------------------------------------------------------

// Run plays the splash on the current terminal and blocks until the user
// presses enter (nil) or quits out of it (ErrAborted). version is stamped under
// the prompt; pass "" to leave it off.
func Run(version string) error {
	snd := sound.New()
	defer snd.Close() // also cuts off anything still ringing when they hit enter

	// Mix the soundtrack before the program exists. Synthesis and the file write
	// are the expensive part of making noise, and down here they cost nothing the
	// animation can feel; once it is running, starting the phrase is one fork.
	snd.Mix(phraseClip, score())

	m, err := tea.NewProgram(model{snd: snd, version: version}, tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if final, ok := m.(model); ok && final.aborted {
		return ErrAborted
	}
	return nil
}

// Static returns the finished title screen as plain text, for when there is no
// terminal to animate on (a pipe, CI, a redirected log).
func Static(version string) string { return frameAt(animDone, 0, 0, version) }

// Interactive reports whether there is a real terminal to animate on.
func Interactive() bool {
	return term.IsTerminal(int(os.Stdout.Fd())) && term.IsTerminal(int(os.Stdin.Fd()))
}

// seenFile marks that the splash has already played on this machine.
func seenFile() string { return filepath.Join(paths.Root(), "splash-seen") }

// Seen reports whether the first-run splash has already played.
func Seen() bool {
	_, err := os.Stat(seenFile())
	return err == nil
}

// MarkSeen records that the splash has played. It goes through EnsureDirs
// rather than creating the home itself: the splash is the very first thing a
// launch does, so ~/.pie may not exist yet and the marker has to land in a
// fully scaffolded home.
//
// Best-effort - a splash that replays because the marker could not be written
// is a far better failure than a CLI that refuses to start.
func MarkSeen() {
	if err := paths.EnsureDirs(); err != nil {
		return
	}
	_ = os.WriteFile(seenFile(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}
