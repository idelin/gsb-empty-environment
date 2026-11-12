package taskpkg

import "fmt"

// Matrix performs multi-channel matrix convolution: each of the numOut
// output channels is the sum of numIn independently convolved signals, so
// the number of input and output channels may differ. Every (out, in) pair
// owns a Convolver with its own impulse response, updatable independently
// via SetIR. All pairs share the same block size and are processed in
// lockstep, so their block boundaries stay aligned.
//
// The same streaming, crossfade and tail-preservation semantics as
// Convolver apply. Not safe for concurrent use, except that SetIR may be
// called from another goroutine than Process/Flush.
type Matrix struct {
	numIn  int
	numOut int
	convs  [][]*Convolver // [out][in]
}

// NewMatrix returns a Matrix with numIn input and numOut output channels
// and the given partition block size. All pairs start with an empty IR
// (silence); install responses with SetIR.
func NewMatrix(numIn, numOut, blockSize int) *Matrix {
	if numIn < 1 || numOut < 1 {
		panic("taskpkg: matrix needs at least one input and one output")
	}
	m := &Matrix{numIn: numIn, numOut: numOut, convs: make([][]*Convolver, numOut)}
	for j := range m.convs {
		m.convs[j] = make([]*Convolver, numIn)
		for i := range m.convs[j] {
			m.convs[j][i] = NewConvolver(blockSize, nil)
		}
	}
	return m
}

// NumIn returns the number of input channels.
func (m *Matrix) NumIn() int { return m.numIn }

// NumOut returns the number of output channels.
func (m *Matrix) NumOut() int { return m.numOut }

// Convolver returns the Convolver of the given (out, in) pair, e.g. to
// query its TailLength.
func (m *Matrix) Convolver(outCh, inCh int) *Convolver {
	return m.convs[outCh][inCh]
}

// SetIR schedules an IR update for one (out, in) pair. See Convolver.SetIR
// for the crossfade and tail-preservation semantics.
func (m *Matrix) SetIR(outCh, inCh int, ir []float32, fadeSamples int) {
	m.convs[outCh][inCh].SetIR(ir, fadeSamples)
}

// Process feeds one chunk of input per input channel and returns one chunk
// of output per output channel. All input slices must have the same length;
// they may have any length, including less than one block. All returned
// slices have the same length (a multiple of the block size).
func (m *Matrix) Process(inputs [][]float32) [][]float32 {
	if len(inputs) != m.numIn {
		panic(fmt.Sprintf("taskpkg: got %d input channels, want %d", len(inputs), m.numIn))
	}
	n := len(inputs[0])
	for i, in := range inputs {
		if len(in) != n {
			panic(fmt.Sprintf("taskpkg: input channel %d has %d samples, want %d", i, len(in), n))
		}
	}
	outs := make([][]float32, m.numOut)
	for j := range m.convs {
		var acc []float32
		for i, c := range m.convs[j] {
			y := c.Process(inputs[i])
			if acc == nil {
				acc = make([]float32, len(y))
			}
			if len(y) != len(acc) {
				panic("taskpkg: internal error: misaligned pair convolvers")
			}
			for k, v := range y {
				acc[k] += v
			}
		}
		outs[j] = acc
	}
	return outs
}

// Flush drains every pair convolver at end of stream. Each output channel
// is returned at its exact length: total input samples plus the longest
// tail among its pairs. See Convolver.Flush.
func (m *Matrix) Flush() [][]float32 {
	outs := make([][]float32, m.numOut)
	for j := range m.convs {
		var acc []float32
		for _, c := range m.convs[j] {
			y := c.Flush()
			if len(y) > len(acc) {
				acc = append(acc, make([]float32, len(y)-len(acc))...)
			}
			for k, v := range y {
				acc[k] += v
			}
		}
		outs[j] = acc
	}
	return outs
}

// Reset explicitly clears the state of every pair convolver, keeping the
// installed IRs. See Convolver.Reset.
func (m *Matrix) Reset() {
	for j := range m.convs {
		for _, c := range m.convs[j] {
			c.Reset()
		}
	}
}
