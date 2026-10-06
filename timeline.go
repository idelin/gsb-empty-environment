package resample

import "math/big"

// Rate is an exact rational sample rate in samples per unit time.
type Rate struct {
	Num int64
	Den int64
}

func (r Rate) spacing() *big.Rat { return big.NewRat(r.Den, r.Num) }

func (r Rate) valid() bool { return r.Num > 0 && r.Den > 0 }

// InputSegment declares the input sample rate taking effect at an absolute
// input index. The first segment must have FromIndex == 0. Sample times are
// continuous across the switch: the sample at FromIndex sits exactly where
// the previous segment's grid placed it, and the new spacing applies from
// there on.
type InputSegment struct {
	FromIndex int64
	Rate      Rate
}

// segment is InputSegment with the absolute start time precomputed.
type segment struct {
	fromIndex int64
	startTime *big.Rat
	spacing   *big.Rat
}

// Timeline is a streaming resampler on an absolute rational time axis.
//
// Input sample times are piecewise-uniform, switching rate at the segment
// boundaries. Output sample n sits at absolute time n/outRate. The output
// phase is a single exact rational carried across Write calls and across
// rate switches, so phase passes through switches continuously in absolute
// time. Outputs are emitted while their time is below the (virtual) time of
// the sample one past the end; outputs past the last real sample use the
// last sample repeated. Empty input produces no output.
//
// At most two input samples are retained internally, independent of stream
// length.
type Timeline struct {
	outStep *big.Rat
	segs    []segment

	nextOut  *big.Rat // absolute time of the next output sample
	outCount int64
	inCount  int64

	prevT   *big.Rat
	prevV   float64
	hasPrev bool
	lastT   *big.Rat
	lastV   float64
	hasLast bool

	flushed bool
}

// NewTimeline builds a Timeline with the given output rate and input rate
// segments. It panics on invalid rates or unsorted/unaligned segments.
func NewTimeline(outRate Rate, segs ...InputSegment) *Timeline {
	if !outRate.valid() {
		panic("resample: output rate must be positive")
	}
	t := &Timeline{
		outStep: outRate.spacing(),
		nextOut: new(big.Rat),
	}
	t.segs = buildSegments(segs)
	return t
}

func buildSegments(segs []InputSegment) []segment {
	if len(segs) == 0 || segs[0].FromIndex != 0 {
		panic("resample: first input segment must start at index 0")
	}
	out := make([]segment, len(segs))
	for i, s := range segs {
		if !s.Rate.valid() {
			panic("resample: input rate must be positive")
		}
		if i > 0 && s.FromIndex <= segs[i-1].FromIndex {
			panic("resample: input segments must be strictly increasing")
		}
		out[i] = segment{fromIndex: s.FromIndex, spacing: s.Rate.spacing()}
	}
	out[0].startTime = new(big.Rat)
	for i := 1; i < len(out); i++ {
		prev := &out[i-1]
		span := new(big.Rat).SetInt64(out[i].fromIndex - prev.fromIndex)
		span.Mul(span, prev.spacing)
		out[i].startTime = new(big.Rat).Add(prev.startTime, span)
	}
	return out
}

// segmentOf returns the index of the segment containing input sample i.
func (t *Timeline) segmentOf(i int64) int {
	k := 0
	for j := 1; j < len(t.segs); j++ {
		if t.segs[j].fromIndex > i {
			break
		}
		k = j
	}
	return k
}

// inTime returns the absolute time of input sample i.
func (t *Timeline) inTime(i int64) *big.Rat {
	s := &t.segs[t.segmentOf(i)]
	d := new(big.Rat).SetInt64(i - s.fromIndex)
	d.Mul(d, s.spacing)
	return d.Add(d, s.startTime)
}

// Write consumes the next contiguous input samples (e.g. the confirmed
// prefix released by a Sequencer) and returns newly determined outputs.
func (t *Timeline) Write(xs []float64) []float64 {
	if t.flushed {
		panic("resample: Write after Flush")
	}
	var out []float64
	for _, v := range xs {
		tt := t.inTime(t.inCount)
		t.inCount++
		t.prevT, t.prevV, t.hasPrev = t.lastT, t.lastV, t.hasLast
		t.lastT, t.lastV, t.hasLast = tt, v, true
		// Emit every output at or before the newest sample time: its
		// bracketing pair (prev, last) is fully available.
		for t.nextOut.Cmp(tt) <= 0 {
			out = append(out, t.interpolate())
			t.outCount++
			t.nextOut.Add(t.nextOut, t.outStep)
		}
	}
	return out
}

// interpolate evaluates the output at nextOut from the pair (prev, last).
// Callers guarantee prevT < nextOut <= lastT, or that nextOut == lastT == 0
// for the very first sample.
func (t *Timeline) interpolate() float64 {
	if !t.hasPrev {
		return t.lastV
	}
	num := new(big.Rat).Sub(t.nextOut, t.prevT)
	den := new(big.Rat).Sub(t.lastT, t.prevT)
	frac, _ := num.Quo(num, den).Float64()
	return t.prevV + frac*(t.lastV-t.prevV)
}

