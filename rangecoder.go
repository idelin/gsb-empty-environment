// Package rangecoder implements integer range arithmetic coding with an
// adaptive frequency model, an explicit container format (magic, version,
// length, checksum) and a strictly bounded decoder.
//
// The coder is a carry-less 32-bit range coder (after D. Subbotin).
// Encoder and decoder walk the exact same model states, so their
// arithmetic stays perfectly synchronized. Every stream ends with an
// explicit end-of-stream symbol, and Decode additionally enforces a
// caller-supplied output budget so corrupt input can never trigger
// unbounded decoding.
package rangecoder

import "bytes"

const (
	// topValue is the renormalization threshold for the 32-bit range.
	topValue uint32 = 1 << 24

	// numSymbols is 256 byte values plus one end-of-stream marker.
	numSymbols = 257
	// eofSymbol terminates every encoded stream.
	eofSymbol = numSymbols - 1

	// maxTotal triggers rescaling of the adaptive frequency table.
	// It must stay well below topValue so that range/total >= 1 always.
	maxTotal = 1 << 14
	// incr is the frequency increment per observed symbol.
	incr = 4
)

// ---------------------------------------------------------------------------
// Adaptive frequency model
// ---------------------------------------------------------------------------

// model is an adaptive order-0 frequency table shared in identical form by
// encoder and decoder. All frequencies stay >= 1, even after rescaling.
type model struct {
	freq  [numSymbols]uint32
	total uint32
}

func newModel() *model {
	m := &model{}
	for i := range m.freq {
		m.freq[i] = 1
	}
	m.total = numSymbols
	return m
}

// cumFreq returns the cumulative frequency of all symbols below sym.
func (m *model) cumFreq(sym uint32) uint32 {
	var c uint32
	for i := uint32(0); i < sym; i++ {
		c += m.freq[i]
	}
	return c
}

// find maps a cumulative frequency back to its symbol and the cumulative
// frequency below that symbol.
func (m *model) find(cf uint32) (sym uint32, cum uint32) {
	for i := 0; i < numSymbols; i++ {
		f := m.freq[i]
		if cf < cum+f {
			return uint32(i), cum
		}
		cum += f
	}
	// Unreachable: cf is always < total.
	return eofSymbol, cum - m.freq[eofSymbol]
}

// update records one observation of sym, rescaling when the total grows
// too large. Rescaling halves every frequency but never below 1, so every
// known symbol keeps a positive probability.
func (m *model) update(sym uint32) {
	m.freq[sym] += incr
	m.total += incr
	if m.total >= maxTotal {
		m.rescale()
	}
}

func (m *model) rescale() {
	m.total = 0
	for i, f := range m.freq {
		f = (f + 1) >> 1
		m.freq[i] = f
		m.total += f
	}
}

// ---------------------------------------------------------------------------
// Encoder
// ---------------------------------------------------------------------------

type encoder struct {
	out       *bytes.Buffer
	low       uint64 // 33 significant bits: bit 32 is the carry
	rng       uint32
	cache     byte
	cacheSize int
	started   bool
}

func newEncoder(out *bytes.Buffer) *encoder {
	return &encoder{out: out, rng: 0xffffffff}
}

// shiftLow emits the top byte of low, deferring 0xff runs that a later
// carry could still propagate through. The stale initial cache byte is
// never emitted: it was never shifted out of low, so the output stream
// contains exactly the bytes of low and nothing else.
func (e *encoder) shiftLow() {
	carry := byte(e.low >> 32)
	if uint32(e.low) < 0xff000000 || carry != 0 {
		if e.started {
			e.out.WriteByte(e.cache + carry)
		}
		e.started = true
		for ; e.cacheSize > 0; e.cacheSize-- {
			e.out.WriteByte(0xff + carry)
		}
		e.cache = byte(e.low >> 24)
	} else {
		e.cacheSize++
	}
	e.low = uint64(uint32(e.low) << 8)
}

func (e *encoder) encode(cum, freq, total uint32) {
	r := uint64(e.rng) / uint64(total)
	e.low += r * uint64(cum)
	e.rng = uint32(r * uint64(freq))
	for e.rng < topValue {
		e.shiftLow()
		e.rng <<= 8
	}
}

func (e *encoder) flush() {
	for i := 0; i < 4; i++ {
		e.shiftLow()
	}
}

// ---------------------------------------------------------------------------
// Decoder
// ---------------------------------------------------------------------------

type decoder struct {
	in   []byte
	pos  int
	code uint32
	rng  uint32
}

func newDecoder(in []byte) *decoder {
	d := &decoder{in: in, rng: 0xffffffff}
	for i := 0; i < 4; i++ {
		d.code = d.code<<8 | uint32(d.byte())
	}
	return d
}

// byte reads the next input byte, returning 0 past the end so a truncated
// stream decodes deterministically instead of blocking forever.
func (d *decoder) byte() byte {
	if d.pos < len(d.in) {
		b := d.in[d.pos]
		d.pos++
		return b
	}
	return 0
}

func (d *decoder) cumFreq(total uint32) uint32 {
	r := uint64(d.rng) / uint64(total)
	cf := uint64(d.code) / r
	if cf >= uint64(total) {
		cf = uint64(total) - 1 // clamp corrupt/padded input
	}
	return uint32(cf)
}

func (d *decoder) update(cum, freq, total uint32) {
	r := uint64(d.rng) / uint64(total)
	d.code -= uint32(r * uint64(cum))
	d.rng = uint32(r * uint64(freq))
	for d.rng < topValue {
		d.code = d.code<<8 | uint32(d.byte())
		d.rng <<= 8
	}
}
