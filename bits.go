package bitlayout

// This file implements the two independent ordering axes:
//
//   - BitOrder decides how the bits of one group are emitted into the
//     stream (msb: most-significant bit first; lsb: least-significant
//     first) and how stream positions map to byte bits.
//   - ByteOrder decides the order of the 8-bit groups that make up a
//     field wider than 8 bits (big: most-significant group first;
//     little: least-significant group first).
//
// A field of n bits is split into groups of 8 bits, except the most
// significant group which holds the remaining ((n-1)%8)+1 bits.

// bitWriter emits bits into a caller-supplied buffer. Only the bits it
// touches are modified; all other bits of the buffer are preserved.
type bitWriter struct {
	buf []byte
	msb bool
	pos int64
}

func newBitWriter(buf []byte, order BitOrder) *bitWriter {
	return &bitWriter{buf: buf, msb: order == MSBFirst}
}

// group writes the low g bits of v as one group, honouring bit order.
// Bits are set with OR; callers start from a validated, zeroed region.
func (w *bitWriter) group(v uint64, g int) {
	for g > 0 {
		off := uint(w.pos & 7)
		k := 8 - int(off)
		if k > g {
			k = g
		}
		var chunk byte
		if w.msb {
			// Take the top k of the remaining g bits; the first-emitted
			// bit lands on the lowest free stream position of the byte.
			chunk = byte(v>>uint(g-k)) & byte(mask(k))
			w.buf[w.pos>>3] |= chunk << (8 - off - uint(k))
		} else {
			// First-emitted bit is the LSB and lands on bit `off`.
			chunk = byte(v) & byte(mask(k))
			w.buf[w.pos>>3] |= chunk << off
			v >>= uint(k)
		}
		w.pos += int64(k)
		g -= k
	}
}

// field writes the low n bits of v, honouring byte order.
func (w *bitWriter) field(v uint64, n int, bo ByteOrder) {
	if bo == LittleEndian {
		for rem, shift := n, 0; rem > 0; {
			g := min(8, rem)
			w.group(v>>uint(shift), g)
			shift += g
			rem -= g
		}
		return
	}
	// big: most significant group first
	first := (n-1)%8 + 1
	w.group(v>>uint(n-first), first)
	for rem := n - first; rem > 0; rem -= 8 {
		w.group(v>>uint(rem-8), 8)
	}
}

// bitReader reads bits from a buffer. avail is the number of valid bits.
type bitReader struct {
	buf   []byte
	msb   bool
	pos   int64
	avail int64
}

func newBitReader(buf []byte, order BitOrder, avail int64) *bitReader {
	return &bitReader{buf: buf, msb: order == MSBFirst, avail: avail}
}

func (r *bitReader) remaining() int64 { return r.avail - r.pos }

// bitAt returns the stream bit at an absolute position. The caller must
// guarantee pos < avail.
func (r *bitReader) bitAt(pos int64) uint64 {
	b := r.buf[pos>>3]
	if r.msb {
		if b&(0x80>>(uint(pos)&7)) != 0 {
			return 1
		}
		return 0
	}
	if b&(1<<(uint(pos)&7)) != 0 {
		return 1
	}
	return 0
}

// group reads one group of g bits, honouring bit order.
func (r *bitReader) group(g int) uint64 {
	var v uint64
	if r.msb {
		for j := 0; j < g; j++ {
			v = v<<1 | r.bitAt(r.pos)
			r.pos++
		}
		return v
	}
	for j := 0; j < g; j++ {
		v |= r.bitAt(r.pos) << uint(j)
		r.pos++
	}
	return v
}

// field reads n bits honouring byte order. Callers must guarantee
// remaining() >= n.
func (r *bitReader) field(n int, bo ByteOrder) uint64 {
	if bo == LittleEndian {
		var v uint64
		for rem, shift := n, 0; rem > 0; {
			g := min(8, rem)
			v |= r.group(g) << uint(shift)
			shift += g
			rem -= g
		}
		return v
	}
	first := (n-1)%8 + 1
	v := r.group(first)
	for rem := n - first; rem > 0; rem -= 8 {
		v = v<<8 | r.group(8)
	}
	return v
}

// mask returns a value with the low n bits set (n in 0..64).
func mask(n int) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<uint(n) - 1
}
