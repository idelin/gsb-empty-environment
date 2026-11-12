package taskpkg

import "sync"

// pendingIR is an impulse response update waiting for the next block
// boundary.
type pendingIR struct {
	ir   []float32
	fade int
}

// filterLayer holds one partitioned impulse response and its overlap-add
// state. Two layers are alive while a crossfade is in progress.
type filterLayer struct {
	parts [][]complex128 // partition spectra, each of length nfft
	irLen int            // impulse response length in samples
	ola   []float64      // overlap-add tail, length nfft-bsize
}

// Convolver performs uniform partitioned convolution of one input stream
// with a finite impulse response.
//
// The impulse response is split into partitions of BlockSize samples and
// convolved with a single shared FFT size (uniform partitioning), using
// overlap-add reconstruction. Input may be delivered in chunks of any
// length; output becomes available in granules of BlockSize samples and is
// sample-aligned with the input (no additional latency beyond block
// granularity). Input that does not yet fill a block is buffered, never
// dropped; Flush drains buffered partial blocks and the complete reverb
// tail at end of stream.
//
// The IR can be swapped at run time with SetIR. The swap takes effect on
// the next processed block boundary; during the crossfade the old filter
// keeps running on its preserved history so its tail rings out and is
// faded, never cleared abruptly. State is only cleared by an explicit
// Reset.
//
// A Convolver is not safe for concurrent use, except that SetIR may be
// called from another goroutine than Process/Flush.
type Convolver struct {
	bsize int // partition / hop size in samples
	nfft  int // FFT size, nextPow2(2*bsize)

	mu      sync.Mutex // guards pending
	pending *pendingIR

	fdl       [][]complex128 // input block spectra, index 0 = newest
	cur       *filterLayer
	old       *filterLayer // non-nil while crossfading
	fadeTotal int
	fadePos   int

	inBuf   []float32 // partial-block input buffer
	outBuf  []float32 // produced but not yet collected output
	inTotal int64     // real input samples fed so far
	emitted int64     // output samples handed out so far
	flushed bool

	zeroBlock []float32
	ySpec     []complex128
	outA      []float64
	outB      []float64
}

// NewConvolver returns a Convolver with the given partition block size and
// initial impulse response (nil or empty is allowed and produces silence).
func NewConvolver(blockSize int, ir []float32) *Convolver {
	if blockSize < 1 {
		panic("taskpkg: block size must be positive")
	}
	c := &Convolver{
		bsize: blockSize,
		nfft:  nextPow2(2 * blockSize),
		cur:   &filterLayer{},
	}
	c.ySpec = make([]complex128, c.nfft)
	c.outA = make([]float64, blockSize)
	c.outB = make([]float64, blockSize)
	c.zeroBlock = make([]float32, blockSize)
	c.setLayer(c.cur, ir)
	c.growFDL()
	return c
}

// BlockSize returns the partition block size in samples.
func (c *Convolver) BlockSize() int { return c.bsize }

// TailLength returns the number of trailing output samples the current
// filter state produces after the last input sample, i.e. IR length - 1
// (the longer of the two IRs while a crossfade is in progress).
func (c *Convolver) TailLength() int { return c.tailLen() }

// SetIR schedules an impulse response update. The new IR takes effect on
// the next processed block boundary. The outputs of the old and new filters
// are crossfaded linearly over fadeSamples output samples; the old filter
// keeps its input history so its tail is preserved until the fade
// explicitly ends. fadeSamples <= 1 switches immediately at the boundary
// (the old tail is cut, which is the caller's explicit choice).
//
// Calling SetIR again before a previous update was applied replaces the
// pending update. Calling it during an ongoing crossfade ends that fade
// early at the next boundary.
func (c *Convolver) SetIR(ir []float32, fadeSamples int) {
	if fadeSamples < 0 {
		fadeSamples = 0
	}
	cp := append([]float32(nil), ir...)
	c.mu.Lock()
	c.pending = &pendingIR{ir: cp, fade: fadeSamples}
	c.mu.Unlock()
}

// Process feeds input samples and returns the output samples that have
// become available. The returned length is a multiple of the block size;
// input that does not complete a block is buffered internally and is never
// dropped. The output stream is sample-aligned with the input stream.
func (c *Convolver) Process(input []float32) []float32 {
	c.inBuf = append(c.inBuf, input...)
	c.inTotal += int64(len(input))
	n := 0
	for len(c.inBuf)-n >= c.bsize {
		c.processBlock(c.inBuf[n : n+c.bsize])
		n += c.bsize
	}
	if n > 0 {
		c.inBuf = append(c.inBuf[:0], c.inBuf[n:]...)
	}
	return c.drain(len(c.outBuf))
}

