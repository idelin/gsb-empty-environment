// Package streamsum provides streaming floating-point summation with an
// explicit, data-dependent error estimate, block-wise updates, retraction
// of the most recent block, and merging of partial sums.
//
// Algorithm
//
// Values are ingested in blocks. Inside a block, values are summed with
// plain recursive (left-to-right) summation, which admits the rigorous
// bound (m-1)*u*sum|x_i| for a block of m values (Higham, "Accuracy and
// Stability of Numerical Algorithms"), where u = 2^-53 is the unit
// roundoff of float64. Block totals are then folded into a global
// accumulator with Neumaier's compensated summation, whose first-order
// error is bounded by 2*u*sum|block totals|.
//
// The reported error estimate is therefore
//
//	bound = sum_blocks (m_b - 1) * u * sum|x in block b|
//	      + 2 * u * sum_blocks |total_b|
//
// Because the first term grows with the block size m_b, partitioning the
// same data into larger blocks yields a larger (more conservative) bound.
//
// Special values (public rules)
//
//   - If any ingested value is NaN, Result is NaN.
//   - Otherwise, if infinities of both signs were ingested, Result is NaN.
//   - Otherwise, if at least one infinity was ingested, Result is that
//     signed infinity.
//   - If finite accumulation overflows to an infinity, Result reflects it.
//   - Whenever Result is not finite, ErrorBound returns +Inf: the library
//     never reports a finite error bound for a non-finite result.
//
// Retract removes the effect of the most recent Add/AddBlock/Merge call.
// Each such call pushes one snapshot, so memory grows linearly with the
// number of unretracted blocks.
package streamsum

import "math"

// u is the unit roundoff of IEEE 754 binary64: 2^-53.
const u = 1.0 / (1 << 53)

type snapshot struct {
	sum, comp float64
	blockErr  float64
	absBlocks float64
	n         int64
	nNaN      int
	nPosInf   int
	nNegInf   int
}

// Summer accumulates a streaming sum block by block.
type Summer struct {
	sum, comp float64 // Neumaier accumulator over block totals
	blockErr  float64 // accumulated per-block error bounds
	absBlocks float64 // sum of |block totals|, for the global bound
	n         int64   // number of finite values ingested
	nNaN      int
	nPosInf   int
	nNegInf   int
	hist      []snapshot
}

// New returns an empty Summer.
func New() *Summer { return &Summer{} }

func (s *Summer) push() {
	s.hist = append(s.hist, snapshot{
		sum: s.sum, comp: s.comp,
		blockErr: s.blockErr, absBlocks: s.absBlocks,
		n: s.n, nNaN: s.nNaN, nPosInf: s.nPosInf, nNegInf: s.nNegInf,
	})
}

// Add ingests a single value (a block of size 1, which contributes no
// block-internal rounding bound).
func (s *Summer) Add(x float64) { s.AddBlock([]float64{x}) }

// AddBlock ingests a block of values. The block is summed internally with
// plain recursive summation; the block total is then folded into the
// global compensated accumulator. An empty block is a no-op and cannot be
// retracted.
func (s *Summer) AddBlock(xs []float64) {
	if len(xs) == 0 {
		return
	}
	s.push()

	total := 0.0
	abs := 0.0
	finite := true
	for _, x := range xs {
		switch {
		case math.IsNaN(x):
			s.nNaN++
			finite = false
		case math.IsInf(x, 1):
			s.nPosInf++
			finite = false
		case math.IsInf(x, -1):
			s.nNegInf++
			finite = false
		default:
			total += x
			abs += math.Abs(x)
			s.n++
		}
	}
	if finite {
		// Rigorous bound for recursive summation of len(xs) values.
		s.blockErr += float64(len(xs)-1) * u * abs
	}
	s.addTotal(total)
}

// addTotal folds a block total into the Neumaier accumulator.
func (s *Summer) addTotal(t float64) {
	sum := s.sum + t
	if math.IsInf(sum, 0) || math.IsNaN(sum) {
		// Overflow (or a non-finite running total): compensation is
		// meaningless; keep the non-finite total and reset comp so
		// Result reports the infinity instead of NaN.
		s.sum = sum
		s.comp = 0
		s.absBlocks += math.Abs(t)
		return
	}
	if math.Abs(s.sum) >= math.Abs(t) {
		s.comp += (s.sum - sum) + t
	} else {
		s.comp += (t - sum) + s.sum
	}
	s.sum = sum
	s.absBlocks += math.Abs(t)
}

// Merge folds another Summer's accumulated state into s as a single
// additional block whose value is o's current result and whose inherited
// error is o's current error bound. Special-value counts are merged.
// The merge can be undone with Retract. Merging a summer into itself is
// not allowed and is a no-op.
func (s *Summer) Merge(o *Summer) {
	if o == s {
		return
	}
	s.push()
	s.nNaN += o.nNaN
	s.nPosInf += o.nPosInf
	s.nNegInf += o.nNegInf
	s.n += o.n
	if o.nNaN == 0 && o.nPosInf == 0 && o.nNegInf == 0 {
		total := o.sum + o.comp
		if !math.IsInf(total, 0) && !math.IsNaN(total) {
			s.blockErr += o.errorBoundFinite()
			s.addTotal(total)
			return
		}
	}
	// Non-finite other: record a synthetic overflow so Result reflects it.
	if o.nNaN == 0 && o.nPosInf == 0 && o.nNegInf == 0 {
		total := o.sum + o.comp
		switch {
		case math.IsNaN(total):
			s.nNaN++
		case math.IsInf(total, 1):
			s.nPosInf++
		case math.IsInf(total, -1):
			s.nNegInf++
		}
	}
}

// Retract undoes the most recent Add, AddBlock, or Merge. It reports
// whether there was anything to retract.
func (s *Summer) Retract() bool {
	if len(s.hist) == 0 {
		return false
	}
	snap := s.hist[len(s.hist)-1]
	s.hist = s.hist[:len(s.hist)-1]
	s.sum, s.comp = snap.sum, snap.comp
	s.blockErr, s.absBlocks = snap.blockErr, snap.absBlocks
	s.n = snap.n
	s.nNaN, s.nPosInf, s.nNegInf = snap.nNaN, snap.nPosInf, snap.nNegInf
	return true
}

// Result returns the current sum according to the special-value rules
// documented in the package comment.
func (s *Summer) Result() float64 {
	switch {
	case s.nNaN > 0, s.nPosInf > 0 && s.nNegInf > 0:
		return math.NaN()
	case s.nPosInf > 0:
		return math.Inf(1)
	case s.nNegInf > 0:
		return math.Inf(-1)
	}
	return s.sum + s.comp
}

// errorBoundFinite is the error estimate assuming a finite result.
func (s *Summer) errorBoundFinite() float64 {
	return s.blockErr + 2*u*s.absBlocks
}

// ErrorBound returns the error estimate for Result. It is +Inf whenever
// Result is not finite: no finite bound is claimed for infinite or NaN
// results.
func (s *Summer) ErrorBound() float64 {
	r := s.Result()
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return math.Inf(1)
	}
	return s.errorBoundFinite()
}

// Count returns the number of finite values ingested (excluding
// retracted ones).
func (s *Summer) Count() int64 { return s.n }
