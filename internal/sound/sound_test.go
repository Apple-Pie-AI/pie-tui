package sound

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A malformed header is silence with no error message, so pin the whole thing.
func TestWavHeaderIsWellFormed(t *testing.T) {
	samples := blipClip(0)
	b := wav(samples)

	if len(b) != 44+len(samples)*2 {
		t.Fatalf("wav is %d bytes, want %d", len(b), 44+len(samples)*2)
	}
	if got := string(b[0:4]); got != "RIFF" {
		t.Errorf("magic = %q, want RIFF", got)
	}
	if got := string(b[8:12]); got != "WAVE" {
		t.Errorf("format = %q, want WAVE", got)
	}
	if got := string(b[12:16]); got != "fmt " {
		t.Errorf("first chunk = %q, want 'fmt '", got)
	}
	le32 := func(off int) uint32 { return binary.LittleEndian.Uint32(b[off:]) }
	le16 := func(off int) uint16 { return binary.LittleEndian.Uint16(b[off:]) }

	if got, want := le32(4), uint32(36+len(samples)*2); got != want {
		t.Errorf("RIFF size = %d, want %d", got, want)
	}
	if got := le16(20); got != 1 {
		t.Errorf("audio format = %d, want 1 (PCM)", got)
	}
	if got := le16(22); got != channels {
		t.Errorf("channels = %d, want %d", got, channels)
	}
	if got := le32(24); got != sampleRate {
		t.Errorf("sample rate = %d, want %d", got, sampleRate)
	}
	if got := le16(34); got != bitDepth {
		t.Errorf("bit depth = %d, want %d", got, bitDepth)
	}
	if got := string(b[36:40]); got != "data" {
		t.Errorf("second chunk = %q, want data", got)
	}
	if got, want := le32(40), uint32(len(samples)*2); got != want {
		t.Errorf("data size = %d, want %d", got, want)
	}
}

// Every cue has to be audible, has to start and end at silence (anything else
// is a click), and must not clip.
func TestClipsAreAudibleAndClean(t *testing.T) {
	clips := map[string][]int16{"open": openClip()}
	for i := range Letters() {
		clips[Letter(i)] = blipClip(i)
	}
	for name, s := range clips {
		if len(s) < sampleRate/20 {
			t.Errorf("%s: only %d samples - too short to hear", name, len(s))
			continue
		}
		peak := 0
		for _, v := range s {
			if a := int(v); a > peak {
				peak = a
			} else if -a > peak {
				peak = -a
			}
		}
		if peak < 3000 {
			t.Errorf("%s: peak amplitude %d - inaudibly quiet", name, peak)
		}
		if peak >= 32767 {
			t.Errorf("%s: peak amplitude %d - clipping", name, peak)
		}
		if s[0] != 0 {
			t.Errorf("%s: starts at %d instead of silence - will click", name, s[0])
		}
		if last := s[len(s)-1]; last != 0 {
			t.Errorf("%s: ends at %d instead of silence - will click", name, last)
		}
	}
}

// The letter cues are a rising phrase, not one beep repeated. Zero crossings
// are a good enough proxy for pitch on a square wave.
func TestLetterCuesRiseInPitch(t *testing.T) {
	prev := 0
	for i := range Letters() {
		s := blipClip(i)
		crossings := 0
		for j := 1; j < len(s); j++ {
			if (s[j-1] < 0) != (s[j] < 0) {
				crossings++
			}
		}
		if i > 0 && crossings <= prev {
			t.Errorf("cue %d has %d zero crossings, not more than cue %d's %d - the phrase is not ascending",
				i, crossings, i-1, prev)
		}
		prev = crossings
	}
}

func TestPentatonicStepsAreInTune(t *testing.T) {
	if got := step(0); math.Abs(got-rootHz) > 0.01 {
		t.Errorf("step(0) = %v, want the root %v", got, rootHz)
	}
	if got := step(12); math.Abs(got-2*rootHz) > 0.01 {
		t.Errorf("step(12) = %v, want an octave up (%v)", got, 2*rootHz)
	}
}

