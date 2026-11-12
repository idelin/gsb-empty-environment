// Package stft provides streaming short-time Fourier transform (STFT) analysis
// and weighted overlap-add reconstruction for audio pipelines.
//
// The caller supplies the analysis window and hop size. Frame m starts at
// absolute sample m*hop - len(window)/2; samples outside the input are treated
// as explicit zeros. Analysis windows each frame, applies an optional spectrum
// callback, and synthesises with the same window. The overlap-added signal is
// divided, sample by sample, by the window-square sum actually accumulated at
// that sample. This makes the result independent of how the input is split into
// chunks and removes the periodic boundary gain changes caused by unnormalised
// overlap-add.
//
// A callback receives the full complex spectrum (length len(window)). It may
// modify any bins, but the result must remain conjugate symmetric for a real
// output signal. Non-Hermitian output is rejected. Valid output samples whose
// accumulated window-square sum is exactly zero are also rejected instead of
// being amplified to infinity.
package stft

import (
	"errors"
	"fmt"
	"math"
)

var (
	// ErrInvalidWindow is returned when the window is empty or non-finite.
	ErrInvalidWindow = errors.New("stft: invalid window")
	// ErrInvalidHop is returned when hop is not positive.
	ErrInvalidHop = errors.New("stft: invalid hop")
	// ErrNonHermitian is returned when a callback leaves a spectrum that is not
	// conjugate symmetric.
	ErrNonHermitian = errors.New("stft: spectrum is not conjugate symmetric")
	// ErrZeroWindowSum is returned when an effective output sample has no
	// non-zero window contribution.
	ErrZeroWindowSum = errors.New("stft: zero window-square sum at effective sample")
	// ErrFlushed is returned when samples are pushed after Flush.
	ErrFlushed = errors.New("stft: analyzer already flushed")
)

// Config defines the STFT geometry. Window and Hop are supplied by the caller.
type Config struct {
	Window []float64
	Hop    int
}

// SpectrumFunc may modify the complex spectrum in place. It must preserve
// conjugate symmetry (and real DC/Nyquist bins) so that the inverse transform
// describes a real signal. Returning a non-nil error aborts processing.
type SpectrumFunc func(spectrum []complex128) error

// Analyzer is a streaming STFT analyser and overlap-add reconstructor. It is
// not safe for concurrent use.
type Analyzer struct {
	n      int
	hop    int
	pad    int
	window []float64
	plan   *fftPlan
	cb     SpectrumFunc

	data      []float64 // buffered input beginning at dataStart
	dataStart int
	received  int

	nextFrame int
	acc       []float64
	weight    []float64
	accStart  int

	spec []complex128

	flushed bool
	failed  error
}

// New creates an analyzer with the caller-provided window and hop. A nil
// callback performs identity analysis. The window is copied.
func New(window []float64, hop int, callback SpectrumFunc) (*Analyzer, error) {
	return NewWithConfig(Config{Window: window, Hop: hop}, callback)
}

// NewAnalyzer is an alias for New.
func NewAnalyzer(window []float64, hop int, callback SpectrumFunc) (*Analyzer, error) {
	return New(window, hop, callback)
}

// NewWithConfig creates an analyzer from cfg.
func NewWithConfig(cfg Config, callback SpectrumFunc) (*Analyzer, error) {
	if len(cfg.Window) == 0 {
		return nil, fmt.Errorf("%w: empty", ErrInvalidWindow)
	}
	if cfg.Hop <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidHop, cfg.Hop)
	}
	for i, v := range cfg.Window {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("%w: sample %d is not finite", ErrInvalidWindow, i)
		}
	}

	n := len(cfg.Window)
	w := append([]float64(nil), cfg.Window...)
	return &Analyzer{
		n:      n,
		hop:    cfg.Hop,
		pad:    n / 2,
		window: w,
		plan:   newFFTPlan(n),
		cb:     callback,
		spec:   make([]complex128, n),
	}, nil
}

