// Package arith implements a custom integer arithmetic coding format for
// context-labeled binary event sequences. It is a self-contained design and
// is NOT compatible with any video coding standard (H.264/H.265 CABAC etc).
//
// Coder core: 32-bit Witten-Neal-Cleary style arithmetic coding with
// E1/E2/E3 normalization and delayed carry (pending bits).
//
// Probability model: every context starts with counts (0:1, 1:1). After a
// bit is coded in a context, only that context is updated: the matching
// count is incremented; when the sum of both counts reaches 4096, each
// count is halved rounding up (ceil), which also keeps every count >= 1.
package arith

import "errors"

const (
	// half, firstQtr, thirdQtr partition the 32-bit code range.
	half     = uint32(1) << 31 // 0x80000000
	firstQtr = uint32(1) << 30 // 0x40000000
	thirdQtr = half | firstQtr // 0xC0000000

	// maxTotal is the rescale threshold for context counts.
	maxTotal = 4096
)

// Sentinel errors. Wrapped with fmt.Errorf("%w: ...") at the failure site.
var (
	ErrBadMagic  = errors.New("arith: bad magic")
	ErrTruncated = errors.New("arith: truncated frame")
	ErrTrailing  = errors.New("arith: trailing bytes after frame")
	ErrChecksum  = errors.New("arith: checksum mismatch")
	ErrLimit     = errors.New("arith: limit exceeded")
	ErrContext   = errors.New("arith: invalid context")
	ErrBit       = errors.New("arith: bit value must be 0 or 1")
	ErrState     = errors.New("arith: invalid encoder state")
)

// context holds the adaptive counts of one context label.
type context struct {
	c0, c1 uint16
}

func (c *context) total() uint32 { return uint32(c.c0) + uint32(c.c1) }

// update adapts the context after coding bit. Identical on both sides.
func (c *context) update(bit byte) {
	if bit == 0 {
		c.c0++
	} else {
		c.c1++
	}
	if uint32(c.c0)+uint32(c.c1) >= maxTotal {
		// ceil(x/2) for x >= 1, so counts stay >= 1.
		c.c0 = (c.c0 + 1) / 2
		c.c1 = (c.c1 + 1) / 2
	}
}

// rangeEncoder is the 32-bit arithmetic encoder with delayed carry.
type rangeEncoder struct {
	low, high uint32 // current interval [low, high], inclusive
	pending   uint64 // E3 bits awaiting carry resolution
	out       []byte // emitted payload bytes
	cur       uint8  // partially filled output byte
	nbits     uint8  // number of valid bits in cur
}

func newRangeEncoder() rangeEncoder {
	return rangeEncoder{high: 0xFFFFFFFF}
}

func (e *rangeEncoder) emitBit(b uint8) {
	e.cur = e.cur<<1 | b
	e.nbits++
	if e.nbits == 8 {
		e.out = append(e.out, e.cur)
		e.cur, e.nbits = 0, 0
	}
}

// emitBitPlusFollow emits b, then all pending opposite bits (carry resolution).
func (e *rangeEncoder) emitBitPlusFollow(b uint8) {
	e.emitBit(b)
	for e.pending > 0 {
		e.emitBit(b ^ 1)
		e.pending--
	}
}

// encodeBit narrows [low, high] to the sub-interval of bit in ctx.
// Integer rule: split = low + floor(range*c0/total); bit 0 takes
// [low, split-1], bit 1 takes [split, high]. All intermediate math is
// 64-bit; results are truncated to 32-bit registers by construction.
func (e *rangeEncoder) encodeBit(ctx *context, bit uint8) {
	rng := uint64(e.high) - uint64(e.low) + 1
	split := uint64(e.low) + rng*uint64(ctx.c0)/uint64(ctx.total())
	if bit == 0 {
		e.high = uint32(split - 1)
	} else {
		e.low = uint32(split)
	}
	e.normalize()
}

func (e *rangeEncoder) normalize() {
	for {
		switch {
		case e.high < half: // E1: interval in lower half
			e.emitBitPlusFollow(0)
		case e.low >= half: // E2: interval in upper half
			e.emitBitPlusFollow(1)
			e.low -= half
			e.high -= half
		case e.low >= firstQtr && e.high < thirdQtr: // E3: straddles middle
			e.pending++
			e.low -= firstQtr
			e.high -= firstQtr
		default:
			return
		}
		e.low <<= 1
		e.high = e.high<<1 | 1
	}
}

// finish terminates the stream: emit two bits that pin a value inside the
// final interval, resolving all pending bits, then zero-pad to a byte.
func (e *rangeEncoder) finish() {
	e.pending++
	if e.low < firstQtr {
		e.emitBitPlusFollow(0)
	} else {
		e.emitBitPlusFollow(1)
	}
	if e.nbits > 0 {
		e.out = append(e.out, e.cur<<(8-e.nbits))
		e.cur, e.nbits = 0, 0
	}
}

// rangeDecoder mirrors rangeEncoder exactly.
type rangeDecoder struct {
	low, high, value uint32
	data             []byte
	pos              int
	cur              uint8
	nbits            uint8
}

func newRangeDecoder(data []byte) *rangeDecoder {
	d := &rangeDecoder{high: 0xFFFFFFFF, data: data}
	for i := 0; i < 32; i++ {
		d.value = d.value<<1 | d.nextBit()
	}
	return d
}

// nextBit reads MSB-first; past the end of the payload it returns 0 bits,
// matching the encoder's zero padding.
func (d *rangeDecoder) nextBit() uint32 {
	if d.nbits == 0 {
		if d.pos < len(d.data) {
			d.cur = d.data[d.pos]
			d.pos++
		} else {
			d.cur = 0
		}
		d.nbits = 8
	}
	d.nbits--
	return uint32(d.cur>>d.nbits) & 1
}

func (d *rangeDecoder) decodeBit(ctx *context) uint8 {
	rng := uint64(d.high) - uint64(d.low) + 1
	total := ctx.total()
	// cum = floor(((value-low+1)*total - 1) / range); always in [0, total).
	cum := uint32(((uint64(d.value)-uint64(d.low)+1)*uint64(total) - 1) / rng)
	split := uint64(d.low) + rng*uint64(ctx.c0)/uint64(total)
	var bit uint8
	if cum < uint32(ctx.c0) {
		bit = 0
		d.high = uint32(split - 1)
	} else {
		bit = 1
		d.low = uint32(split)
	}
	for {
		switch {
		case d.high < half:
		case d.low >= half:
			d.value -= half
			d.low -= half
			d.high -= half
		case d.low >= firstQtr && d.high < thirdQtr:
			d.value -= firstQtr
			d.low -= firstQtr
			d.high -= firstQtr
		default:
			return bit
		}
		d.low <<= 1
		d.high = d.high<<1 | 1
		d.value = d.value<<1 | d.nextBit()
	}
}
