// Package groupstats maintains grouped integer sample summaries with exact
// rational statistics. Summaries can be merged and previously merged
// summaries can be undone; invalid undos are rejected.
package groupstats

import (
	"errors"
	"math/big"
)

// ErrEmpty is returned when a statistic is requested from an empty summary.
var ErrEmpty = errors.New("groupstats: empty summary")

// ErrInvalidUndo is returned when undoing a summary would leave a state
// that no group of integer samples could produce.
var ErrInvalidUndo = errors.New("groupstats: invalid undo")

// Summary aggregates a group of integer samples. Create summaries with
// NewSummary or SummaryOf.
type Summary struct {
	count *big.Int
	sum   *big.Int
	sumSq *big.Int
}

// NewSummary returns an empty summary.
func NewSummary() *Summary {
	return &Summary{new(big.Int), new(big.Int), new(big.Int)}
}

// SummaryOf builds a summary from a group of samples.
func SummaryOf(samples ...int64) *Summary {
	s := NewSummary()
	s.Add(samples...)
	return s
}

// Clone returns a deep copy of s.
func (s *Summary) Clone() *Summary {
	return &Summary{
		new(big.Int).Set(s.count),
		new(big.Int).Set(s.sum),
		new(big.Int).Set(s.sumSq),
	}
}

// Add appends samples to the summary.
func (s *Summary) Add(samples ...int64) {
	one := big.NewInt(1)
	for _, x := range samples {
		bx := big.NewInt(x)
		s.count.Add(s.count, one)
		s.sum.Add(s.sum, bx)
		s.sumSq.Add(s.sumSq, new(big.Int).Mul(bx, bx))
	}
}

// Merge folds other into s. other is left unchanged.
func (s *Summary) Merge(other *Summary) {
	s.count.Add(s.count, other.count)
	s.sum.Add(s.sum, other.sum)
	s.sumSq.Add(s.sumSq, other.sumSq)
}

// realizable reports whether count/sum/sumSq could come from an actual
// group of integer samples. With n integers summing to S, the sum of
// squares Q satisfies Q >= Qmin (values as equal as possible) and
// Q ≡ S (mod 2) since x² ≡ x (mod 2); both conditions together are also
// sufficient, because any (a, b) pair can be spread to (a+1, b-1),
// raising Q by exactly 2(a-b)+2.
func realizable(count, sum, sumSq *big.Int) bool {
	if count.Sign() < 0 || sumSq.Sign() < 0 {
		return false
	}
	if count.Sign() == 0 {
		return sum.Sign() == 0 && sumSq.Sign() == 0
	}
	if new(big.Int).Sub(sumSq, sum).Bit(0) != 0 {
		return false
	}
	// Qmin for sum S over n integers: S = q*n + r with 0 <= r < n,
	// Qmin = (n-r)*q² + r*(q+1)².
	q := new(big.Int)
	r := new(big.Int)
	q.DivMod(sum, count, r)
	q1 := new(big.Int).Add(q, big.NewInt(1))
	qmin := new(big.Int).Mul(new(big.Int).Sub(count, r), new(big.Int).Mul(q, q))
	qmin.Add(qmin, new(big.Int).Mul(r, new(big.Int).Mul(q1, q1)))
	return sumSq.Cmp(qmin) >= 0
}

// Undo removes a previously merged (or added) summary other from s.
// It returns ErrInvalidUndo and leaves s unchanged if the residual state
// could not come from any group of integer samples (negative count,
// negative sum of squares, leftover mass at zero count, or an
// unrealizable count/sum/sumSq combination).
func (s *Summary) Undo(other *Summary) error {
	count := new(big.Int).Sub(s.count, other.count)
	sum := new(big.Int).Sub(s.sum, other.sum)
	sumSq := new(big.Int).Sub(s.sumSq, other.sumSq)
	if !realizable(count, sum, sumSq) {
		return ErrInvalidUndo
	}
	s.count, s.sum, s.sumSq = count, sum, sumSq
	return nil
}

// Count returns the number of samples.
func (s *Summary) Count() *big.Int {
	return new(big.Int).Set(s.count)
}

// Mean returns the exact mean as a reduced rational.
func (s *Summary) Mean() (*big.Rat, error) {
	if s.count.Sign() == 0 {
		return nil, ErrEmpty
	}
	return new(big.Rat).SetFrac(s.sum, s.count), nil
}

// Variance returns the exact population variance as a reduced rational:
// sumSq/count - (sum/count)².
func (s *Summary) Variance() (*big.Rat, error) {
	if s.count.Sign() == 0 {
		return nil, ErrEmpty
	}
	num := new(big.Int).Mul(s.sumSq, s.count)
	num.Sub(num, new(big.Int).Mul(s.sum, s.sum))
	den := new(big.Int).Mul(s.count, s.count)
	return new(big.Rat).SetFrac(num, den), nil
}
