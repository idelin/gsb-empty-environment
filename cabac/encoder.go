package cabac

// Arithmetic coder constants: 32-bit interval [low, high] with
// bit-plus-follow carry control, in the spirit of Witten-Neal-Cleary.
const (
	topValue = uint32(0xFFFFFFFF)
	firstQtr = uint32(0x40000000)
	half     = uint32(0x80000000)
	thirdQtr = uint32(0xC0000000)
)

// Encoder is a binary arithmetic encoder driven by per-bin Models.
type Encoder struct {
	low, high uint32
	pending   uint64
	w         bitWriter
}

// NewEncoder returns an encoder with the full initial interval.
func NewEncoder() *Encoder {
	return &Encoder{low: 0, high: topValue}
}

// EncodeBin codes one binary symbol using the given model, then adapts it.
func (e *Encoder) EncodeBin(m Model, bin uint32) {
	rng := uint64(e.high) - uint64(e.low) + 1
	lpsRange := (rng * uint64(m.ProbLPS())) >> 16
	if lpsRange < 1 {
		lpsRange = 1
	}
	if lpsRange >= rng {
		lpsRange = rng - 1
	}
	if bin == m.MPS() {
		e.high = e.low + uint32(rng-lpsRange) - 1
	} else {
		e.low += uint32(rng - lpsRange)
		e.high = e.low + uint32(lpsRange) - 1
	}
	m.Update(bin)
	e.renormalize()
}

func (e *Encoder) renormalize() {
	for {
		if e.high < half {
			e.emitBitPlusFollow(0)
		} else if e.low >= half {
			e.emitBitPlusFollow(1)
			e.low -= half
			e.high -= half
		} else if e.low >= firstQtr && e.high < thirdQtr {
			e.pending++
			e.low -= firstQtr
			e.high -= firstQtr
		} else {
			return
		}
		e.low <<= 1
		e.high = (e.high << 1) | 1
	}
}

func (e *Encoder) emitBitPlusFollow(bit uint32) {
	e.w.writeBit(bit)
	for e.pending > 0 {
		e.w.writeBit(bit ^ 1)
		e.pending--
	}
}

// Bits returns the number of bits committed to the stream so far,
// including pending follow bits. Useful for convergence measurements.
func (e *Encoder) Bits() uint64 { return e.w.total + e.pending }

// Flush terminates the stream and returns the encoded bytes.
func (e *Encoder) Flush() []byte {
	e.pending++
	if e.low < firstQtr {
		e.emitBitPlusFollow(0)
	} else {
		e.emitBitPlusFollow(1)
	}
	return e.w.bytes()
}