// Flush ends the stream and emits the remaining tail outputs, repeating the
// last sample where interpolation would run past the end. Empty input
// yields no output. Flush is idempotent.
func (t *Timeline) Flush() []float64 {
	if t.flushed {
		return nil
	}
	t.flushed = true
	if !t.hasLast {
		return nil
	}
	// Virtual end: the time the next input sample would have had. Outputs
	// strictly before it exist; all of them lie past lastT (Write drained
	// the rest), so they repeat the last sample.
	end := new(big.Rat).Add(t.lastT, t.segs[t.segmentOf(t.inCount-1)].spacing)
	var out []float64
	for t.nextOut.Cmp(end) < 0 {
		out = append(out, t.lastV)
		t.outCount++
		t.nextOut.Add(t.nextOut, t.outStep)
	}
	return out
}

// Buffered reports how many input samples are retained (never more than 2).
func (t *Timeline) Buffered() int {
	if !t.hasLast {
		return 0
	}
	if !t.hasPrev {
		return 1
	}
	return 2
}

// OutCount reports how many output samples have been emitted so far.
func (t *Timeline) OutCount() int64 { return t.outCount }

// TimelineState is a restorable snapshot of a Timeline, including its rate
// configuration. All rational values are deep-copied by Checkpoint and
// RestoreTimeline.
type TimelineState struct {
	OutRate  Rate
	Segments []InputSegment

	NextOut  *big.Rat
	OutCount int64
	InCount  int64

	PrevT   *big.Rat
	PrevV   float64
	HasPrev bool
	LastT   *big.Rat
	LastV   float64
	HasLast bool

	Flushed bool
}

func copyRat(r *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	return new(big.Rat).Set(r)
}

// Checkpoint captures the full resampler state. Continuing either the
// original or a restored copy with the same remaining input yields
// identical output.
func (t *Timeline) Checkpoint() TimelineState {
	segs := make([]InputSegment, len(t.segs))
	for i, s := range t.segs {
		segs[i] = InputSegment{
			FromIndex: s.fromIndex,
			Rate:      Rate{Num: s.spacing.Denom().Int64(), Den: s.spacing.Num().Int64()},
		}
	}
	return TimelineState{
		OutRate:  Rate{Num: t.outStep.Denom().Int64(), Den: t.outStep.Num().Int64()},
		Segments: segs,
		NextOut:  copyRat(t.nextOut),
		OutCount: t.outCount,
		InCount:  t.inCount,
		PrevT:    copyRat(t.prevT),
		PrevV:    t.prevV,
		HasPrev:  t.hasPrev,
		LastT:    copyRat(t.lastT),
		LastV:    t.lastV,
		HasLast:  t.hasLast,
		Flushed:  t.flushed,
	}
}

// RestoreTimeline rebuilds a Timeline from a checkpoint.
func RestoreTimeline(st TimelineState) *Timeline {
	t := NewTimeline(st.OutRate, st.Segments...)
	t.nextOut = copyRat(st.NextOut)
	t.outCount = st.OutCount
	t.inCount = st.InCount
	t.prevT, t.prevV, t.hasPrev = copyRat(st.PrevT), st.PrevV, st.HasPrev
	t.lastT, t.lastV, t.hasLast = copyRat(st.LastT), st.LastV, st.HasLast
	t.flushed = st.Flushed
	return t
}

// ResampleTimeline is the one-shot exact rational model for Timeline: given
// the whole input at once, it computes on the absolute rational time axis
// the same output a streaming Timeline produces for any chunking of the
// same input, bit-for-bit.
func ResampleTimeline(x []float64, outRate Rate, segs ...InputSegment) []float64 {
	if len(x) == 0 {
		return nil
	}
	t := NewTimeline(outRate, segs...)
	n := int64(len(x))
	times := make([]*big.Rat, n)
	for i := range x {
		times[i] = t.inTime(int64(i))
	}
	end := new(big.Rat).Add(times[n-1], t.segs[t.segmentOf(n-1)].spacing)

	var out []float64
	tt := new(big.Rat)
	k := 0 // first index with times[k] >= tt; nondecreasing as tt advances
	for tt.Cmp(end) < 0 {
		var y float64
		if tt.Sign() == 0 {
			y = x[0]
		} else {
			for k < len(x) && times[k].Cmp(tt) < 0 {
				k++
			}
			if k >= len(x) {
				// Tail region past the last real sample: repeat it.
				y = x[n-1]
			} else {
				num := new(big.Rat).Sub(tt, times[k-1])
				den := new(big.Rat).Sub(times[k], times[k-1])
				frac, _ := num.Quo(num, den).Float64()
				y = x[k-1] + frac*(x[k]-x[k-1])
			}
		}
		out = append(out, y)
		tt.Add(tt, t.outStep)
	}
	return out
}
