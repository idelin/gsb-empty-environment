// Package streamsum provides streaming floating-point summation with an
// explicit, algorithm-specific error bound.
//
// Values arrive in chunks. Each chunk is summed with Neumaier's improved
// Kahan compensation and carries a rigorous error bound. Chunks sit on a
// stack: the two most recent chunks can be merged into one, and the most
// recent chunk can be retracted (popped), which removes its contribution
// exactly because chunks are kept separate until merged.
//
// Public rules for non-finite values:
//
//   - If any input is NaN, Total returns (NaN, +Inf).
//   - If any input is +/-Inf, or a finite intermediate overflows to
//     +/-Inf, Total follows IEEE 754 arithmetic (+/-Inf, or NaN when
//     infinities of opposite sign meet) and the error bound is +Inf.
//   - The error bound is NEVER finite when the result is non-finite:
//     no fake finite error bounds are reported for infinite arithmetic.
//   - State is tracked per chunk, so retracting the chunk that introduced
//     a non-finite value restores finite results and finite bounds.
//
// Error bound (documented algorithm-specific guarantee):
//
//   - A chunk of n finite values with S = sum|x_i| is summed with
//     Neumaier compensation; its bound is (2u + n*u^2) * S, where
//     u = 2^-53 is the unit roundoff of float64.
//   - Merging or totalling two partial sums (s1, s2) with compensations
//     adds u*(|s1+s2| + |c|) for the rounding of the combining adds.
//   - The final result flips sum+comp, adding u*|result|.
//
// All bounds are accumulated in float64 and rounded up conservatively
// (each bound addition is inflated by one ulp of the result).
package streamsum

import "math"

// u is the unit roundoff of binary64 (float64): 2^-53.
const u = 1.0 / 9007199254740992.0 // 2^-53

// chunk is one block of streamed values with its compensated sum and
// its own error bound.
type chunk struct {
	sum    float64 // compensated running sum
	comp   float64 // Neumaier compensation term
	n      int     // number of values
	sumAbs float64 // sum of |x_i|
	err    float64 // error bound: |sum+comp - exact| <= err
	finite bool    // false if NaN/Inf seen or overflow occurred
}

// ChunkInfo describes a retracted chunk.
type ChunkInfo struct {
	Sum      float64 // compensated sum of the chunk (sum + comp)
	Count    int     // number of values in the chunk
	ErrBound float64 // error bound the chunk carried
}

// Accumulator is a streaming summation state: a stack of chunk summaries.
type Accumulator struct {
	chunks []chunk
}

// NewAccumulator returns an empty accumulator.
func NewAccumulator() *Accumulator { return &Accumulator{} }

// Chunks returns how many unmerged chunks are currently on the stack.
func (a *Accumulator) Chunks() int { return len(a.chunks) }

// AddChunk streams one block of values into the accumulator.
func (a *Accumulator) AddChunk(xs []float64) {
	c := chunk{finite: true}
	for _, x := range xs {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			c.finite = false
		}
		t := c.sum + x
		if math.Abs(c.sum) >= math.Abs(x) {
			c.comp += (c.sum - t) + x
		} else {
			c.comp += (x - t) + c.sum
		}
		c.sum = t
		c.sumAbs += math.Abs(x)
		c.n++
	}
	if math.IsInf(c.sum, 0) || math.IsNaN(c.sum) ||
		math.IsInf(c.comp, 0) || math.IsNaN(c.comp) {
		c.finite = false
	}
	if c.finite {
		c.err = (2*u + float64(c.n)*u*u) * c.sumAbs
	} else {
		c.err = math.Inf(1)
	}
	a.chunks = append(a.chunks, c)
}

// combine merges two chunk summaries into one, accumulating the error
// bound for the extra rounding steps.
func combine(x, y chunk) chunk {
	t := x.sum + y.sum
	var c float64
	if math.Abs(x.sum) >= math.Abs(y.sum) {
		c = (x.sum - t) + y.sum
	} else {
		c = (y.sum - t) + x.sum
	}
	c += x.comp + y.comp
	finite := x.finite && y.finite && !math.IsInf(t, 0) && !math.IsNaN(t) &&
		!math.IsInf(c, 0) && !math.IsNaN(c)
	err := x.err + y.err + u*(math.Abs(t)+math.Abs(c))
	if !finite {
		err = math.Inf(1)
	}
	return chunk{
		sum:    t,
		comp:   c,
		n:      x.n + y.n,
		sumAbs: x.sumAbs + y.sumAbs,
		err:    err,
		finite: finite,
	}
}

// Merge combines the two most recent chunks into a single chunk. After
// merging, the pair can only be retracted together. It is a no-op when
// fewer than two chunks are present.
func (a *Accumulator) Merge() {
	if len(a.chunks) < 2 {
		return
	}
	top := a.chunks[len(a.chunks)-1]
	next := a.chunks[len(a.chunks)-2]
	a.chunks = a.chunks[:len(a.chunks)-2]
	a.chunks = append(a.chunks, combine(next, top))
}

// Retract removes the most recent chunk and returns its summary. The
// second return value is false when the accumulator is empty.
func (a *Accumulator) Retract() (ChunkInfo, bool) {
	if len(a.chunks) == 0 {
		return ChunkInfo{}, false
	}
	c := a.chunks[len(a.chunks)-1]
	a.chunks = a.chunks[:len(a.chunks)-1]
	return ChunkInfo{
		Sum:      c.sum + c.comp,
		Count:    c.n,
		ErrBound: c.err,
	}, true
}

// Total returns the compensated sum of all retained chunks and the
// accumulated error bound. Per the public rules, the bound is +Inf
// whenever the result is non-finite.
func (a *Accumulator) Total() (sum float64, errBound float64) {
	if len(a.chunks) == 0 {
		return 0, 0
	}
	acc := a.chunks[0]
	for _, c := range a.chunks[1:] {
		acc = combine(acc, c)
	}
	if !acc.finite {
		// Non-finite arithmetic: report the plain IEEE 754 sum of the
		// chunk sums (compensation is meaningless once NaN/Inf appear)
		// and never a finite error bound.
		return acc.sum, math.Inf(1)
	}
	result := acc.sum + acc.comp
	if math.IsInf(result, 0) || math.IsNaN(result) {
		return result, math.Inf(1)
	}
	return result, acc.err + u*math.Abs(result)
}
