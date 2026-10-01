// Package pktscale transforms packet record streams by rational time and
// byte rates, preserving per-stream causality, fragmenting packets that
// exceed the MTU, and bounding rounding drift by accumulating residuals
// in exact rational arithmetic. It also contains a discrete-event link
// simulator (integer ticks, no real network traffic).
package pktscale

import (
	"errors"
	"fmt"
	"math/big"
)

var half = big.NewRat(1, 2)

// roundHalfUp rounds a non-negative rational to the nearest integer,
// halves rounding up.
func roundHalfUp(x *big.Rat) int64 {
	t := new(big.Rat).Add(x, half)
	q := new(big.Int).Quo(t.Num(), t.Denom()) // truncation == floor for x >= 0
	return q.Int64()
}

// Record is one input packet record. Time is an integer input tick.
// Seq is the declared global order of records and is used as the
// tie-breaker when merging streams.
type Record struct {
	Seq    int64
	Stream int
	Time   int64
	Size   int64
}

// Fragment is one scheduled output fragment. Fragments of one packet
// share PacketID and are ordered by FragID.
type Fragment struct {
	Seq      int64
	Stream   int
	Tick     int64
	Size     int64
	FragID   int
	FragCnt  int
	PacketID int64
}

// streamState is the recoverable per-stream scaling state. All exact
// positions are kept as rationals so a checkpoint round-trip is lossless.
type streamState struct {
	lastIn   int64    // last input tick seen (monotonicity check)
	byteAcc  *big.Rat // exact cumulative scaled bytes
	byteEmit int64    // cumulative emitted (integer) bytes
	nextMin  int64    // earliest tick the next packet may use
	pktID    int64
	residual *big.Rat // emitted minus exact position of last packet (bounded)
}

// Scaler applies rational time/byte rates to record streams.
type Scaler struct {
	timeRate *big.Rat
	byteRate *big.Rat
	mtu      int64
	fragGap  int64 // tick spacing between fragments of one packet

	// Global input->output timeline mapping: out(t) = gBaseOut +
	// (t-gBaseIn)*timeRate. Rate changes advance the base exactly.
	gBaseIn  int64
	gBaseOut *big.Rat

	streams map[int]*streamState
}

// NewScaler builds a Scaler. A zero (or negative) rate is rejected.
// fragGap <= 0 defaults to 1 tick between fragments.
func NewScaler(timeRate, byteRate *big.Rat, mtu, fragGap int64) (*Scaler, error) {
	if timeRate == nil || timeRate.Sign() <= 0 {
		return nil, errors.New("pktscale: time rate must be a positive rational (rate 0 rejected)")
	}
	if byteRate == nil || byteRate.Sign() <= 0 {
		return nil, errors.New("pktscale: byte rate must be a positive rational (rate 0 rejected)")
	}
	if mtu <= 0 {
		return nil, errors.New("pktscale: MTU must be positive")
	}
	if fragGap <= 0 {
		fragGap = 1
	}
	return &Scaler{
		timeRate: new(big.Rat).Set(timeRate),
		byteRate: new(big.Rat).Set(byteRate),
		mtu:      mtu,
		fragGap:  fragGap,
		gBaseOut: new(big.Rat),
		streams:  make(map[int]*streamState),
	}, nil
}

// ExactTime maps an input tick to the exact (rational) output position.
func (s *Scaler) ExactTime(t int64) *big.Rat {
	d := big.NewRat(t-s.gBaseIn, 1)
	return new(big.Rat).Add(s.gBaseOut, d.Mul(d, s.timeRate))
}

// SetTimeRate changes the time rate at input tick at. Positions before
// at are unaffected; the mapping stays continuous at the change point.
func (s *Scaler) SetTimeRate(rate *big.Rat, at int64) error {
	if rate == nil || rate.Sign() <= 0 {
		return errors.New("pktscale: time rate must be a positive rational (rate 0 rejected)")
	}
	if at < s.gBaseIn {
		return fmt.Errorf("pktscale: rate change at %d is before current base %d", at, s.gBaseIn)
	}
	d := big.NewRat(at-s.gBaseIn, 1)
	s.gBaseOut.Add(s.gBaseOut, d.Mul(d, s.timeRate))
	s.gBaseIn = at
	s.timeRate = new(big.Rat).Set(rate)
	return nil
}

// SetByteRate changes the byte rate for subsequently processed records.
// Byte accumulation is incremental, so no rebase is needed.
func (s *Scaler) SetByteRate(rate *big.Rat) error {
	if rate == nil || rate.Sign() <= 0 {
		return errors.New("pktscale: byte rate must be a positive rational (rate 0 rejected)")
	}
	s.byteRate = new(big.Rat).Set(rate)
	return nil
}

// Process scales one record and returns its scheduled fragments.
// Records of one stream must arrive with non-decreasing Time.
func (s *Scaler) Process(rec Record) ([]Fragment, error) {
	if rec.Size <= 0 {
		return nil, fmt.Errorf("pktscale: record %d has non-positive size %d", rec.Seq, rec.Size)
	}
	st := s.streams[rec.Stream]
	if st == nil {
		st = &streamState{byteAcc: new(big.Rat), residual: new(big.Rat)}
		s.streams[rec.Stream] = st
	}
	if rec.Time < st.lastIn {
		return nil, fmt.Errorf("pktscale: stream %d time %d goes backwards (last %d)",
			rec.Stream, rec.Time, st.lastIn)
	}
	st.lastIn = rec.Time

	// Time axis: round the exact cumulative position, then clamp to the
	// causality floor (fragment tail of the previous packet). Because the
	// exact position is used (not an increment), residuals never
	// accumulate: |emit-exact| <= 1/2 before clamping.
	exact := s.ExactTime(rec.Time)
	emit := roundHalfUp(exact)
	if emit < st.nextMin {
		emit = st.nextMin
	}
	st.residual.Sub(big.NewRat(emit, 1), exact)

	// Byte axis: error-diffused rounding of the cumulative scaled size,
	// so per-packet rounding residuals cancel out over a stream.
	st.byteAcc.Add(st.byteAcc, new(big.Rat).Mul(big.NewRat(rec.Size, 1), s.byteRate))
	sz := roundHalfUp(st.byteAcc) - st.byteEmit
	if sz < 1 {
		sz = 1 // keep packets representable
	}
	st.byteEmit += sz

	// Fragmentation: oversized packets are split; fragment k is emitted
	// fragGap ticks after fragment k-1, and the next packet of the stream
	// may not start before the last fragment (joint causality constraint).
	n := (sz + s.mtu - 1) / s.mtu
	frags := make([]Fragment, 0, n)
	rem := sz
	pid := st.pktID
	st.pktID++
	for k := int64(0); k < n; k++ {
		fs := s.mtu
		if rem < fs {
			fs = rem
		}
		frags = append(frags, Fragment{
			Seq:      rec.Seq,
			Stream:   rec.Stream,
			Tick:     emit + k*s.fragGap,
			Size:     fs,
			FragID:   int(k),
			FragCnt:  int(n),
			PacketID: pid,
		})
		rem -= fs
	}
	st.nextMin = emit + (n-1)*s.fragGap
	return frags, nil
}

// Residual returns the current rounding residual (emitted minus exact
// output position) of a stream; it stays within [-1/2, 1/2] plus any
// causality clamping.
func (s *Scaler) Residual(stream int) *big.Rat {
	st := s.streams[stream]
	if st == nil {
		return new(big.Rat)
	}
	return new(big.Rat).Set(st.residual)
}
