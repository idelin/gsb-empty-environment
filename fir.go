// Package fir implements a streaming FIR (finite impulse response) filter
// whose state can be exported and restored. A restored filter produces
// output identical, sample for sample, to a filter that ran continuously.
package fir

import "errors"

// ErrEmptyCoeffs is returned when a coefficient set has no taps.
var ErrEmptyCoeffs = errors.New("fir: coefficient set must contain at least one tap")

// coeffSwitch is a coefficient change scheduled to take effect at the
// sample boundary at: the sample with index at is the first one filtered
// with the new coefficients.
type coeffSwitch struct {
	at     uint64
	coeffs []float64
}

// Filter is a streaming FIR filter. The state consists of the ring buffer
// of previous input samples and the absolute sample position; together they
// are sufficient to suspend and later resume processing.
type Filter struct {
	coeffs []float64
	// hist is a ring buffer indexed by sample index modulo len(hist);
	// hist[count%len(hist)] is where the next sample will be written.
	hist  []float64
	count uint64 // number of samples processed so far (stream position)

	pending []coeffSwitch // scheduled switches, sorted by at, stable
}

// New returns a Filter with the given coefficients.
func New(coeffs []float64) (*Filter, error) {
	if len(coeffs) == 0 {
		return nil, ErrEmptyCoeffs
	}
	c := make([]float64, len(coeffs))
	copy(c, coeffs)
	return &Filter{coeffs: c, hist: make([]float64, len(c))}, nil
}

// NewRational returns a Filter whose coefficients are the rational numbers
// nums[i]/dens[i], evaluated in floating point.
func NewRational(nums, dens []int64) (*Filter, error) {
	if len(nums) == 0 || len(nums) != len(dens) {
		return nil, ErrEmptyCoeffs
	}
	coeffs := make([]float64, len(nums))
	for i := range nums {
		if dens[i] == 0 {
			return nil, errors.New("fir: zero denominator in rational coefficient")
		}
		coeffs[i] = float64(nums[i]) / float64(dens[i])
	}
	return New(coeffs)
}

// Count returns the number of samples processed so far, i.e. the index the
// next input sample will have in the stream.
func (f *Filter) Count() uint64 { return f.count }

// Coeffs returns a copy of the currently active coefficients.
func (f *Filter) Coeffs() []float64 {
	c := make([]float64, len(f.coeffs))
	copy(c, f.coeffs)
	return c
}

// ScheduleSwitch schedules a coefficient change that takes effect at the
// sample boundary at: the input sample with index at is the first one
// filtered with the new coefficients. Samples already in the history remain
// and participate in the computation under the new coefficients. Switches
// scheduled for a boundary in the past take effect immediately. Switches at
// the same boundary are applied in scheduling order.
func (f *Filter) ScheduleSwitch(at uint64, coeffs []float64) error {
	if len(coeffs) == 0 {
		return ErrEmptyCoeffs
	}
	c := make([]float64, len(coeffs))
	copy(c, coeffs)
	f.ensureCap(len(c))
	// Insert sorted by at, keeping scheduling order for equal boundaries.
	i := len(f.pending)
	for i > 0 && f.pending[i-1].at > at {
		i--
	}
	f.pending = append(f.pending, coeffSwitch{})
	copy(f.pending[i+1:], f.pending[i:])
	f.pending[i] = coeffSwitch{at: at, coeffs: c}
	return nil
}

// ensureCap grows the ring buffer so it can hold n samples of history.
func (f *Filter) ensureCap(n int) {
	if n <= len(f.hist) {
		return
	}
	next := make([]float64, n)
	valid := f.count
	if uint64(len(f.hist)) < valid {
		valid = uint64(len(f.hist))
	}
	for i := f.count - valid; i < f.count; i++ {
		next[i%uint64(n)] = f.hist[i%uint64(len(f.hist))]
	}
	f.hist = next
}

// applyPending activates every scheduled switch whose boundary has been
// reached by the current stream position.
func (f *Filter) applyPending() {
	for len(f.pending) > 0 && f.pending[0].at <= f.count {
		f.coeffs = f.pending[0].coeffs
		f.pending = f.pending[1:]
	}
}

// ProcessSample filters one input sample and returns the output sample.
func (f *Filter) ProcessSample(x float64) float64 {
	f.applyPending()
	n := f.count
	mod := uint64(len(f.hist))
	f.hist[n%mod] = x
	y := 0.0
	for k, c := range f.coeffs {
		if uint64(k) > n {
			break
		}
		y += c * f.hist[(n-uint64(k))%mod]
	}
	f.count++
	return y
}

// ProcessBlock filters a block of input samples and returns the output
// block. Processing sample by sample, in blocks of any size, or with empty
// blocks interleaved all yield identical results.
func (f *Filter) ProcessBlock(in []float64) []float64 {
	out := make([]float64, len(in))
	for i, x := range in {
		out[i] = f.ProcessSample(x)
	}
	return out
}
