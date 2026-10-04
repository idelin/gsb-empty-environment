// Package rms implements a dynamic-window root-mean-square accumulator
// using exact rational arithmetic.
//
// The window keeps at most maxWindow squared samples of history. The
// active window length can be changed at run time within [1, maxWindow];
// shrinking and later expanding the window reuses retained history, but
// samples older than maxWindow are discarded permanently.
//
// MeanSquare returns the exact fraction sum(x^2)/n where n is the actual
// number of samples in the active window (which may be smaller than the
// window length when not enough samples have been added).
//
// SqrtInterval returns a rigorous interval [lo, hi] with directed
// rounding such that lo <= sqrt(MeanSquare) <= hi.
package rms

import (
	"math/big"
)

// Window is a dynamic-window RMS accumulator. It is not safe for
// concurrent use.
type Window struct {
	maxSize int        // hard cap on retained history
	win     int        // active window length, 1 <= win <= maxSize
	buf     []*big.Rat // squared samples, oldest first; len(buf) <= maxSize
	sum     *big.Rat   // exact sum of the last min(win, len(buf)) entries of buf
}

// New returns a Window retaining at most maxWindow samples of history.
// The active window length starts at maxWindow. It panics if maxWindow < 1.
func New(maxWindow int) *Window {
	if maxWindow < 1 {
		panic("rms: maxWindow must be >= 1")
	}
	return &Window{
		maxSize: maxWindow,
		win:     maxWindow,
		sum:     new(big.Rat),
	}
}

// MaxWindow returns the maximum number of retained samples.
func (w *Window) MaxWindow() int { return w.maxSize }

// WindowSize returns the current active window length.
func (w *Window) WindowSize() int { return w.win }

// Len returns the number of samples currently retained in history.
func (w *Window) Len() int { return len(w.buf) }

// Count returns the number of samples used for normalization, i.e.
// min(WindowSize, Len).
func (w *Window) Count() int { return w.active() }

func (w *Window) active() int {
	if len(w.buf) < w.win {
		return len(w.buf)
	}
	return w.win
}

// Add appends one sample. The float64 is converted to its exact rational
// value before squaring, so squaring never overflows or underflows.
// It panics if x is NaN or infinite.
func (w *Window) Add(x float64) {
	r := new(big.Rat).SetFloat64(x)
	if r == nil {
		panic("rms: non-finite sample")
	}
	w.AddRat(r)
}

// AddAll appends each sample in order. Feeding samples in any chunking
// yields the same state as adding them one by one.
func (w *Window) AddAll(xs ...float64) {
	for _, x := range xs {
		w.Add(x)
	}
}

// AddRat appends one exact rational sample. x is neither retained nor
// modified.
func (w *Window) AddRat(x *big.Rat) {
	sq := new(big.Rat).Mul(x, x)
	w.buf = append(w.buf, sq)
	w.sum.Add(w.sum, sq)
	if len(w.buf) > w.win {
		// The sample sliding out of the active window is at index
		// len(buf)-1-win; subtract it from the running sum.
		w.sum.Sub(w.sum, w.buf[len(w.buf)-1-w.win])
	}
	if len(w.buf) > w.maxSize {
		// The oldest sample is outside the active window (win <=
		// maxSize < len(buf)), so the sum is already correct.
		w.buf = w.buf[1:]
	}
}

// SetWindow changes the active window length. It panics unless
// 1 <= n <= MaxWindow(). Retained history (up to MaxWindow samples) is
// reused, so expanding the window after shrinking brings older retained
// samples back into the statistics.
func (w *Window) SetWindow(n int) {
	if n < 1 || n > w.maxSize {
		panic("rms: window length out of range")
	}
	w.win = n
	w.recompute()
}

// recompute rebuilds the running sum over the active window.
func (w *Window) recompute() {
	n := w.active()
	w.sum = new(big.Rat)
	for _, sq := range w.buf[len(w.buf)-n:] {
		w.sum.Add(w.sum, sq)
	}
}

// SumSquares returns a copy of the exact sum of squared samples in the
// active window.
func (w *Window) SumSquares() *big.Rat {
	return new(big.Rat).Set(w.sum)
}

// MeanSquare returns the exact fraction sum(x^2)/n over the active
// window, normalized by the actual sample count n = Count(). It returns
// nil when no samples have been added.
func (w *Window) MeanSquare() *big.Rat {
	n := w.active()
	if n == 0 {
		return nil
	}
	return new(big.Rat).Quo(w.sum, big.NewRat(int64(n), 1))
}

// SqrtInterval returns rigorous bounds lo <= sqrt(MeanSquare()) <= hi,
// computed with prec bits of mantissa using directed rounding. It
// returns ok == false when no samples have been added. It panics if
// prec == 0.
func (w *Window) SqrtInterval(prec uint) (lo, hi *big.Float, ok bool) {
	if prec == 0 {
		panic("rms: precision must be >= 1")
	}
	ms := w.MeanSquare()
	if ms == nil {
		return nil, nil, false
	}
	// Round the mean square toward -Inf/+Inf, then take the square
	// root with the same directed rounding. Sqrt is monotone, so the
	// composition still brackets the true value.
	lo = new(big.Float).SetPrec(prec).SetMode(big.ToNegativeInf)
	lo.Sqrt(lo.SetRat(ms))
	hi = new(big.Float).SetPrec(prec).SetMode(big.ToPositiveInf)
	hi.Sqrt(hi.SetRat(ms))
	return lo, hi, true
}
