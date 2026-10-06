// Package pcmix mixes integer PCM fragments placed at arbitrary (possibly
// negative) frame offsets. Contributions are accumulated exactly as
// rationals (sample * num / den) in big integers; rounding (half to even)
// and saturation happen exactly once, at render time. Individual tracks are
// never saturated before mixing.
package pcmix

import (
	"errors"
	"fmt"
	"math/big"
)

// Gain is a rational gain factor Num/Den. Den must be non-zero; the sign is
// normalized so that Den > 0.
type Gain struct {
	Num int64
	Den int64
}

func (g Gain) normalized() (Gain, error) {
	if g.Den == 0 {
		return Gain{}, errors.New("pcmix: gain denominator is zero")
	}
	if g.Den < 0 {
		g.Num = -g.Num
		g.Den = -g.Den
	}
	return g, nil
}

type fragment struct {
	offset     int64
	channelMap []int
	gain       Gain
	samples    []int32 // interleaved by fragment channel
}

func (f *fragment) frames() int64 {
	if len(f.channelMap) == 0 {
		return 0
	}
	return int64(len(f.samples) / len(f.channelMap))
}

// Mixer accumulates PCM fragments into channels with exact rational
// arithmetic. It is not safe for concurrent use.
type Mixer struct {
	channels int
	min      int64 // saturation bounds applied once at render
	max      int64
	acc      []map[int64]*big.Rat // per channel: frame -> exact mixed value
	frags    map[string]*fragment
}

// New creates a Mixer with the given number of output channels. Output
// samples are saturated to the signed bitDepth range (e.g. 16 gives
// [-32768, 32767]). bitDepth must be in [2, 32].
func New(channels int, bitDepth uint) (*Mixer, error) {
	if channels <= 0 {
		return nil, errors.New("pcmix: channels must be positive")
	}
	if bitDepth < 2 || bitDepth > 32 {
		return nil, errors.New("pcmix: bitDepth must be in [2, 32]")
	}
	max := int64(1)<<(bitDepth-1) - 1
	min := -int64(1) << (bitDepth - 1)
	m := &Mixer{
		channels: channels,
		min:      min,
		max:      max,
		acc:      make([]map[int64]*big.Rat, channels),
		frags:    make(map[string]*fragment),
	}
	for ch := range m.acc {
		m.acc[ch] = make(map[int64]*big.Rat)
	}
	return m, nil
}

// Add mixes a fragment into the accumulator. samples are interleaved by
// fragment channel; channelMap[i] is the mixer channel that fragment
// channel i feeds, so len(samples) must be a multiple of len(channelMap).
// offset is the frame index (may be negative) of samples[0] in the mix
// timeline. Adding with an existing id replaces the previous fragment.
func (m *Mixer) Add(id string, offset int64, channelMap []int, gain Gain, samples []int32) error {
	if len(channelMap) == 0 {
		return errors.New("pcmix: empty channel map")
	}
	for _, ch := range channelMap {
		if ch < 0 || ch >= m.channels {
			return fmt.Errorf("pcmix: channel map entry %d out of range [0,%d)", ch, m.channels)
		}
	}
	if len(samples)%len(channelMap) != 0 {
		return errors.New("pcmix: sample count is not a multiple of the fragment channel count")
	}
	g, err := gain.normalized()
	if err != nil {
		return err
	}
	if _, exists := m.frags[id]; exists {
		m.Remove(id)
	}
	f := &fragment{
		offset:     offset,
		channelMap: append([]int(nil), channelMap...),
		gain:       g,
		samples:    append([]int32(nil), samples...),
	}
	m.apply(f, 1)
	m.frags[id] = f
	return nil
}

// Remove withdraws a fragment and returns the half-open frame range
// [start, end) whose mixed values changed, so callers can re-render only
// the affected region. ok is false if the id is unknown.
func (m *Mixer) Remove(id string) (start, end int64, ok bool) {
	f, exists := m.frags[id]
	if !exists {
		return 0, 0, false
	}
	delete(m.frags, id)
	m.apply(f, -1)
	return f.offset, f.offset + f.frames(), true
}

// apply adds (sign=+1) or subtracts (sign=-1) a fragment's exact rational
// contribution from the accumulator.
func (m *Mixer) apply(f *fragment, sign int64) {
	nch := len(f.channelMap)
	num := f.gain.Num * sign
	delta := new(big.Rat)
	for i, s := range f.samples {
		if s == 0 || num == 0 {
			continue
		}
		ch := f.channelMap[i%nch]
		frame := f.offset + int64(i/nch)
		delta.SetInt64(int64(s) * num)
		if f.gain.Den != 1 {
			delta.Quo(delta, new(big.Rat).SetInt64(f.gain.Den))
		}
		cell := m.acc[ch][frame]
		if cell == nil {
			m.acc[ch][frame] = new(big.Rat).Set(delta)
			continue
		}
		cell.Add(cell, delta)
		if cell.Sign() == 0 {
			delete(m.acc[ch], frame)
		}
	}
}

// Range returns the half-open frame span [start, end) currently covered by
// any fragment. ok is false when the mixer is empty.
func (m *Mixer) Range() (start, end int64, ok bool) {
	first := true
	for _, f := range m.frags {
		s, e := f.offset, f.offset+f.frames()
		if first || s < start {
			start = s
		}
		if first || e > end {
			end = e
		}
		first = false
	}
	return start, end, !first
}

// Render returns the mixed output for frames [start, end) as
// out[channel][frame-start]. Each accumulated rational is rounded once
// (half to even) and then saturated to the configured bit depth.
func (m *Mixer) Render(start, end int64) [][]int32 {
	if end < start {
		end = start
	}
	n := end - start
	out := make([][]int32, m.channels)
	for ch := 0; ch < m.channels; ch++ {
		row := make([]int32, n)
		for frame := start; frame < end; frame++ {
			cell := m.acc[ch][frame]
			if cell == nil {
				continue
			}
			row[frame-start] = m.saturate(roundHalfEven(cell))
		}
		out[ch] = row
	}
	return out
}

// roundHalfEven rounds r to the nearest integer; ties go to the even
// neighbour.
func roundHalfEven(r *big.Rat) *big.Int {
	num := r.Num()
	den := r.Denom()
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(num, den, rem) // truncated toward zero; rem has num's sign
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	cmp := twice.Cmp(den)
	away := cmp > 0 || (cmp == 0 && q.Bit(0) == 1)
	if away {
		if num.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return q
}

func (m *Mixer) saturate(v *big.Int) int32 {
	if !v.IsInt64() {
		if v.Sign() < 0 {
			return int32(m.min)
		}
		return int32(m.max)
	}
	x := v.Int64()
	if x < m.min {
		return int32(m.min)
	}
	if x > m.max {
		return int32(m.max)
	}
	return int32(x)
}
