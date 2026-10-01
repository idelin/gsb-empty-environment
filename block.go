package bitpack

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// Block payload layout (all multi-byte fixed fields little-endian):
//
//	flags     1B   bit0: exception positions stored as bitmap
//	bitWidth  1B   0..64, bit width of packed deltas (0 allowed: constant block)
//	count     4B   uint32, explicit value count (tail block may be shorter)
//	base      uvarint(zigzag(min value in block))
//	excCount  uvarint
//	positions bitmap ceil(count/8) bytes, or delta-encoded uvarints (cheaper wins)
//	excValues excCount uvarints, full deltas (never truncated)
//	packed    ceil(count*bitWidth/8) bytes, LSB-first; exception slots are zero
//
// A delta is value-base computed in uint64, so the full int64 range
// (including MinInt64..MaxInt64 spans) is representable without overflow.
const (
	flagPosBitmap uint8 = 1 << 0
	maxBitWidth         = 64
	maxBlockCount       = 1 << 28 // hard sanity cap for decoded block length
)

var (
	errBadBlock   = errors.New("bitpack: malformed block")
	errPseudoLen  = errors.New("bitpack: declared length inconsistent with payload size")
	errBadVersion = errors.New("bitpack: unsupported version")
)

func zigzagEncode(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }
func zigzagDecode(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

func uvarintSize(x uint64) int {
	n := 1
	for x >= 0x80 {
		x >>= 7
		n++
	}
	return n
}

// blockPlan is the exact, cost-optimal encoding choice for one block.
type blockPlan struct {
	width  int
	base   int64
	excPos []int
	excVal []uint64
	bitmap bool
	cost   int // exact payload size in bytes
}

// planBlock evaluates every candidate bit width that can be optimal
// (0, 64, and each distinct delta bit-length) and picks the exact
// minimum-cost encoding. All header, position-encoding and packed
// bytes are counted in the objective.
func planBlock(deltas []uint64, base int64) blockPlan {
	n := len(deltas)
	var present [maxBitWidth + 1]bool
	for _, d := range deltas {
		present[bits.Len64(d)] = true
	}

	best := blockPlan{base: base, cost: -1}
	try := func(w int) {
		var excPos []int
		var excVal []uint64
		for i, d := range deltas {
			if w < maxBitWidth && bits.Len64(d) > w {
				excPos = append(excPos, i)
				excVal = append(excVal, d)
			}
		}
		cost := 1 + 1 + 4 // flags + bitWidth + count
		cost += uvarintSize(zigzagEncode(base))
		cost += uvarintSize(uint64(len(excPos)))
		varCost := 0
		prev := 0
		for i, p := range excPos {
			gap := p
			if i > 0 {
				gap = p - prev
			}
			prev = p
			varCost += uvarintSize(uint64(gap))
		}
		bmpCost := (n + 7) / 8
		bitmap := false
		if len(excPos) > 0 && bmpCost < varCost {
			bitmap = true
			cost += bmpCost
		} else {
			cost += varCost
		}
		for _, d := range excVal {
			cost += uvarintSize(d)
		}
		cost += (n*w + 7) / 8
		if best.cost < 0 || cost < best.cost {
			best = blockPlan{width: w, base: base, excPos: excPos, excVal: excVal, bitmap: bitmap, cost: cost}
		}
	}

	try(0)
	try(maxBitWidth)
	for w := 1; w < maxBitWidth; w++ {
		if present[w] {
			try(w)
		}
	}
	return best
}

// encodeBlock encodes one block of values into a payload byte slice.
func encodeBlock(vals []int64) []byte {
	base := vals[0]
	for _, v := range vals[1:] {
		if v < base {
			base = v
		}
	}
	deltas := make([]uint64, len(vals))
	for i, v := range vals {
		deltas[i] = uint64(v) - uint64(base) // exact mod 2^64, base is min
	}
	p := planBlock(deltas, base)

	out := make([]byte, 0, p.cost)
	flags := uint8(0)
	if p.bitmap {
		flags |= flagPosBitmap
	}
	out = append(out, flags, byte(p.width))
	var fixed [4]byte
	binary.LittleEndian.PutUint32(fixed[:], uint32(len(vals)))
	out = append(out, fixed[:]...)
	var tmp [binary.MaxVarintLen64]byte
	m := binary.PutUvarint(tmp[:], zigzagEncode(base))
	out = append(out, tmp[:m]...)
	m = binary.PutUvarint(tmp[:], uint64(len(p.excPos)))
	out = append(out, tmp[:m]...)
	if p.bitmap {
		bm := make([]byte, (len(vals)+7)/8)
		for _, pos := range p.excPos {
			bm[pos/8] |= 1 << (uint(pos) % 8)
		}
		out = append(out, bm...)
	} else {
		prev := 0
		for i, pos := range p.excPos {
			gap := pos
			if i > 0 {
				gap = pos - prev
			}
			prev = pos
			m = binary.PutUvarint(tmp[:], uint64(gap))
			out = append(out, tmp[:m]...)
		}
	}
	for _, d := range p.excVal {
		m = binary.PutUvarint(tmp[:], d)
		out = append(out, tmp[:m]...)
	}
	inExc := make(map[int]struct{}, len(p.excPos))
	for _, pos := range p.excPos {
		inExc[pos] = struct{}{}
	}
	bw := &bitWriter{}
	for i, d := range deltas {
		if _, ok := inExc[i]; ok {
			bw.write(0, uint(p.width)) // placeholder, real value stored out-of-band
		} else {
			bw.write(d, uint(p.width))
		}
	}
	out = append(out, bw.bytes()...)
	if len(out) != p.cost {
		panic(fmt.Sprintf("bitpack: cost model off: planned %d, wrote %d", p.cost, len(out)))
	}
	return out
}

// byteCursor is a bounds-checked reader over a payload.
type byteCursor struct {
	buf []byte
	off int
}

func (c *byteCursor) u8() (byte, error) {
	if c.off+1 > len(c.buf) {
		return 0, errBadBlock
	}
	b := c.buf[c.off]
	c.off++
	return b, nil
}

func (c *byteCursor) u32() (uint32, error) {
	if c.off+4 > len(c.buf) {
		return 0, errBadBlock
	}
	v := binary.LittleEndian.Uint32(c.buf[c.off:])
	c.off += 4
	return v, nil
}

func (c *byteCursor) uvarint() (uint64, error) {
	v, n := binary.Uvarint(c.buf[c.off:])
	if n <= 0 {
		return 0, errBadBlock
	}
	c.off += n
	return v, nil
}

func (c *byteCursor) take(n int) ([]byte, error) {
	if n < 0 || c.off+n > len(c.buf) {
		return nil, errBadBlock
	}
	b := c.buf[c.off : c.off+n]
	c.off += n
	return b, nil
}

// decodeBlock decodes a payload. maxCount bounds the declared count.
// The declared count is validated against the actual payload size
// before any output allocation happens: a corrupt "pseudo length"
// causes an error, not a huge allocation.
func decodeBlock(payload []byte, maxCount uint32) ([]int64, error) {
	c := &byteCursor{buf: payload}
	flags, err := c.u8()
	if err != nil {
		return nil, err
	}
	if flags & ^flagPosBitmap != 0 {
		return nil, errBadBlock
	}
	width, err := c.u8()
	if err != nil {
		return nil, err
	}
	if width > maxBitWidth {
		return nil, errBadBlock
	}
	count, err := c.u32()
	if err != nil {
		return nil, err
	}
	if count == 0 || count > maxCount || count > maxBlockCount {
		return nil, errPseudoLen
	}
	baseU, err := c.uvarint()
	if err != nil {
		return nil, err
	}
	base := zigzagDecode(baseU)
	excCount, err := c.uvarint()
	if err != nil {
		return nil, err
	}
	if excCount > uint64(count) {
		return nil, errBadBlock
	}
	if width == maxBitWidth && excCount != 0 {
		return nil, errBadBlock
	}

	excPos := make([]int, 0, excCount)
	if flags&flagPosBitmap != 0 {
		bm, err := c.take((int(count) + 7) / 8)
		if err != nil {
			return nil, err
		}
		for i := 0; i < int(count); i++ {
			if bm[i/8]&(1<<(uint(i)%8)) != 0 {
				excPos = append(excPos, i)
			}
		}
		if uint64(len(excPos)) != excCount {
			return nil, errBadBlock
		}
	} else {
		prev := -1
		for i := uint64(0); i < excCount; i++ {
			gap, err := c.uvarint()
			if err != nil {
				return nil, err
			}
			pos := int(gap)
			if i > 0 {
				pos = prev + int(gap)
				if pos < prev || gap > uint64(maxBlockCount) { // overflow / runaway guard
					return nil, errBadBlock
				}
			}
			if pos <= prev || pos >= int(count) {
				return nil, errBadBlock
			}
			excPos = append(excPos, pos)
			prev = pos
		}
	}
	excVal := make([]uint64, excCount)
	for i := range excVal {
		v, err := c.uvarint()
		if err != nil {
			return nil, err
		}
		excVal[i] = v
	}

	// Validate declared geometry against the real payload size first.
	packedLen := (int64(count)*int64(width) + 7) / 8
	if int64(len(payload)-c.off) != packedLen {
		return nil, errPseudoLen
	}
	packed, err := c.take(int(packedLen))
	if err != nil {
		return nil, err
	}

	// Only now, with a verified length, allocate the output.
	out := make([]int64, count)
	br := &bitReader{buf: packed}
	for i := range out {
		d := br.read(uint(width))
		out[i] = int64(uint64(base) + d)
	}
	for i, pos := range excPos {
		out[pos] = int64(uint64(base) + excVal[i])
	}
	return out, nil
}