// Push consumes an arbitrary-length input chunk and returns all output samples
// that have become final. The concatenation of Push and Flush results is
// independent of the input chunking. The returned slice is newly allocated.
func (a *Analyzer) Push(samples []float64) ([]float64, error) {
	if a.failed != nil {
		return nil, a.failed
	}
	if a.flushed {
		return nil, ErrFlushed
	}
	if len(samples) == 0 {
		return nil, nil
	}

	a.data = append(a.data, samples...)
	a.received += len(samples)

	var out []float64
	for {
		start := a.frameStart(a.nextFrame)
		if start+a.n > a.received {
			break
		}
		if err := a.processFrame(start); err != nil {
			a.failed = err
			return nil, err
		}
		a.nextFrame++
		a.trimInput()

		next := start + a.hop
		if next <= a.received {
			chunk, err := a.emitTo(next)
			if err != nil {
				a.failed = err
				return nil, err
			}
			out = append(out, chunk...)
		}
	}
	return out, nil
}

// Process is an alias for Push.
func (a *Analyzer) Process(samples []float64) ([]float64, error) {
	return a.Push(samples)
}

// Flush zero-pads the end of the input, processes every frame that overlaps the
// effective samples, and returns the remaining output. Exactly len(input)
// samples are returned across all Push and Flush calls. Flush is idempotent.
func (a *Analyzer) Flush() ([]float64, error) {
	if a.failed != nil {
		return nil, a.failed
	}
	if a.flushed {
		return nil, nil
	}

	var out []float64
	length := a.received
	for {
		start := a.frameStart(a.nextFrame)
		if start > length-1 {
			break
		}
		if err := a.processFrame(start); err != nil {
			a.failed = err
			return nil, err
		}
		a.nextFrame++
		a.trimInput()

		next := start + a.hop
		if next > length {
			next = length
		}
		chunk, err := a.emitTo(next)
		if err != nil {
			a.failed = err
			return nil, err
		}
		out = append(out, chunk...)
	}

	chunk, err := a.emitTo(length)
	if err != nil {
		a.failed = err
		return nil, err
	}
	out = append(out, chunk...)
	a.flushed = true
	return out, nil
}

// Reset discards all buffered input, output and frame state while keeping the
// window, hop and callback.
func (a *Analyzer) Reset() {
	a.data = nil
	a.dataStart = 0
	a.received = 0
	a.nextFrame = 0
	a.acc = a.acc[:0]
	a.weight = a.weight[:0]
	a.accStart = 0
	a.flushed = false
	a.failed = nil
}

// WindowSize returns the analysis/synthesis window length.
func (a *Analyzer) WindowSize() int { return a.n }

// HopSize returns the frame hop.
func (a *Analyzer) HopSize() int { return a.hop }

// ProcessAll is a convenience wrapper that runs the whole signal through a new
// analyzer.
func ProcessAll(window []float64, hop int, callback SpectrumFunc, samples []float64) ([]float64, error) {
	a, err := New(window, hop, callback)
	if err != nil {
		return nil, err
	}
	out, err := a.Push(samples)
	if err != nil {
		return nil, err
	}
	tail, err := a.Flush()
	if err != nil {
		return nil, err
	}
	return append(out, tail...), nil
}

func (a *Analyzer) frameStart(frame int) int {
	return frame*a.hop - a.pad
}

func (a *Analyzer) processFrame(start int) error {
	for j := 0; j < a.n; j++ {
		abs := start + j
		if abs < 0 || abs >= a.received {
			a.spec[j] = 0
			continue
		}
		idx := abs - a.dataStart
		if idx < 0 || idx >= len(a.data) {
			return fmt.Errorf("stft: internal input window miss at sample %d", abs)
		}
		a.spec[j] = complex(a.data[idx]*a.window[j], 0)
	}
	a.plan.forward(a.spec)
	a.makeHermitian()

	if a.cb != nil {
		if err := a.cb(a.spec); err != nil {
			return err
		}
		if err := a.checkHermitian(); err != nil {
			return err
		}
	}

	a.plan.inverse(a.spec)
	a.addFrame(start)
	return nil
}

