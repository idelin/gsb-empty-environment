// Package resample provides streaming sample resampling with exact
// rational phase, linear interpolation, fragment reassembly and
// sample-rate switching on an absolute rational timeline.
//
// All resamplers keep exact rational phase across Write calls, so arbitrary
// chunking of the input stream yields bit-identical output. Internal
// buffering is O(1): at most two input samples are retained regardless of
// stream length.
package resample

// Resampler is a fixed-ratio streaming resampler.
//
// The ratio is the exact rational stepNum/stepDen input samples per output
// sample. Output n sits at input time t_n = n*stepNum/stepDen and is
// produced by linear interpolation. Outputs are emitted while t_n < N
// (N = total input samples); when the interpolation would need a sample
// past the end of the stream, the last sample is repeated. Empty input
// produces no output.
type Resampler struct {
	stepNum int64
	stepDen int64

	tNum     int64 // next output time numerator; denominator is stepDen
	inCount  int64
	outCount int64

	prev    float64
	cur     float64
	haveCur bool

	flushed bool
}

// New returns a Resampler with ratio stepNum/stepDen input samples per
// output sample. Both must be positive. Use stepNum < stepDen for
// upsampling, stepNum > stepDen for downsampling.
func New(stepNum, stepDen int64) *Resampler {
	if stepNum <= 0 || stepDen <= 0 {
		panic("resample: step numerator and denominator must be positive")
	}
	return &Resampler{stepNum: stepNum, stepDen: stepDen}
}

// Write consumes the next contiguous input samples and returns any outputs
// whose interpolation is fully determined by the samples seen so far.
// Phase is carried exactly across calls, so chunk boundaries never affect
// the result. Writing after Flush panics.
func (r *Resampler) Write(x []float64) []float64 {
	if r.flushed {
		panic("resample: Write after Flush")
	}
	var out []float64
	for _, v := range x {
		idx := r.inCount
		r.inCount++
		r.prev, r.cur = r.cur, v
		r.haveCur = true
		// Emit every output with tNum/stepDen <= idx: the bracketing
		// pair (idx-1, idx) is now fully available.
		for r.tNum <= idx*r.stepDen {
			var y float64
			if idx == 0 {
				y = v
			} else {
				fracNum := r.tNum - (idx-1)*r.stepDen
				frac := float64(fracNum) / float64(r.stepDen)
				y = r.prev + frac*(v-r.prev)
			}
			out = append(out, y)
			r.outCount++
			r.tNum += r.stepNum
		}
	}
	return out
}

// Flush ends the stream and emits the remaining tail outputs. Outputs whose
// right interpolation point lies past the last input sample use the last
// sample repeated. Flush is idempotent; subsequent calls return nil.
func (r *Resampler) Flush() []float64 {
	if r.flushed {
		return nil
	}
	r.flushed = true
	if r.inCount == 0 {
		return nil
	}
	// After the last Write, tNum > (inCount-1)*stepDen, so every
	// remaining output with tNum/stepDen < inCount falls in the
	// repeat-last tail region.
	end := r.inCount * r.stepDen
	var out []float64
	for r.tNum < end {
		out = append(out, r.cur)
		r.outCount++
		r.tNum += r.stepNum
	}
	return out
}

// Buffered reports how many input samples are currently retained. It never
// exceeds 2, independent of stream length.
func (r *Resampler) Buffered() int {
	if !r.haveCur {
		return 0
	}
	if r.inCount < 2 {
		return 1
	}
	return 2
}

// OutCount reports how many output samples have been emitted so far.
func (r *Resampler) OutCount() int64 { return r.outCount }

// ResampleAll is the one-shot exact rational model for the fixed-ratio
// resampler: it reads the whole input at once and returns the same output
// a streaming Resampler produces for any chunking of the same input,
// bit-for-bit.
func ResampleAll(x []float64, stepNum, stepDen int64) []float64 {
	if stepNum <= 0 || stepDen <= 0 {
		panic("resample: step numerator and denominator must be positive")
	}
	n := int64(len(x))
	if n == 0 {
		return nil
	}
	// Output count: #{m >= 0 : m*stepNum < n*stepDen} = ceil(n*stepDen/stepNum).
	count := (n*stepDen + stepNum - 1) / stepNum
	out := make([]float64, 0, count)
	for m := int64(0); m < count; m++ {
		tNum := m * stepNum
		var y float64
		switch {
		case tNum == 0:
			y = x[0]
		default:
			// k = ceil(t): pair (k-1, k) brackets t with frac in (0,1].
			k := (tNum + stepDen - 1) / stepDen
			if k >= n {
				// Tail region (n-1, n): repeat the last sample.
				y = x[n-1]
			} else {
				fracNum := tNum - (k-1)*stepDen
				frac := float64(fracNum) / float64(stepDen)
				y = x[k-1] + frac*(x[k]-x[k-1])
			}
		}
		out = append(out, y)
	}
	return out
}
