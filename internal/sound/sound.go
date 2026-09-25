// Package sound plays the splash screen's chiptune cues.
//
// The clips are synthesized in pure Go (see synth.go), written to a temp dir as
// WAVs, and handed to whatever audio command the OS already ships - afplay on
// macOS, paplay/aplay/ffplay on Linux. That is deliberate: every Go audio
// library needs cgo to reach CoreAudio or ALSA, and this repo releases
// CGO_ENABLED=0 cross-compiled binaries for four platforms.
//
// A Player is nil-safe throughout. When there is no audio command, no temp dir,
// or PIE_NO_SOUND is set, New returns nil and every method is a no-op, so
// callers never branch on whether sound is available.
package sound

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Cue names.
const Open = "open"

// Letter names the cue for the i'th sounded letter of the wordmark.
func Letter(i int) string { return "letter" + strconv.Itoa(i%len(pentatonic)) }

// Letters is how many distinct letter cues exist.
func Letters() int { return len(pentatonic) }

type Player struct {
	dir   string
	clips map[string]string  // name -> rendered wav on disk
	pcm   map[string][]int16 // name -> raw samples, so scores can be mixed
	spawn func(path string) *exec.Cmd

	mu    sync.Mutex
	procs []*exec.Cmd
}

// A Note is one sound in a score: which cue, and how far into the phrase it
// lands.
type Note struct {
	Clip string
	At   time.Duration
}

// New renders the cues and returns a player, or nil if this machine cannot (or
// should not) make noise.
func New() *Player {
	if os.Getenv("PIE_NO_SOUND") != "" {
		return nil
	}
	spawn := findPlayer()
	if spawn == nil {
		return nil
	}
	dir, err := os.MkdirTemp("", "pie-splash-")
	if err != nil {
		return nil
	}
	p := newPlayer(dir, spawn)
	for i := range Letters() {
		if !p.render(Letter(i), blipClip(i)) {
			p.Close()
			return nil
		}
	}
	if !p.render(Open, openClip()) {
		p.Close()
		return nil
	}
	return p
}

// newPlayer builds an empty player. New and the tests both go through it, so a
// player can never come into being with only half its tables.
func newPlayer(dir string, spawn func(string) *exec.Cmd) *Player {
	return &Player{
		dir:   dir,
		clips: map[string]string{},
		pcm:   map[string][]int16{},
		spawn: spawn,
	}
}

func (p *Player) render(name string, samples []int16) bool {
	path := filepath.Join(p.dir, name+".wav")
	if err := os.WriteFile(path, wav(samples), 0o600); err != nil {
		return false
	}
	p.clips[name] = path
	p.pcm[name] = samples
	return true
}

// Mix renders a whole score into a single clip, so the phrase plays as one
// stream from one process.
//
// This is not an optimization, it is the only way the timing works. Spawning the
// OS audio command costs hundreds of milliseconds before the first sample is
// audible, and that cost is paid per process and varies wildly run to run - far
// more than the gap between two notes. A cue per process therefore arrives
// scattered, in a different order every time. Mixed into one buffer the spacing
// is sample-accurate and byte-identical on every run, and the process overhead
// can only shift where the phrase as a whole begins.
//
// Mix is deliberately callable before the animation starts: it is the expensive
// part (synthesis and a file write), and it must not land on the render loop.
func (p *Player) Mix(name string, score []Note) bool {
	if p == nil || len(score) == 0 {
		return false
	}
	end := 0
	for _, n := range score {
		s, ok := p.pcm[n.Clip]
		if !ok {
			return false
		}
		if e := sampleAt(n.At) + len(s); e > end {
			end = e
		}
	}
	buf := make([]int16, end)
	for _, n := range score {
		off := sampleAt(n.At)
		for i, s := range p.pcm[n.Clip] {
			// Notes can overlap; sum them and hold the ceiling rather than
			// letting int16 wrap, which would crack instead of blend.
			v := int(buf[off+i]) + int(s)
			buf[off+i] = int16(min(max(v, -32768), 32767))
		}
	}
	return p.render(name, buf)
}

// Has reports whether a cue exists to play.
func (p *Player) Has(name string) bool {
	if p == nil {
		return false
	}
	_, ok := p.clips[name]
	return ok
}

func sampleAt(d time.Duration) int { return int(d.Seconds() * sampleRate) }

// Play starts a cue and returns immediately - the animation must never wait on
// audio.
func (p *Player) Play(name string) {
	if p == nil {
		return
	}
	path, ok := p.clips[name]
	if !ok {
		return
	}
	cmd := p.spawn(path)
	// The TUI owns the terminal; a player that printed to it would corrupt the
	// frame. nil here means /dev/null, not "inherit".
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return
	}
	p.mu.Lock()
	p.procs = append(p.procs, cmd)
	p.mu.Unlock()
	go func() { _ = cmd.Wait() }() // reap, so finished players aren't zombies
}

// Stop cuts anything still playing but leaves the player usable - for when the
// score stops matching what is on screen and a new cue has to take over.
func (p *Player) Stop() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.procs {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
	p.procs = nil
}

// Close silences anything still playing and removes the rendered clips.
func (p *Player) Close() {
	if p == nil {
		return
	}
	p.Stop()
	_ = os.RemoveAll(p.dir)
}

// findPlayer picks the first audio command present on this machine. Order
// matters only in that the platform-native one should win.
func findPlayer() func(string) *exec.Cmd {
	candidates := []struct {
		bin  string
		args []string
	}{
		{"afplay", nil},           // macOS
		{"paplay", nil},           // PulseAudio / PipeWire
		{"aplay", []string{"-q"}}, // ALSA
		{"play", []string{"-q"}},  // sox
		{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}}, // ffmpeg
	}
	for _, c := range candidates {
		path, err := exec.LookPath(c.bin)
		if err != nil {
			continue
		}
		args := c.args
		return func(f string) *exec.Cmd {
			return exec.Command(path, append(append([]string{}, args...), f)...)
		}
	}
	return nil
}