// Sound is a nicety, never a requirement: a machine with no audio command has
// to yield a usable no-op rather than an error or a panic.
func TestSilentMachineYieldsNoOpPlayer(t *testing.T) {
	t.Setenv("PIE_NO_SOUND", "1")
	p := New()
	if p != nil {
		t.Fatal("PIE_NO_SOUND did not disable sound")
	}
	p.Play(Open) // must not panic on the nil receiver
	p.Play(Letter(3))
	p.Close()
}

// A player renders its clips up front and cleans them up on Close.
func TestPlayerRendersAndCleansUpClips(t *testing.T) {
	p := newFake(t)
	if got := len(p.clips); got != Letters()+1 {
		t.Fatalf("rendered %d clips, want %d", got, Letters()+1)
	}
	for name, path := range p.clips {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.HasPrefix(b, []byte("RIFF")) {
			t.Errorf("%s: not a RIFF file", name)
		}
	}
	dir := p.dir
	p.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Close left %s behind", dir)
	}
}

// The point of mixing: a score becomes one clip whose note spacing is baked into
// the samples. Two mixes of the same score must be byte-identical, because that
// is the property a cue-per-process can never have - each process pays its own
// few hundred milliseconds of startup, so the same score played that way arrives
// differently every single run.
func TestMixIsDeterministic(t *testing.T) {
	p := newFake(t)
	defer p.Close()

	s := []Note{
		{Clip: Letter(0), At: 0},
		{Clip: Letter(1), At: 140 * time.Millisecond},
		{Clip: Open, At: 500 * time.Millisecond},
	}
	if !p.Mix("phrase", s) {
		t.Fatal("could not mix the score")
	}
	first, err := os.ReadFile(p.clips["phrase"])
	if err != nil {
		t.Fatal(err)
	}
	if !p.Mix("phrase", s) {
		t.Fatal("could not mix the score a second time")
	}
	second, err := os.ReadFile(p.clips["phrase"])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("the same score mixed to different audio twice")
	}

	// The clip has to be long enough to hold its last note, or the phrase is
	// cut off mid-chord.
	want := sampleAt(500*time.Millisecond) + len(p.pcm[Open])
	if got := len(p.pcm["phrase"]); got != want {
		t.Errorf("mixed clip is %d samples, want %d", got, want)
	}
}

// A score naming a cue that was never rendered must fail, not mix silence and
// pretend the phrase is intact.
func TestMixRejectsAnUnknownCue(t *testing.T) {
	p := newFake(t)
	defer p.Close()
	if p.Mix("phrase", []Note{{Clip: "nosuchcue"}}) {
		t.Error("mixed a score naming a cue that does not exist")
	}
}

// Playing must be fire-and-forget, and must never hand the player the terminal
// - anything it printed would land in the middle of the animation.
func TestPlayIsAsyncAndNeverTouchesTheTerminal(t *testing.T) {
	p := newFake(t)
	defer p.Close()

	p.Play(Open)
	p.mu.Lock()
	n := len(p.procs)
	var cmd *exec.Cmd
	if n > 0 {
		cmd = p.procs[0]
	}
	p.mu.Unlock()

	if n != 1 {
		t.Fatalf("Play started %d processes, want 1", n)
	}
	if cmd.Stdout != nil || cmd.Stderr != nil || cmd.Stdin != nil {
		t.Error("player process was wired to the terminal")
	}
	p.Play("no-such-cue") // unknown cues are ignored, not fatal
}

// newFake builds a player whose "audio command" is `true`, so tests exercise
// the real render/spawn/cleanup path without making noise on the build machine.
func newFake(t *testing.T) *Player {
	t.Helper()
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no `true` binary to stand in for an audio player")
	}
	dir := t.TempDir()
	p := newPlayer(filepath.Join(dir, "clips"), func(string) *exec.Cmd { return exec.Command(bin) })
	if err := os.MkdirAll(p.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range Letters() {
		if !p.render(Letter(i), blipClip(i)) {
			t.Fatal("could not render a letter cue")
		}
	}
	if !p.render(Open, openClip()) {
		t.Fatal("could not render the open cue")
	}
	return p
}