// Flush signals end of stream: any buffered partial block is zero-padded
// and processed, and the complete remaining tail (TailLength samples) is
// produced. The returned slice ends exactly at the last sample of the
// convolution, so the total number of samples returned by Process and
// Flush together is len(input) + TailLength. After Flush the convolver
// should be considered finished; call Reset before reusing it.
func (c *Convolver) Flush() []float32 {
	if c.flushed {
		return nil
	}
	c.flushed = true
	target := c.inTotal + int64(c.tailLen())
	if c.inTotal == 0 {
		target = 0
	}
	if len(c.inBuf) > 0 {
		pad := make([]float32, c.bsize)
		copy(pad, c.inBuf)
		c.inBuf = c.inBuf[:0]
		c.processBlock(pad)
	}
	for c.emitted+int64(len(c.outBuf)) < target {
		c.processBlock(c.zeroBlock)
	}
	return c.drain(int(target - c.emitted))
}

// Reset explicitly clears all state: input history, filter tails, buffered
// samples and any pending IR update. The current IR is kept. This is the
// only operation that discards the reverb tail, and it does so only when
// explicitly called.
func (c *Convolver) Reset() {
	c.mu.Lock()
	c.pending = nil
	c.mu.Unlock()
	for i := range c.fdl {
		clear(c.fdl[i])
	}
	clear(c.cur.ola)
	c.old = nil
	c.fadeTotal, c.fadePos = 0, 0
	c.inBuf = c.inBuf[:0]
	c.outBuf = c.outBuf[:0]
	c.inTotal, c.emitted = 0, 0
	c.flushed = false
}

func (c *Convolver) drain(n int) []float32 {
	if n > len(c.outBuf) {
		n = len(c.outBuf)
	}
	out := make([]float32, n)
	copy(out, c.outBuf[:n])
	c.outBuf = append(c.outBuf[:0], c.outBuf[n:]...)
	c.emitted += int64(n)
	return out
}

func (c *Convolver) tailLen() int {
	l := c.cur.irLen
	if c.old != nil && c.old.irLen > l {
		l = c.old.irLen
	}
	if l == 0 {
		return 0
	}
	return l - 1
}

func (c *Convolver) setLayer(l *filterLayer, ir []float32) {
	l.irLen = len(ir)
	l.parts = partitionIR(ir, c.bsize, c.nfft)
	l.ola = make([]float64, c.nfft-c.bsize)
}

// processBlock convolves one full block of input and appends bsize output
// samples to outBuf. input must have length bsize.
func (c *Convolver) processBlock(input []float32) {
	c.mu.Lock()
	if c.pending != nil {
		c.applyPending()
	}
	c.mu.Unlock()

	if c.old != nil && c.fadePos >= c.fadeTotal {
		c.old = nil
	}

	curActive := len(c.cur.parts) > 0
	oldActive := c.old != nil && len(c.old.parts) > 0
	if curActive || oldActive {
		c.pushFDL(input)
	}

	if curActive {
		c.convolveBlock(c.cur, c.outA)
	} else {
		clear(c.outA)
	}

	if c.old == nil {
		for i := 0; i < c.bsize; i++ {
			c.outBuf = append(c.outBuf, float32(c.outA[i]))
		}
		return
	}

	if oldActive {
		c.convolveBlock(c.old, c.outB)
	} else {
		clear(c.outB)
	}
	for i := 0; i < c.bsize; i++ {
		var s float64
		if c.fadePos < c.fadeTotal {
			g := fadeGain(c.fadePos, c.fadeTotal)
			s = c.outA[i]*g + c.outB[i]*(1-g)
			c.fadePos++
		} else {
			s = c.outA[i]
		}
		c.outBuf = append(c.outBuf, float32(s))
	}
	if c.fadePos >= c.fadeTotal {
		c.old = nil
	}
}

