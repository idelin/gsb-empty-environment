// Package mixer accumulates integer PCM segments placed at arbitrary time
// offsets into a multi-channel mix. Each segment is scaled by a rational
// gain and mapped onto output channels. All contributions are accumulated
// exactly (big-integer arithmetic over a shared denominator); conversion to
// integer output happens exactly once at read time using round-half-to-even
// followed by saturation. Intermediate (per-track) values are never
// saturated, so overlapping segments may cancel each other losslessly.
package mixer

import (
	"errors"
	"fmt"
	"math/big"
)

// Range identifies a half-open interval [Start, End) of sample positions on
// a set of channels. It describes the region invalidated by an Add/Remove.
type Range struct {
	Channels []int
	Start    int64
	End      int64 // exclusive
}

type cell struct {
	ch  int
	pos int64
}

type segment struct {
	pcm    [][]int64 // source channels, all of equal length
	offset int64
	chMap  []int // chMap[i] = destination channel of source channel i
	num    *big.Int
	den    *big.Int // > 0
}

// Mixer is an exact accumulator for integer PCM segments.
type Mixer struct {
	channels int
	min      int64
	max      int64

	den *big.Int // shared denominator: LCM of every gain denominator added so far
	acc map[cell]*big.Int

	segments map[string]*segment
}

// New creates a Mixer with the given number of output channels. Output
// samples are saturated to [min, max] (e.g. -32768, 32767 for int16).
func New(channels int, min, max int64) (*Mixer, error) {
	if channels <= 0 {
		return nil, errors.New("mixer: channels must be positive")
	}
	if min > max {
		return nil, errors.New("mixer: min must be <= max")
	}
	return &Mixer{
		channels: channels,
		min:      min,
		max:      max,
		den:      big.NewInt(1),
		acc:      make(map[cell]*big.Int),
		segments: make(map[string]*segment),
	}, nil
}

// Channels returns the number of output channels.
func (m *Mixer) Channels() int { return m.channels }

// Add mixes pcm (one slice per source channel, all of equal length) into the
// output starting at sample offset (which may be negative). chMap[i] selects
// the output channel for source channel i; pass nil for identity mapping.
// The segment is scaled by the rational gain num/den (den != 0; the gain may
// be negative). The id must be unique and is later used by Remove.
//
// The returned Range is the region of the mix affected by this call.
func (m *Mixer) Add(id string, pcm [][]int64, offset int64, chMap []int, num, den int64) (Range, error) {
	if _, exists := m.segments[id]; exists {
		return Range{}, fmt.Errorf("mixer: duplicate segment id %q", id)
	}
	if den == 0 {
		return Range{}, errors.New("mixer: gain denominator must be non-zero")
	}
	if len(pcm) == 0 {
		return Range{}, errors.New("mixer: segment has no source channels")
	}
	length := len(pcm[0])
	for _, ch := range pcm {
		if len(ch) != length {
			return Range{}, errors.New("mixer: source channels differ in length")
		}
	}
	if chMap == nil {
		chMap = make([]int, len(pcm))
		for i := range chMap {
			chMap[i] = i
		}
	}
	if len(chMap) != len(pcm) {
		return Range{}, errors.New("mixer: channel map length mismatch")
	}
	for _, c := range chMap {
		if c < 0 || c >= m.channels {
			return Range{}, fmt.Errorf("mixer: channel %d out of range", c)
		}
	}

	gn := big.NewInt(num)
	gd := big.NewInt(den)
	if gd.Sign() < 0 {
		gn.Neg(gn)
		gd.Neg(gd)
	}

	// Grow the shared denominator to a multiple of this segment's
	// denominator and rescale existing accumulators to stay exact.
	if new(big.Int).Mod(m.den, gd).Sign() != 0 {
		factor := new(big.Int).Quo(gd, new(big.Int).GCD(nil, nil, m.den, gd))
		for _, v := range m.acc {
			v.Mul(v, factor)
		}
		m.den.Mul(m.den, factor)
	}

	seg := &segment{pcm: pcm, offset: offset, chMap: chMap, num: gn, den: gd}
	m.accumulate(seg, 1)
	m.segments[id] = seg

	channels := make([]int, len(chMap))
	copy(channels, chMap)
	return Range{Channels: channels, Start: offset, End: offset + int64(length)}, nil
}

// Remove withdraws the segment previously added under id and returns the
// Range of the mix that had to be recomputed. The second return value
// reports whether the segment existed.
func (m *Mixer) Remove(id string) (Range, bool) {
	seg, ok := m.segments[id]
	if !ok {
		return Range{}, false
	}
	delete(m.segments, id)
	m.accumulate(seg, -1)
	channels := make([]int, len(seg.chMap))
	copy(channels, seg.chMap)
	return Range{
		Channels: channels,
		Start:    seg.offset,
		End:      seg.offset + int64(len(seg.pcm[0])),
	}, true
}

// accumulate adds (sign=1) or subtracts (sign=-1) a segment's exact
// contribution from the shared accumulators.
func (m *Mixer) accumulate(seg *segment, sign int) {
	// multiplier = num * (sharedDen / segDen); exact because sharedDen is a
	// multiple of segDen.
	mult := new(big.Int).Quo(m.den, seg.den)
	mult.Mul(mult, seg.num)
	if sign < 0 {
		mult.Neg(mult)
	}
	if mult.Sign() == 0 {
		return
	}
	tmp := new(big.Int)
	for src, dst := range seg.chMap {
		for i, s := range seg.pcm[src] {
			if s == 0 {
				continue
			}
			k := cell{ch: dst, pos: seg.offset + int64(i)}
			v := m.acc[k]
			if v == nil {
				v = new(big.Int)
				m.acc[k] = v
			}
			v.Add(v, tmp.Mul(mult, big.NewInt(s)))
			if v.Sign() == 0 {
				delete(m.acc, k)
			}
		}
	}
}

// Sample returns the mixed value at (ch, pos): the exact accumulated sum is
// rounded once with round-half-to-even and then saturated to [min, max].
func (m *Mixer) Sample(ch int, pos int64) int64 {
	if ch < 0 || ch >= m.channels {
		return 0
	}
	v := m.acc[cell{ch: ch, pos: pos}]
	if v == nil {
		return 0
	}
	return clamp(roundHalfEven(v, m.den), m.min, m.max)
}

// Samples returns n consecutive mixed samples on channel ch starting at pos.
func (m *Mixer) Samples(ch int, pos int64, n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = m.Sample(ch, pos+int64(i))
	}
	return out
}

// roundHalfEven rounds the rational n/d (d > 0) to the nearest integer;
// exact halves go to the even neighbour.
func roundHalfEven(n, d *big.Int) int64 {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, d, r) // truncated toward zero; r carries n's sign
	twice := new(big.Int).Abs(r)
	twice.Lsh(twice, 1)
	switch cmp := twice.Cmp(d); {
	case cmp > 0, cmp == 0 && q.Bit(0) == 1:
		if n.Sign() >= 0 {
			q.Add(q, bigOne)
		} else {
			q.Sub(q, bigOne)
		}
	}
	if !q.IsInt64() {
		if q.Sign() < 0 {
			return minInt64
		}
		return maxInt64
	}
	return q.Int64()
}

var bigOne = big.NewInt(1)

const (
	maxInt64 = int64(^uint64(0) >> 1)
	minInt64 = -maxInt64 - 1
)

func clamp(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
