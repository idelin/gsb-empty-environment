package taskpkg

import (
	"errors"
	"io"
	"math"
)

// ErrInvalidParameter is returned by New or SetRatio when a configured
// value is unusable.
var ErrInvalidParameter = errors.New("phasevocoder: invalid FFT length, hop size or stretch ratio")

// Vocoder time-stretches a mono float64 sample stream while preserving
// pitch. It implements io.Writer for input and io.Reader for the stretched
// output; both sides accept arbitrary chunk sizes.
//
// Analysis uses a Hann window of fftLen samples every analysisHop samples.
// Synthesis frames are placed at hops derived from the rational stretch
// ratio rather than at a fixed hop. Output samples are normalized by the
// accumulated squared window, so overlap regions keep their amplitude.
//
// Transient preservation is not attempted; impulsive material may blur.
type Vocoder struct {
	fftLen      int
	analysisHop int
	window      []float64

	// Active ratio: the synthesis timeline advances num/den analysis
	// hops for every analysis frame.
	num, den int64

	// Exact synthesis-hop accumulator, in units of 1/den. Each frame
	// adds num*analysisHop; the integer quotient is the frame's
	// synthesis hop and the remainder carries into the next frame.
	hopAccum int64

	// Pending ratio, applied at the next frame boundary. A zero den
	// means no change is pending.
	pendingNum, pendingDen int64

	// Exact ideal output timeline position in samples, accumulating the
	// rational ratio. Flush uses it to cap the realized output length.
	idealExact float64

	// Absolute sample position of the current synthesis frame. The
	// first frame starts at zero; later frames advance by the measured
	// integer synthesis hop.
	synPos int

	// Absolute index of the first sample still held in output/windowSum.
	outBase int

	// Target output length after Flush.
	targetOut int

	// Input not yet covered by a complete analysis frame.
	input []float64

	// Overlap-add accumulators, indexed relative to outBase.
	output    []float64
	windowSum []float64

	// Per-frame scratch space.
	frame     []complex128
	spectrum  []complex128
	mag       []float64
	synPhase  []float64
	lastPhase []float64

	frameCount int
	flushed    bool
}

// New creates a Vocoder. fftLen must be a power of two at least 2, and
// analysisHop must be between 1 and fftLen-1. stretchNum/stretchDen give
// the initial time stretch factor as a positive rational: 2/1 doubles
// duration, 1/2 halves it, while 1/1 leaves timing unchanged.
func New(fftLen, analysisHop int, stretchNum, stretchDen int64) (*Vocoder, error) {
	if fftLen < 2 || fftLen&(fftLen-1) != 0 ||
		analysisHop < 1 || analysisHop >= fftLen ||
		stretchNum <= 0 || stretchDen <= 0 {
		return nil, ErrInvalidParameter
	}
	return &Vocoder{
		fftLen:      fftLen,
		analysisHop: analysisHop,
		window:      hannWindow(fftLen),
		num:         stretchNum,
		den:         stretchDen,
		frame:       make([]complex128, fftLen),
		spectrum:    make([]complex128, fftLen),
		mag:         make([]float64, fftLen/2+1),
		synPhase:    make([]float64, fftLen/2+1),
		lastPhase:   make([]float64, fftLen/2+1),
	}, nil
}

// SetRatio schedules a stretch-ratio change. The new rational factor takes
// effect only when the next complete analysis frame is consumed, i.e. at a
// frame boundary; samples already inside the current partial frame keep the
// old ratio. The pending value is replaced on every call.
func (v *Vocoder) SetRatio(stretchNum, stretchDen int64) error {
	if stretchNum <= 0 || stretchDen <= 0 {
		return ErrInvalidParameter
	}
	v.pendingNum, v.pendingDen = stretchNum, stretchDen
	return nil
}

// Write appends input samples. Arbitrary block sizes are allowed: the
// output depends solely on the concatenated sample stream.
func (v *Vocoder) Write(p []float64) (int, error) {
	if v.flushed {
		return 0, io.ErrClosedPipe
	}
	v.input = append(v.input, p...)
	for len(v.input) >= v.fftLen {
		v.processFrame(v.input[:v.fftLen])
		v.input = v.input[v.analysisHop:]
	}
	return len(p), nil
}

// processFrame applies any pending ratio at this frame boundary, measures
// the accumulated synthesis hop, and renders one synthesis frame.
func (v *Vocoder) processFrame(x []float64) {
	if v.pendingDen != 0 {
		// Carry the fractional remainder into the new denominator;
		// rounding costs strictly less than one output sample.
		if v.den != v.pendingDen {
			v.hopAccum = int64(math.Round(float64(v.hopAccum) *
				float64(v.pendingDen) / float64(v.den)))
			if v.hopAccum < 0 {
				v.hopAccum = 0
			}
		}
		v.num, v.den = v.pendingNum, v.pendingDen
		v.pendingNum, v.pendingDen = 0, 0
	}

	v.hopAccum += v.num * int64(v.analysisHop)
	hop := int(v.hopAccum / v.den)
	v.hopAccum -= int64(hop) * v.den

	// The first frame is anchored at position zero; every later frame is
	// placed at the previous position plus its measured synthesis hop.
	if v.frameCount > 0 {
		v.synPos += hop
	}
	v.idealExact += float64(v.num) * float64(v.analysisHop) / float64(v.den)
	v.frameCount++

	v.synthesize(x, hop)
}

