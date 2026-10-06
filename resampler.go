// Package resample provides streaming sample-rate conversion using a
// rational rate ratio and linear interpolation.
//
// The resampler keeps its phase as an exact rational position on the
// input-sample axis, so feeding input in arbitrarily sized chunks yields
// bit-identical output to feeding the whole stream at once: chunking never
// resets or drifts the phase.
//
// Output positions (in input-sample units) are
//
//	pos_k = k * den / num      k = 0, 1, 2, ...
//
// where num/den is the output/input rate ratio in lowest terms. An input
// stream of N samples therefore produces K = ceil(N*num/den) outputs: all
// positions pos_k strictly below N. Output samples whose interpolation
// window reaches past the end of the input are completed by repeating the
// last input sample. Flushing an empty stream produces no output.
//
// The internal buffer holds only the input samples still needed by the
// next output (at most one between writes), so memory stays constant
// regardless of stream length.
package resample

import "errors"

// ErrNonPositiveRatio is returned when a rate ratio is not strictly positive.
var ErrNonPositiveRatio = errors.New("resample: rate ratio must be positive")

// ErrWriteAfterFlush is returned when Write is called after Flush.
var ErrWriteAfterFlush = errors.New("resample: write after flush")

// Resampler converts a contiguous, in-order input stream to a new sample
// rate. It is not safe for concurrent use.
type Resampler struct {
	num int64 // rate ratio numerator   (output rate), lowest terms
	den int64 // rate ratio denominator (input rate),  lowest terms

	i   int64 // integer part of the next output position (input-sample units)
	r   int64 // fraction numerator of the next output position; denominator is num
	out int64 // number of outputs emitted so far

	base int64     // absolute input index of buf[0]
	buf  []float64 // pending input samples, absolute indices [base, base+len(buf))
	done bool      // Flush has been called
}

// New returns a Resampler converting between the rates num/den
// (output rate / input rate). The ratio is reduced to lowest terms.
func New(num, den int64) (*Resampler, error) {
	if num <= 0 || den <= 0 {
		return nil, ErrNonPositiveRatio
	}
	g := gcd(num, den)
	return &Resampler{num: num / g, den: den / g}, nil
}

// Write appends contiguous input samples (continuing the stream from all
// previous writes) and returns the output samples that become available.
// The returned slice is newly allocated and safe to keep.
func (s *Resampler) Write(p []float64) ([]float64, error) {
	if s.done {
		return nil, ErrWriteAfterFlush
	}
	s.buf = append(s.buf, p...)
	avail := s.base + int64(len(s.buf)) // absolute count of received samples
	var out []float64
	// Emit every output whose interpolation window [i, i+1] is fully received.
	for s.i+2 <= avail {
		out = append(out, s.value(s.i, s.r, avail))
		s.advance()
	}
	s.compact()
	return out, nil
}

// Flush ends the input stream and returns the remaining outputs. Outputs
// whose interpolation window extends past the last input sample are
// completed by repeating the last sample. Flushing an empty stream returns
// nil. Flush is idempotent; Write must not be called afterwards.
func (s *Resampler) Flush() ([]float64, error) {
	if s.done {
		return nil, nil
	}
	s.done = true
	n := s.base + int64(len(s.buf))
	if n == 0 {
		return nil, nil
	}
	// Total output count: all positions k*den/num strictly below n.
	total := ceilDiv(n*s.num, s.den)
	var out []float64
	for s.out < total {
		out = append(out, s.value(s.i, s.r, n))
		s.advance()
	}
	return out, nil
}

// Buffered reports how many input samples are currently held inside the
// resampler. It stays bounded by a small constant no matter how long the
// stream runs.
func (s *Resampler) Buffered() int { return len(s.buf) }

// OutputCount returns the number of outputs emitted so far.
func (s *Resampler) OutputCount() int64 { return s.out }

// compact drops buffered samples no future output can reference. The last
// received sample is always kept: Flush may need it to fill the tail.
func (s *Resampler) compact() {
	avail := s.base + int64(len(s.buf))
	if avail == 0 {
		return
	}
	keep := s.i
	if keep > avail-1 {
		keep = avail - 1
	}
	if keep > s.base {
		drop := keep - s.base
		copy(s.buf, s.buf[drop:])
		s.buf = s.buf[:len(s.buf)-int(drop)]
		s.base = keep
	}
}

// TotalOutputCount returns the total number of outputs a stream of n input
// samples produces after Flush: ceil(n*num/den).
func (s *Resampler) TotalOutputCount(n int64) int64 {
	if n <= 0 {
		return 0
	}
	return ceilDiv(n*s.num, s.den)
}

// value computes the linearly interpolated sample at position i + r/num,
// clamping the window to the last available sample (index n-1).
func (s *Resampler) value(i, r, n int64) float64 {
	j0 := i
	if j0 > n-1 {
		j0 = n - 1
	}
	j1 := i + 1
	if j1 > n-1 {
		j1 = n - 1
	}
	x0 := s.buf[j0-s.base]
	x1 := s.buf[j1-s.base]
	return x0 + (x1-x0)*float64(r)/float64(s.num)
}

// advance moves the output position to the next output: pos += den/num.
// The fraction numerator stays in [0, num), so no overflow is possible.
func (s *Resampler) advance() {
	acc := s.r + s.den
	s.i += acc / s.num
	s.r = acc % s.num
	s.out++
}

func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