// makeHermitian makes the tiny numerical asymmetry of a real-input FFT exact.
// This lets an identity callback pass the conjugate-symmetry check unchanged.
func (a *Analyzer) makeHermitian() {
	a.spec[0] = complex(real(a.spec[0]), 0)
	if a.n%2 == 0 {
		a.spec[a.n/2] = complex(real(a.spec[a.n/2]), 0)
	}
	for k := 1; k < (a.n+1)/2; k++ {
		a.spec[a.n-k] = complex(real(a.spec[k]), -imag(a.spec[k]))
	}
}

func (a *Analyzer) checkHermitian() error {
	if !imagIsZero(a.spec[0]) {
		return fmt.Errorf("%w: DC bin", ErrNonHermitian)
	}
	if a.n%2 == 0 && !imagIsZero(a.spec[a.n/2]) {
		return fmt.Errorf("%w: Nyquist bin", ErrNonHermitian)
	}
	for k := 1; k < (a.n+1)/2; k++ {
		x, y := a.spec[k], a.spec[a.n-k]
		diff := y - complex(real(x), -imag(x))
		if math.Hypot(real(diff), imag(diff)) > 1e-9*(1+math.Hypot(real(x), imag(x))) {
			return fmt.Errorf("%w: bins %d and %d", ErrNonHermitian, k, a.n-k)
		}
	}
	return nil
}

func imagIsZero(v complex128) bool {
	return math.Abs(imag(v)) <= 1e-12*(1+math.Abs(real(v)))
}

func (a *Analyzer) addFrame(start int) {
	rel := start - a.accStart
	first := 0
	if rel < 0 {
		first = -rel
	}
	if first >= a.n {
		return
	}
	end := rel + a.n
	a.ensureAcc(end)

	for j := first; j < a.n; j++ {
		w := a.window[j]
		idx := rel + j
		a.acc[idx] += real(a.spec[j]) * w
		a.weight[idx] += w * w
	}
}

func (a *Analyzer) ensureAcc(length int) {
	if length <= len(a.acc) {
		return
	}
	extra := length - len(a.acc)
	a.acc = append(a.acc, make([]float64, extra)...)
	a.weight = append(a.weight, make([]float64, extra)...)
}

func (a *Analyzer) emitTo(limit int) ([]float64, error) {
	if limit > a.received {
		limit = a.received
	}
	if limit < 0 {
		limit = 0
	}
	if limit <= a.accStart {
		return nil, nil
	}

	count := limit - a.accStart
	out := make([]float64, count)
	for i := 0; i < count; i++ {
		var sum float64
		if i < len(a.weight) {
			sum = a.weight[i]
		}
		if sum == 0 {
			return nil, fmt.Errorf("%w: sample %d", ErrZeroWindowSum, a.accStart+i)
		}
		out[i] = a.acc[i] / sum
	}
	a.dropAcc(count, limit)
	return out, nil
}

func (a *Analyzer) dropAcc(count, limit int) {
	if count >= len(a.acc) {
		a.acc = a.acc[:0]
		a.weight = a.weight[:0]
		a.accStart = limit
		return
	}
	remaining := len(a.acc) - count
	copy(a.acc, a.acc[count:])
	a.acc = a.acc[:remaining]
	copy(a.weight, a.weight[count:])
	a.weight = a.weight[:remaining]
	a.accStart = limit
}

func (a *Analyzer) trimInput() {
	next := a.frameStart(a.nextFrame)
	if next > a.received {
		next = a.received
	}
	if next <= a.dataStart {
		return
	}
	drop := next - a.dataStart
	if drop > len(a.data) {
		drop = len(a.data)
	}
	a.data = a.data[drop:]
	a.dataStart += drop
	if len(a.data) == 0 {
		a.data = nil
	}
}