// synthesize runs one analysis/synthesis pass: windowed FFT, subtraction of
// the expected per-bin phase advance, wrapping to [-pi, pi), instantaneous
// frequency estimation, synthesis phase propagation, inverse FFT and
// normalized overlap-add.
func (v *Vocoder) synthesize(x []float64, synHop int) {
	n := v.fftLen
	binCount := n/2 + 1

	for i := 0; i < n; i++ {
		v.frame[i] = complex(x[i]*v.window[i], 0)
	}
	fft(v.frame, false)

	for k := 0; k < binCount; k++ {
		sample := v.frame[k]
		v.mag[k] = math.Hypot(real(sample), imag(sample))

		phase := math.Atan2(imag(sample), real(sample))
		expected := 2 * math.Pi * float64(k) * float64(v.analysisHop) / float64(n)
		// Remove the phase a stationary bin-k component would gain over
		// one analysis hop, and fold the residual into [-pi, pi).
		deviation := wrapPi(phase - v.lastPhase[k] - expected)
		// Measured phase advance per analysis hop at this bin.
		frequency := expected + deviation

		// Rescale the advance from the analysis hop to the synthesis hop.
		v.synPhase[k] += frequency * float64(synHop) / float64(v.analysisHop)
		v.lastPhase[k] = phase

		v.spectrum[k] = complex(v.mag[k]*math.Cos(v.synPhase[k]),
			v.mag[k]*math.Sin(v.synPhase[k]))
	}

	// Mirror the lower half so the inverse transform stays real-valued.
	for k := 1; k < n/2; k++ {
		v.spectrum[n-k] = complex(real(v.spectrum[k]), -imag(v.spectrum[k]))
	}
	fft(v.spectrum, true)

	start := v.synPos - v.outBase
	end := start + n
	if end > len(v.output) {
		grown := make([]float64, end)
		copy(grown, v.output)
		v.output = grown
		grownWindow := make([]float64, end)
		copy(grownWindow, v.windowSum)
		v.windowSum = grownWindow
	}

	for i := 0; i < n; i++ {
		idx := start + i
		v.output[idx] += real(v.spectrum[i]) * v.window[i]
		v.windowSum[idx] += v.window[i] * v.window[i]
	}
}

// Read returns finalized output samples, each divided by the squared-window
// sum accumulated at its position. With no finalized sample available it
// returns (0, nil); after Flush it drains the stream and then returns
// io.EOF.
func (v *Vocoder) Read(p []float64) (int, error) {
	limit := v.synPos
	if v.flushed {
		limit = v.targetOut
	}
	// Only indices that actually exist in the overlap-add buffers are
	// readable; after Flush the buffers may overshoot targetOut.
	if produced := len(v.output) + v.outBase; limit > produced {
		limit = produced
	}

	available := limit - v.outBase
	if available <= 0 {
		if v.flushed {
			return 0, io.EOF
		}
		return 0, nil
	}

	count := len(p)
	if count > available {
		count = available
	}
	for i := 0; i < count; i++ {
		if v.windowSum[i] > 0 {
			p[i] = v.output[i] / v.windowSum[i]
		}
	}

	v.output = v.output[count:]
	v.windowSum = v.windowSum[count:]
	v.outBase += count
	return count, nil
}

// Flush marks the stream complete. Any partial tail is zero-padded into a
// final analysis frame, and zero frames follow until the overlap-add windows
// cover the ideal target length, so the realized duration matches the
// (possibly piecewise) stretch ratio to within one analysis hop. After
// Flush, Read drains the remaining output and then returns io.EOF.
func (v *Vocoder) Flush() {
	if v.flushed {
		return
	}

	tailLen := len(v.input)

	// Ideal target length for the whole input. idealExact already
	// accounts for every complete analysis frame (each consumes
	// analysisHop input samples); the fractional tail contributes
	// tailLen ratio-scaled output samples. This must be measured before
	// the zero-padded tail frame adds another full-frame advance.
	target := math.Round(v.idealExact +
		float64(tailLen)*float64(v.num)/float64(v.den))

	if tailLen > 0 {
		frame := make([]float64, v.fftLen)
		copy(frame, v.input)
		v.input = nil
		v.processFrame(frame)
	}

	v.targetOut = int(target)

	if v.frameCount > 0 {
		zero := make([]float64, v.fftLen)
		// Stop once the current frame's coverage already reaches the
		// target; Read truncates any overshoot to targetOut.
		for v.synPos+v.fftLen <= v.targetOut {
			v.processFrame(zero)
		}
	}

	v.flushed = true
}

// Process is a convenience wrapper that stretches in to a newly allocated
// slice, applying Flush internally.
func Process(fftLen, analysisHop int, stretchNum, stretchDen int64, in []float64) ([]float64, error) {
	v, err := New(fftLen, analysisHop, stretchNum, stretchDen)
	if err != nil {
		return nil, err
	}
	if _, err := v.Write(in); err != nil {
		return nil, err
	}
	v.Flush()

	out := make([]float64, 0, len(in))
	buf := make([]float64, 4096)
	for {
		n, readErr := v.Read(buf)
		out = append(out, buf[:n]...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return out, nil
}

// wrapPi maps an angle onto the half-open interval [-pi, pi).
func wrapPi(angle float64) float64 {
	const twoPi = 2 * math.Pi
	wrapped := math.Mod(angle+math.Pi, twoPi)
	if wrapped < 0 {
		wrapped += twoPi
	}
	return wrapped - math.Pi
}
