// Package windowagg implements a monitoring window over a multiset of
// int64 values. Values can be inserted and removed (one occurrence at a
// time); duplicates are counted individually. It reports the exact median
// and the trimmed mean (dropping the k smallest and k largest values) as
// reduced rationals.
package windowagg

import (
	"math/big"
	"sort"
)

// Window is a multiset of values kept in sorted (non-decreasing) order.
// The zero value is ready to use.
type Window struct {
	vals []int64  // sorted ascending
	sum  *big.Int // exact running sum, immune to int64 overflow
}

// New returns an empty Window.
func New() *Window {
	return &Window{sum: new(big.Int)}
}

// Len reports the number of values currently in the window,
// counting each duplicate individually.
func (w *Window) Len() int { return len(w.vals) }

// Insert adds one occurrence of v to the window.
func (w *Window) Insert(v int64) {
	i := sort.Search(len(w.vals), func(i int) bool { return w.vals[i] >= v })
	w.vals = append(w.vals, 0)
	copy(w.vals[i+1:], w.vals[i:])
	w.vals[i] = v
	w.lazySum().Add(w.lazySum(), big.NewInt(v))
}

// Remove deletes one occurrence of v from the window. It reports whether
// v was present. Removing a value that appears multiple times deletes a
// single instance; the rest remain.
func (w *Window) Remove(v int64) bool {
	i := sort.Search(len(w.vals), func(i int) bool { return w.vals[i] >= v })
	if i >= len(w.vals) || w.vals[i] != v {
		return false
	}
	copy(w.vals[i:], w.vals[i+1:])
	w.vals = w.vals[:len(w.vals)-1]
	w.lazySum().Sub(w.lazySum(), big.NewInt(v))
	return true
}

// Sum returns the exact sum of all values in the window.
func (w *Window) Sum() *big.Int { return new(big.Int).Set(w.lazySum()) }

// Median returns the exact median of the window as a reduced rational.
// For an even number of values it is the mean of the two middle values.
// ok is false when the window is empty.
func (w *Window) Median() (med *big.Rat, ok bool) {
	n := len(w.vals)
	if n == 0 {
		return nil, false
	}
	mid := n / 2
	if n%2 == 1 {
		return new(big.Rat).SetInt64(w.vals[mid]), true
	}
	num := new(big.Int).Add(big.NewInt(w.vals[mid-1]), big.NewInt(w.vals[mid]))
	return new(big.Rat).SetFrac(num, big.NewInt(2)), true
}

// TrimmedMean drops the k smallest and k largest values and returns the
// mean of the remainder as a reduced rational. ok is false when the
// window holds fewer than 2k+1 values (nothing would remain, or the trim
// is ambiguous) or when k is negative.
func (w *Window) TrimmedMean(k int) (mean *big.Rat, ok bool) {
	if k < 0 {
		return nil, false
	}
	n := len(w.vals)
	if n < 2*k+1 {
		return nil, false
	}
	rest := n - 2*k
	sum := new(big.Int)
	for _, v := range w.vals[k : n-k] {
		sum.Add(sum, big.NewInt(v))
	}
	return new(big.Rat).SetFrac(sum, big.NewInt(int64(rest))), true
}

// lazySum keeps the zero-value Window usable without New.
func (w *Window) lazySum() *big.Int {
	if w.sum == nil {
		w.sum = new(big.Int)
	}
	return w.sum
}
