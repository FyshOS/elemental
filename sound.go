package main

import (
	"bytes"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

// Sound names an audible game event. The game model only emits these through a
// callback, so it stays free of any audio dependency (and tests stay silent).
type Sound int

const (
	SoundTap      Sound = iota // a cell is tapped / selected
	SoundInvalid               // an illegal swap is rejected
	SoundMatch                 // a run of cells dissolves
	SoundBurst                 // a compound match (L/T/X or 5+) explodes
	SoundLevelUp               // the difficulty level rises
	SoundGameOver              // the core runs dark
)

// Audio format for the synthesized blips: CD-quality mono, 16-bit signed.
const (
	sampleRate = 44100
	channels   = 1

	// deviceBufferSize caps how much audio the output device keeps queued ahead.
	// Small means immediate feedback - but large enough old hardware won't glitch.
	deviceBufferSize = 30 * time.Millisecond
)

// soundPlayer renders short procedural blips for game events - fittingly, the
// audio is synthesized just like the visuals. It owns a single oto context and a
// pool of in-flight players. If the audio device can't be opened it disables
// itself and every Play becomes a no-op, so the game runs fine without sound.
type soundPlayer struct {
	ctx     *oto.Context
	enabled bool
	muted   atomic.Bool // when set, every Play is a no-op
	pcm     map[Sound][]byte

	mu     sync.Mutex
	active []*oto.Player
}

// SetMuted silences (or unsilences) the player. Safe to call from any goroutine.
func (sp *soundPlayer) SetMuted(muted bool) {
	if sp != nil {
		sp.muted.Store(muted)
	}
}

// Muted reports whether the player is currently silenced.
func (sp *soundPlayer) Muted() bool {
	return sp != nil && sp.muted.Load()
}

// newSoundPlayer opens the audio device and pre-renders every effect. It returns
// a usable (possibly disabled) player and never an error - silence is an
// acceptable degraded mode.
func newSoundPlayer() *soundPlayer {
	sp := &soundPlayer{pcm: buildSounds()}

	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:   sampleRate,
		ChannelCount: channels,
		Format:       oto.FormatSignedInt16LE,
		BufferSize:   deviceBufferSize,
	})
	if err != nil {
		return sp // disabled: no audio device
	}
	sp.ctx = ctx

	// Wait briefly for the device to come up; if it stalls, give up on sound
	// rather than holding the whole game's startup hostage.
	select {
	case <-ready:
		sp.enabled = true
	case <-time.After(2 * time.Second):
	}
	return sp
}

// Play renders the requested effect. Safe to call from any goroutine.
func (sp *soundPlayer) Play(s Sound) {
	if sp == nil || !sp.enabled || sp.muted.Load() {
		return
	}
	buf, ok := sp.pcm[s]
	if !ok {
		return
	}

	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Drop players that have finished so the pool can't grow without bound.
	live := sp.active[:0]
	for _, p := range sp.active {
		if p.IsPlaying() {
			live = append(live, p)
		} else {
			_ = p.Close()
		}
	}
	sp.active = live

	p := sp.ctx.NewPlayer(bytes.NewReader(buf))
	p.Play()
	sp.active = append(sp.active, p)
}

// --- synthesis ---

// segment is one freq-swept tone, rendered as a soft sine with a little harmonic
// body and a gentle vibrato shimmer - an energy hum rather than a retro beep.
type segment struct {
	fStart, fEnd float64 // hertz, linearly swept across the segment
	dur          time.Duration
	vol          float64 // 0..1 peak amplitude
}

// buildSounds pre-renders the PCM for every game event. Tones are airy and ring
// out with a bell-like decay so they feel elemental, not 8-bit.
func buildSounds() map[Sound][]byte {
	return map[Sound][]byte{
		// A tiny soft tick acknowledging a cell tap.
		SoundTap: render(segment{680, 640, 45 * time.Millisecond, 0.14}),
		// A gentle low sigh when a swap makes no match.
		SoundInvalid: render(segment{300, 180, 200 * time.Millisecond, 0.24}),
		// A warm shimmering chime as a run dissolves.
		SoundMatch: render(segment{600, 770, 160 * time.Millisecond, 0.24}),
		// A deep soft swell with a bright bloom for a compound-match explosion.
		SoundBurst: render(
			segment{180, 150, 150 * time.Millisecond, 0.32},
			segment{640, 900, 200 * time.Millisecond, 0.22},
		),
		// An airy rising triad as the difficulty level climbs.
		SoundLevelUp: render(
			segment{523, 523, 130 * time.Millisecond, 0.24},
			segment{659, 659, 130 * time.Millisecond, 0.24},
			segment{784, 784, 240 * time.Millisecond, 0.26},
		),
		// A slow falling pad as the core goes dark.
		SoundGameOver: render(
			segment{392, 392, 240 * time.Millisecond, 0.30},
			segment{294, 294, 240 * time.Millisecond, 0.30},
			segment{196, 196, 240 * time.Millisecond, 0.30},
			segment{131, 131, 520 * time.Millisecond, 0.32},
		),
	}
}

// render synthesizes one or more segments into 16-bit little-endian PCM bytes.
func render(segs ...segment) []byte {
	var out []byte
	for _, s := range segs {
		n := int(float64(sampleRate) * s.dur.Seconds())
		if n <= 0 {
			continue
		}
		attack := sampleRate / 150 // ~7ms fade-in kills the click
		release := sampleRate / 30 // ~33ms soft tail
		if release > n {
			release = n
		}
		phase := 0.0
		for i := 0; i < n; i++ {
			t := float64(i) / float64(n)

			// Subtle vibrato gives the tone an ethereal, energetic shimmer.
			vib := 1.0 + 0.004*math.Sin(2*math.Pi*5.5*float64(i)/float64(sampleRate))
			freq := (s.fStart + (s.fEnd-s.fStart)*t) * vib
			phase += freq / sampleRate
			ang := 2 * math.Pi * phase

			// A soft sine with a touch of upper harmonics for warmth.
			wave := (math.Sin(ang) + 0.22*math.Sin(2*ang) + 0.08*math.Sin(3*ang)) / 1.30

			// Quick attack, bell-like exponential decay, soft release to silence.
			env := math.Exp(-2.2 * t)
			if i < attack {
				env *= float64(i) / float64(attack)
			}
			if i > n-release {
				env *= float64(n-i) / float64(release)
			}

			v := int16(wave * env * s.vol * math.MaxInt16 * 0.9)
			out = append(out, byte(v), byte(v>>8))
		}
	}
	return out
}
