package tsxform

import (
	"fmt"
	"math/big"
)

// Packet is one input record of a stream.
type Packet struct {
	Stream int
	Time   int64 // input tick, non-decreasing per stream
	Size   int64 // bytes
}

// Fragment is a scaled output unit: a whole packet or one MTU-sized piece
// of it. IdealTick is the exact-rate scaled departure tick (before link
// queueing). Seq is the global declared order of the parent packet.
type Fragment struct {
	Seq       int64
	PacketID  int64
	Stream    int
	IdealTick int64
	Size      int64
	FragIndex int
	FragCount int
}

// timeSeg is one segment of the piecewise-linear exact time map. Anchor is
// the exact (rational) scaled position of tick At.
type timeSeg struct {
	At     int64
	Rate   Ratio
	Anchor *big.Rat
}

// streamState carries the per-stream rounding residuals. Keeping the exact
// rational position and the cumulative byte floor is what makes rounding
// residual compensation exact: drift stays below one tick / one byte
// forever instead of accumulating.
type streamState struct {
	lastIn       int64
	hasInput     bool
	lastTick     int64
	cumBytes     int64
	byteBaseCum  int64
	byteBaseEmit int64
	emitted      int64
	byteRate     Ratio
}

// Scaler transforms packets by a rational time rate and a rational byte
// rate, fragmenting results that exceed the MTU.
type Scaler struct {
	mtu      int64
	segs     []timeSeg
	byteRate Ratio
	streams  map[int]*streamState
	nextSeq  int64
	nextPkt  int64

	// Clamped counts packets whose scaled size rounded to zero and was
	// raised to the 1-byte minimum.
	Clamped int64
}

// NewScaler builds a scaler with the given MTU and initial rates.
func NewScaler(mtu int64, timeRate, byteRate Ratio) (*Scaler, error) {
	if mtu <= 0 {
		return nil, fmt.Errorf("scaler: MTU must be positive, got %d", mtu)
	}
	s := &Scaler{
		mtu:      mtu,
		byteRate: byteRate,
		streams:  map[int]*streamState{},
	}
	s.segs = append(s.segs, timeSeg{At: 0, Rate: timeRate, Anchor: new(big.Rat)})
	return s, nil
}

// posAt evaluates the exact scaled position of input tick t.
func (s *Scaler) posAt(t int64) *big.Rat {
	seg := s.segs[0]
	for _, sg := range s.segs {
		if sg.At <= t {
			seg = sg
		} else {
			break
		}
	}
	d := new(big.Rat).SetInt64(t - seg.At)
	d.Mul(d, new(big.Rat).SetFrac64(seg.Rate.Num, seg.Rate.Den))
	return d.Add(d, seg.Anchor)
}

// SetTimeRate changes the time rate at input tick at, keeping the exact
// scaled position continuous across the change point.
func (s *Scaler) SetTimeRate(r Ratio, at int64) error {
	last := s.segs[len(s.segs)-1]
	if at < last.At {
		return fmt.Errorf("scaler: rate change at %d precedes last change at %d", at, last.At)
	}
	if at == last.At {
		s.segs[len(s.segs)-1].Rate = r
		return nil
	}
	s.segs = append(s.segs, timeSeg{At: at, Rate: r, Anchor: s.posAt(at)})
	return nil
}

// SetByteRate changes the byte rate; cumulative emitted bytes stay
// continuous across the change.
func (s *Scaler) SetByteRate(r Ratio) {
	s.byteRate = r
	for _, st := range s.streams {
		st.byteBaseCum = st.cumBytes
		st.byteBaseEmit = st.emitted
		st.byteRate = r
	}
}

// floorRat returns floor(r) for a non-negative rational.
func floorRat(r *big.Rat) int64 {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	if rem.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q.Int64()
}

// scaleBytes returns floor(cum * r) exactly.
func scaleBytes(cum int64, r Ratio) int64 {
	v := new(big.Int).Mul(big.NewInt(cum), big.NewInt(r.Num))
	v.Quo(v, big.NewInt(r.Den))
	return v.Int64()
}

// Emit scales one packet and returns its fragments in order.
func (s *Scaler) Emit(p Packet) ([]Fragment, error) {
	if p.Size <= 0 {
		return nil, fmt.Errorf("scaler: packet size must be positive, got %d", p.Size)
	}
	if p.Time < 0 {
		return nil, fmt.Errorf("scaler: negative input tick %d", p.Time)
	}
	st := s.streams[p.Stream]
	if st == nil {
		st = &streamState{byteRate: s.byteRate}
		s.streams[p.Stream] = st
	}
	if st.hasInput && p.Time < st.lastIn {
		return nil, fmt.Errorf("scaler: stream %d input time went backwards (%d < %d)", p.Stream, p.Time, st.lastIn)
	}
	st.lastIn = p.Time
	st.hasInput = true

	tick := floorRat(s.posAt(p.Time))
	if tick < st.lastTick {
		tick = st.lastTick
	}
	st.lastTick = tick

	st.cumBytes += p.Size
	target := st.byteBaseEmit + scaleBytes(st.cumBytes-st.byteBaseCum, st.byteRate)
	sz := target - st.emitted
	if sz < 1 {
		sz = 1
		s.Clamped++
	}
	st.emitted += sz

	count := int((sz + s.mtu - 1) / s.mtu)
	frags := make([]Fragment, 0, count)
	rem := sz
	for i := 0; i < count; i++ {
		fsz := s.mtu
		if rem < fsz {
			fsz = rem
		}
		frags = append(frags, Fragment{
			Seq:       s.nextSeq,
			PacketID:  s.nextPkt,
			Stream:    p.Stream,
			IdealTick: tick,
			Size:      fsz,
			FragIndex: i,
			FragCount: count,
		})
		rem -= fsz
	}
	s.nextSeq++
	s.nextPkt++
	return frags, nil
}
