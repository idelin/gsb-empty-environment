// Package rmswin implements a dynamic-window exact RMS (root mean square)
// accumulator. All arithmetic is done with exact rational numbers, so
// squaring extreme magnitudes never overflows and the reported mean square
// is an exact fraction. The square root is reported as an exact rational
// interval [lo, hi] with lo <= RMS < hi.
package rmswin

import (
	"errors"
	"math/big"
)

// ErrEmptyWindow is returned when a statistic is requested from a window
// that holds no samples.
var ErrEmptyWindow = errors.New("rmswin: window is empty")

// Window is a sliding window over squared samples with an exact running
// sum. The window limit may be changed at runtime; only the most recent
// maxWindow samples are ever retained.
type Window struct {
	max   int        // hard capacity; history beyond this is discarded
	size  int        // current window limit (1 <= size <= max)
	buf   []*big.Rat // ring buffer of squared samples, len == max
	head  int        // ring index of the oldest retained sample
	count int        // number of retained samples (count <= size)
	sum   *big.Rat   // exact sum of the retained squared samples
}

// NewWindow creates a Window whose history never exceeds maxWindow samples.
// The initial window limit equals maxWindow. It panics if maxWindow < 1.
func NewWindow(maxWindow int) *Window {
	if maxWindow < 1 {
		panic("rmswin: maxWindow must be >= 1")
	}
	return &Window{
		max:  maxWindow,
		size: maxWindow,
		buf:  make([]*big.Rat, maxWindow),
		sum:  new(big.Rat),
	}
}

// SetWindow changes the active window limit at runtime, clamped to
// [1, MaxWindow]. Shrinking immediately evicts the oldest samples beyond
// the new limit. Growing only raises the limit; already-evicted history is
// not recovered, and normalization keeps using the actual sample count
// until the window refills.
func (w *Window) SetWindow(n int) {
	if n < 1 {
		n = 1
	}
	if n > w.max {
		n = w.max
	}
	w.size = n
	for w.count > w.size {
		w.evictOldest()
	}
}

// WindowSize returns the current window limit.
func (w *Window) WindowSize() int { return w.size }

// MaxWindow returns the hard capacity set at construction.
func (w *Window) MaxWindow() int { return w.max }

// Len returns the number of samples currently retained.
func (w *Window) Len() int { return w.count }

// AddRat inserts one exact rational sample. The value is copied, so the
// caller may reuse or mutate v afterwards.
func (w *Window) AddRat(v *big.Rat) {
	sq := new(big.Rat).Mul(v, v)
	w.pushSquared(sq)
}

// AddFloat64 inserts one float64 sample, converted to its exact rational
// value (no rounding). It returns an error for NaN or Inf, which have no
// exact rational form.
func (w *Window) AddFloat64(f float64) error {
	r := new(big.Rat)
	if r.SetFloat64(f) == nil {
		return errors.New("rmswin: NaN or Inf sample rejected")
	}
	w.pushSquared(new(big.Rat).Mul(r, r))
	return nil
}

// AddFloat64Batch inserts a chunk of float64 samples. It is exactly
// equivalent to calling AddFloat64 on each element in order: chunk
// boundaries have no effect on the result. On the first invalid element it
// returns an error and leaves the window unchanged.
func (w *Window) AddFloat64Batch(fs []float64) error {
	sqs := make([]*big.Rat, len(fs))
	for i, f := range fs {
		r := new(big.Rat)
		if r.SetFloat64(f) == nil {
			return errors.New("rmswin: NaN or Inf sample rejected")
		}
		sqs[i] = new(big.Rat).Mul(r, r)
	}
	for _, sq := range sqs {
		w.pushSquared(sq)
	}
	return nil
}

// AddRatBatch inserts a chunk of exact rational samples; equivalent to
// calling AddRat on each element in order.
func (w *Window) AddRatBatch(vs []*big.Rat) {
	for _, v := range vs {
		w.AddRat(v)
	}
}

func (w *Window) pushSquared(sq *big.Rat) {
	if w.count == w.size {
		w.evictOldest()
	}
	idx := (w.head + w.count) % w.max
	w.buf[idx] = sq
	w.sum.Add(w.sum, sq)
	w.count++
}

func (w *Window) evictOldest() {
	w.sum.Sub(w.sum, w.buf[w.head])
	w.buf[w.head] = nil
	w.head = (w.head + 1) % w.max
	w.count--
}

// MeanSquare returns the exact fraction sum(x_i^2)/count over the retained
// samples. When fewer samples than the window limit are present, the actual
// sample count is used as the denominator.
func (w *Window) MeanSquare() (*big.Rat, error) {
	if w.count == 0 {
		return nil, ErrEmptyWindow
	}
	ms := new(big.Rat).Set(w.sum)
	return ms.Quo(ms, new(big.Rat).SetInt64(int64(w.count))), nil
}

// RMSInterval returns exact rational bounds lo, hi such that
// lo <= sqrt(MeanSquare) < hi. The bounds are derived from the integer
// square root of num*den: with ms = p/q and s = isqrt(p*q),
// (s/q)^2 <= p/q < ((s+1)/q)^2, so the interval is exact and its width is
// at most 1/q + rounding of the endpoints.
func (w *Window) RMSInterval() (lo, hi *big.Rat, err error) {
	ms, err := w.MeanSquare()
	if err != nil {
		return nil, nil, err
	}
	p := ms.Num()
	q := ms.Denom()
	n := new(big.Int).Mul(p, q)
	s := new(big.Int).Sqrt(n) // floor(sqrt(p*q))
	lo = new(big.Rat).SetFrac(s, q)
	hi = new(big.Rat).SetFrac(new(big.Int).Add(s, big.NewInt(1)), q)
	return lo, hi, nil
}
