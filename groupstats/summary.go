// Package groupstats maintains exact streaming statistics (count, mean and
// population variance) over groups of integer samples. All statistics are
// returned as reduced rational numbers, and summaries can be merged together
// or have a previously merged summary undone.
package groupstats

import (
	"errors"
	"math/big"
)

// ErrInvalidUnmerge is returned when a summary being undone was not (or cannot
// have been) part of the receiver: the subtraction would produce a negative
// count or statistics that are impossible for any real sample set.
var ErrInvalidUnmerge = errors.New("groupstats: unmerge of a summary that was not added")

// Summary is an exact aggregate over a group of integer samples.
// The zero value is an empty group ready to use.
type Summary struct {
	count *big.Int
	sum   *big.Int
	sumSq *big.Int
}

// New returns an empty Summary.
func New() *Summary {
	return &Summary{count: new(big.Int), sum: new(big.Int), sumSq: new(big.Int)}
}

func (s *Summary) init() {
	if s.count == nil {
		s.count = new(big.Int)
		s.sum = new(big.Int)
		s.sumSq = new(big.Int)
	}
}

// Clone returns a deep copy of s.
func (s *Summary) Clone() *Summary {
	s.init()
	return &Summary{
		count: new(big.Int).Set(s.count),
		sum:   new(big.Int).Set(s.sum),
		sumSq: new(big.Int).Set(s.sumSq),
	}
}

// Add records a single integer sample.
func (s *Summary) Add(x *big.Int) {
	s.init()
	s.count.Add(s.count, big.NewInt(1))
	s.sum.Add(s.sum, x)
	s.sumSq.Add(s.sumSq, new(big.Int).Mul(x, x))
}

// Merge combines another group's summary into s.
func (s *Summary) Merge(o *Summary) {
	s.init()
	o.init()
	s.count.Add(s.count, o.count)
	s.sum.Add(s.sum, o.sum)
	s.sumSq.Add(s.sumSq, o.sumSq)
}

// Unmerge removes a summary that was previously merged into (or added to) s.
// It returns ErrInvalidUnmerge and leaves s unchanged when the subtraction
// cannot correspond to real data: a negative count, a negative sum of
// squares, or a violation of count*sumSq >= sum^2 (which every real sample
// set satisfies by Cauchy-Schwarz).
func (s *Summary) Unmerge(o *Summary) error {
	s.init()
	o.init()
	count := new(big.Int).Sub(s.count, o.count)
	sum := new(big.Int).Sub(s.sum, o.sum)
	sumSq := new(big.Int).Sub(s.sumSq, o.sumSq)
	if count.Sign() < 0 || sumSq.Sign() < 0 {
		return ErrInvalidUnmerge
	}
	if count.Sign() > 0 {
		lhs := new(big.Int).Mul(count, sumSq)
		rhs := new(big.Int).Mul(sum, sum)
		if lhs.Cmp(rhs) < 0 {
			return ErrInvalidUnmerge
		}
	} else if sum.Sign() != 0 || sumSq.Sign() != 0 {
		// An empty remainder must be exactly empty.
		return ErrInvalidUnmerge
	}
	s.count, s.sum, s.sumSq = count, sum, sumSq
	return nil
}

// Count returns the number of samples in the group.
func (s *Summary) Count() *big.Int {
	s.init()
	return new(big.Int).Set(s.count)
}

// Mean returns the exact mean as a reduced rational, or nil for an empty group.
func (s *Summary) Mean() *big.Rat {
	s.init()
	if s.count.Sign() == 0 {
		return nil
	}
	return new(big.Rat).SetFrac(s.sum, s.count)
}

// Variance returns the exact population variance as a reduced rational,
// or nil for an empty group.
func (s *Summary) Variance() *big.Rat {
	s.init()
	if s.count.Sign() == 0 {
		return nil
	}
	// var = (count*sumSq - sum^2) / count^2, always >= 0 for real data.
	num := new(big.Int).Mul(s.count, s.sumSq)
	num.Sub(num, new(big.Int).Mul(s.sum, s.sum))
	den := new(big.Int).Mul(s.count, s.count)
	return new(big.Rat).SetFrac(num, den)
}