// applyPending installs the pending IR as the current layer and moves the
// previous one to the fading-out layer. The shared FDL (input history) is
// left untouched, so the old tail keeps ringing and the new IR immediately
// sees past input. Must be called with c.mu held.
func (c *Convolver) applyPending() {
	p := c.pending
	c.pending = nil
	l := &filterLayer{}
	c.setLayer(l, p.ir)
	if p.fade > 1 && c.cur.irLen > 0 {
		c.old = c.cur
		c.fadeTotal = p.fade
		c.fadePos = 0
	} else {
		c.old = nil
		c.fadeTotal = 0
		c.fadePos = 0
	}
	c.cur = l
	c.growFDL()
	c.rebuildOLA(l)
}

// rebuildOLA reconstructs the overlap-add tail buffer of a freshly
// installed layer by replaying the most recent frames against the shared
// FDL. Without this the new IR would silently lose the spill that its
// previous frames would have contributed to the upcoming output block,
// truncating the convolution of already-received input. Must be called
// before the next pushFDL, while the FDL still holds the previous block's
// state.
func (c *Convolver) rebuildOLA(l *filterLayer) {
	if len(l.parts) == 0 || len(c.fdl) == 0 {
		return
	}
	spill := c.nfft - c.bsize
	frames := (spill + c.bsize - 1) / c.bsize
	for j := frames; j >= 1; j-- {
		y := c.ySpec
		clear(y)
		for k := 0; k < len(l.parts); k++ {
			idx := j - 1 + k
			if idx >= len(c.fdl) {
				break
			}
			f := c.fdl[idx]
			p := l.parts[k]
			for i := 0; i < c.nfft; i++ {
				y[i] += f[i] * p[i]
			}
		}
		fft(y, true)
		copy(l.ola, l.ola[c.bsize:])
		clear(l.ola[spill-c.bsize:])
		for i := 0; i < spill; i++ {
			l.ola[i] += real(y[c.bsize+i])
		}
	}
}

// pushFDL computes the spectrum of one input block and pushes it onto the
// frequency delay line, reusing the oldest entry's backing array.
func (c *Convolver) pushFDL(input []float32) {
	slot := c.fdl[len(c.fdl)-1]
	copy(c.fdl[1:], c.fdl[:len(c.fdl)-1])
	c.fdl[0] = slot
	clear(slot)
	for i, s := range input {
		slot[i] = complex(float64(s), 0)
	}
	fft(slot, false)
}

// convolveBlock computes one output block of a layer from the shared FDL
// and applies overlap-add with the layer's tail buffer.
func (c *Convolver) convolveBlock(l *filterLayer, out []float64) {
	y := c.ySpec
	clear(y)
	for k := 0; k < len(l.parts); k++ {
		f := c.fdl[k]
		p := l.parts[k]
		for i := 0; i < c.nfft; i++ {
			y[i] += f[i] * p[i]
		}
	}
	fft(y, true)
	for i := 0; i < c.bsize; i++ {
		out[i] = real(y[i]) + l.ola[i]
	}
	spill := c.nfft - c.bsize
	copy(l.ola, l.ola[c.bsize:])
	clear(l.ola[spill-c.bsize:])
	for i := 0; i < spill; i++ {
		l.ola[i] += real(y[c.bsize+i])
	}
}

func (c *Convolver) growFDL() {
	need := len(c.cur.parts)
	if c.old != nil && len(c.old.parts) > need {
		need = len(c.old.parts)
	}
	for len(c.fdl) < need {
		c.fdl = append(c.fdl, make([]complex128, c.nfft))
	}
}

// partitionIR splits ir into blockSize-sample partitions and returns their
// nfft-point spectra.
func partitionIR(ir []float32, blockSize, nfft int) [][]complex128 {
	if len(ir) == 0 {
		return nil
	}
	nparts := (len(ir) + blockSize - 1) / blockSize
	parts := make([][]complex128, nparts)
	for k := 0; k < nparts; k++ {
		buf := make([]complex128, nfft)
		lo := k * blockSize
		hi := min(lo+blockSize, len(ir))
		for i := lo; i < hi; i++ {
			buf[i-lo] = complex(float64(ir[i]), 0)
		}
		fft(buf, false)
		parts[k] = buf
	}
	return parts
}

// fadeGain returns the new-filter gain at fade position pos of total
// samples: a linear ramp from exactly 0 to exactly 1, continuous at both
// ends of the fade region.
func fadeGain(pos, total int) float64 {
	if total <= 1 {
		return 1
	}
	return float64(pos) / float64(total-1)
}
