package sound

import (
	"encoding/binary"
	"math"
)

// Everything here is synthesized from oscillators at run time - there are no
// audio assets in the repo and no audio library in go.mod. A chiptune blip is
// a few hundred bytes of arithmetic, and keeping it that way is what lets the
// release stay CGO_ENABLED=0 and cross-compile to four platforms.

const (
	sampleRate = 44100
	bitDepth   = 16
	channels   = 1
)

type waveform int

const (
	square waveform = iota
	triangle
	sine
)

// note is one voice of a clip: a tone starting at `at`, lasting `dur`, and
// optionally gliding from freq to sweepTo. Times are in seconds.
type note struct {
	at      float64
	dur     float64
	freq    float64
	sweepTo float64 // 0 = hold freq
	wave    waveform
	gain    float64
}

func osc(w waveform, phase float64) float64 {
	switch w {
	case triangle:
		return 4*math.Abs(phase-0.5) - 1
	case sine:
		return math.Sin(2 * math.Pi * phase)
	default: // square, 50% duty - the classic arcade blip
		if phase < 0.5 {
			return 1
		}
		return -1
	}
}

// envelope shapes a note into a plucked chiptune hit: near-instant attack, then
// an exponential decay. The tail is forced to zero so a clip never ends
// mid-waveform, which is what a click actually is.
func envelope(elapsed, dur float64) float64 {
	if elapsed < 0 || elapsed > dur {
		return 0
	}
	const attack, release = 0.003, 0.008
	e := math.Exp(-3.5 * elapsed / dur)
	if elapsed < attack {
		e *= elapsed / attack
	}
	if rem := dur - elapsed; rem < release {
		e *= rem / release
	}
	return e
}

// render mixes notes into 16-bit mono PCM.
func render(notes []note) []int16 {
	total := 0.0
	for _, n := range notes {
		if end := n.at + n.dur; end > total {
			total = end
		}
	}
	buf := make([]float64, int(total*sampleRate)+1)

	for _, n := range notes {
		phase := 0.0
		start := int(n.at * sampleRate)
		for i := 0; i < int(n.dur*sampleRate); i++ {
			idx := start + i
			if idx >= len(buf) {
				break
			}
			elapsed := float64(i) / sampleRate
			buf[idx] += osc(n.wave, phase) * envelope(elapsed, n.dur) * n.gain

			f := n.freq
			if n.sweepTo > 0 {
				f += (n.sweepTo - n.freq) * (elapsed / n.dur)
			}
			phase += f / sampleRate
			phase -= math.Floor(phase)
		}
	}

	out := make([]int16, len(buf))
	for i, v := range buf {
		out[i] = int16(math.Round(math.Max(-1, math.Min(1, v)) * 32000))
	}
	return out
}

// wav wraps PCM samples in a 44-byte RIFF header - the one container every
// system audio player reads without argument.
func wav(samples []int16) []byte {
	data := len(samples) * bitDepth / 8
	b := make([]byte, 0, 44+data)
	put32 := func(v uint32) { b = binary.LittleEndian.AppendUint32(b, v) }
	put16 := func(v uint16) { b = binary.LittleEndian.AppendUint16(b, v) }

	b = append(b, "RIFF"...)
	put32(uint32(36 + data))
	b = append(b, "WAVEfmt "...)
	put32(16)                                   // fmt chunk size
	put16(1)                                    // PCM, uncompressed
	put16(channels)                             //
	put32(sampleRate)                           //
	put32(sampleRate * channels * bitDepth / 8) // byte rate
	put16(uint16(channels * bitDepth / 8))      // block align
	put16(bitDepth)                             //
	b = append(b, "data"...)                    //
	put32(uint32(data))                         //
	for _, s := range samples {
		put16(uint16(s))
	}
	return b
}

// ---- the two cues ----------------------------------------------------------

// semitone steps of a major pentatonic scale: one note per sounded letter of
// "APPLE PIE", so the wordmark plays a rising phrase as it fills in rather
// than the same beep eight times.
var pentatonic = []int{0, 2, 4, 7, 9, 12, 14, 16}

const rootHz = 523.25 // C5

func step(semitones float64) float64 { return rootHz * math.Pow(2, semitones/12) }

// blipClip is one letter landing: a short square-wave pluck with a quiet
// octave above it for sparkle.
func blipClip(i int) []int16 {
	f := step(float64(pentatonic[i%len(pentatonic)]))
	return render([]note{
		{at: 0, dur: 0.10, freq: f, wave: square, gain: 0.55},
		{at: 0, dur: 0.07, freq: f * 2, wave: square, gain: 0.12},
	})
}

// openClip is the pie parting: a major arpeggio that resolves into a ringing
// chord, over a low sweep rising as the slices slide outward.
func openClip() []int16 {
	root, third, fifth, oct := step(0), step(4), step(7), step(12)
	return render([]note{
		{at: 0.00, dur: 0.18, freq: root, wave: square, gain: 0.30},
		{at: 0.08, dur: 0.18, freq: third, wave: square, gain: 0.30},
		{at: 0.16, dur: 0.18, freq: fifth, wave: square, gain: 0.30},
		{at: 0.24, dur: 0.90, freq: oct, wave: triangle, gain: 0.40},
		{at: 0.24, dur: 0.90, freq: fifth, wave: triangle, gain: 0.26},
		{at: 0.24, dur: 0.90, freq: root, wave: triangle, gain: 0.26},
		{at: 0.00, dur: 0.55, freq: 180, sweepTo: 520, wave: sine, gain: 0.35},
	})
}
