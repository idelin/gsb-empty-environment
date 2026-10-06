package resample

import (
	"errors"
	"math/big"
)

// ErrBadSegments is returned for an inconsistent segment/switch layout.
var ErrBadSegments = errors.New("resample: need len(switches) == len(segments)-1 and increasing switches")

// Segment is one rate ratio (Num/Den = output rate / input rate) of a
// Switcher timeline.
type Segment struct{ Num, Den int64 }

// Switcher is a streaming resampler whose rate ratio changes at specified
// input-sample indices. The output schedule lives on an absolute rational
// time axis measured in input-sample units: the next output position is an
// exact rational number, and stepping it by the active segment's den/num
// carries the phase across rate switches without any reset.
//
// Output positions are pos_0 = 0, pos_{k+1} = pos_k + step(pos_k), where
// step(pos) is the den/num of the segment active at pos. A stream of N
// input samples yields every output with pos_k < N; interpolation windows
// reaching past the end are completed by repeating the last sample, and an
// empty stream produces no output.
type Switcher struct {
	segs     []Segment  // ratios in lowest terms
	switches []*big.Rat // absolute switch positions; segs[j] applies for pos in [switches[j-1], switches[j])

	pos *big.Rat // absolute position of the next output
	out int64    // outputs emitted so far

	base int64     // absolute input index of buf[0]
	buf  []float64 // pending input samples
	done bool
}

// NewSwitcher builds a Switcher. segments[j] is the rate ratio used while
// the absolute position lies in [switches[j-1], switches[j]) (with
// switches[-1] = 0 and switches[last] = +inf). switches must be strictly
// increasing input-sample indices.
func NewSwitcher(segments []Segment, switches []int64) (*Switcher, error) {
	if len(segments) == 0 || len(switches) != len(segments)-1 {
		return nil, ErrBadSegments
	}
	segs := make([]Segment, len(segments))
	for j, s := range segments {
		if s.Num <= 0 || s.Den <= 0 {
			return nil, ErrNonPositiveRatio
		}
		g := gcd(s.Num, s.Den)
		segs[j] = Segment{Num: s.Num / g, Den: s.Den / g}
	}
	sw := make([]*big.Rat, len(switches))
	for j, at := range switches {
		if j > 0 && at <= switches[j-1] {
			return nil, ErrBadSegments
		}
		sw[j] = big.NewRat(at, 1)
	}
	return &Switcher{segs: segs, switches: sw, pos: new(big.Rat)}, nil
}

// Write appends contiguous input samples and returns newly available
// outputs. Chunk boundaries never affect the result.
func (s *Switcher) Write(p []float64) ([]float64, error) {
	if s.done {
		return nil, ErrWriteAfterFlush
	}
	s.buf = append(s.buf, p...)
	avail := s.base + int64(len(s.buf))
	var out []float64
	for floorRat(s.pos)+2 <= avail {
		out = append(out, s.value(s.pos, avail))
		s.pos.Add(s.pos, s.step(s.pos))
		s.out++
	}
	s.compact()
	return out, nil
}

// Flush ends the stream and emits all remaining outputs (positions < N).
// Missing tail samples are filled by repeating the last sample. Flushing an
// empty stream returns nil. Flush is idempotent.
func (s *Switcher) Flush() ([]float64, error) {
	if s.done {
		return nil, nil
	}
	s.done = true
	n := s.base + int64(len(s.buf))
	if n == 0 {
		return nil, nil
	}
	limit := big.NewRat(n, 1)
	var out []float64
	for s.pos.Cmp(limit) < 0 {
		out = append(out, s.value(s.pos, n))
		s.pos.Add(s.pos, s.step(s.pos))
		s.out++
	}
	return out, nil
}

// State is a checkpoint of a Switcher. It is a deep copy: mutating it never
// affects the Switcher it came from.
type State struct {
	Pos  *big.Rat
	Out  int64
	Base int64
	Buf  []float64
	Done bool
}

// Snapshot captures the full streaming state so processing can be resumed
// later (or replayed) with bit-identical results.
func (s *Switcher) Snapshot() State {
	st := State{
		Pos:  new(big.Rat).Set(s.pos),
		Out:  s.out,
		Base: s.base,
		Done: s.done,
	}
	if len(s.buf) > 0 {
		st.Buf = append([]float64(nil), s.buf...)
	}
	return st
}

// Restore replaces the Switcher's state with a checkpoint previously taken
// with Snapshot. The checkpoint itself is copied, so it can be reused.
func (s *Switcher) Restore(st State) {
	s.pos = new(big.Rat).Set(st.Pos)
	s.out = st.Out
	s.base = st.Base
	s.done = st.Done
	s.buf = append([]float64(nil), st.Buf...)
}

// Buffered reports how many input samples are held internally (constant
// per write, independent of stream length).
func (s *Switcher) Buffered() int { return len(s.buf) }

// OutputCount returns the number of outputs emitted so far.
func (s *Switcher) OutputCount() int64 { return s.out }

// step returns the output step (in input-sample units) active at pos.
func (s *Switcher) step(pos *big.Rat) *big.Rat {
	j := 0
	for j < len(s.switches) && pos.Cmp(s.switches[j]) >= 0 {
		j++
	}
	return big.NewRat(s.segs[j].Den, s.segs[j].Num)
}

// value interpolates the input at absolute rational position pos, clamping
// the window to the last of the n available samples.
func (s *Switcher) value(pos *big.Rat, n int64) float64 {
	i := floorRat(pos)
	frac := new(big.Rat).Sub(pos, big.NewRat(i, 1))
	f, _ := frac.Float64()
	j0 := i
	if j0 > n-1 {
		j0 = n - 1
	}
	j1 := i + 1
	if j1 > n-1 {
		j1 = n - 1
	}
	x0 := s.buf[j0-s.base]
	x1 := s.buf[j1-s.base]
	return x0 + (x1-x0)*f
}

// compact drops buffered samples that no future output can reference.
func (s *Switcher) compact() {
	avail := s.base + int64(len(s.buf))
	if avail == 0 {
		return
	}
	i := floorRat(s.pos)
	if i > avail-1 {
		i = avail - 1
	}
	if i > s.base {
		drop := i - s.base
		copy(s.buf, s.buf[drop:])
		s.buf = s.buf[:len(s.buf)-int(drop)]
		s.base = i
	}
}

// floorRat returns the greatest integer <= r.
func floorRat(r *big.Rat) int64 {
	q := new(big.Int).Quo(r.Num(), r.Denom()) // truncated division
	if r.Sign() < 0 && new(big.Int).Rem(r.Num(), r.Denom()).Sign() != 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q.Int64()
}
