package bitlayout

// BitOrder selects how an absolute stream position maps to a bit inside a
// byte of the buffer. It is independent from ByteOrder.
type BitOrder int

const (
	// MSBFirst maps stream position 0 to bit 7 (the most significant bit)
	// of byte 0, position 1 to bit 6, and so on.
	MSBFirst BitOrder = iota
	// LSBFirst maps stream position 0 to bit 0 (the least significant bit)
	// of byte 0, position 1 to bit 1, and so on.
	LSBFirst
)

// ByteOrder selects how the bits of a multi-byte field value are emitted
// into the bit stream. It is independent from BitOrder.
type ByteOrder int

const (
	// BigEndian emits the value most significant bit first.
	BigEndian ByteOrder = iota
	// LittleEndian splits the value into 8-bit groups and emits the least
	// significant group first; each group is emitted most significant bit
	// first. The final group holds the remaining high bits of the value.
	LittleEndian
)

var rev8tab [256]uint8

func init() {
	for i := 0; i < 256; i++ {
		x := uint8(i)
		x = (x&0x55)<<1 | (x&0xAA)>>1
		x = (x&0x33)<<2 | (x&0xCC)>>2
		x = (x&0x0F)<<4 | (x&0xF0)>>4
		rev8tab[i] = x
	}
}

func mask64(n int) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << uint(n)) - 1
}

// bitWriter writes bits into buf at absolute stream positions. Only the
// addressed bits are modified; all other bits of buf are preserved.
type bitWriter struct {
	buf []byte
	bo  BitOrder
}

// put writes the low n bits of val, most significant bit first, starting at
// stream position pos.
func (w *bitWriter) put(pos uint64, val uint64, n int) {
	for n > 0 {
		bi := pos >> 3
		off := uint(pos & 7)
		k := 8 - int(off)
		if k > n {
			k = n
		}
		chunk := uint8((val >> uint(n-k)) & mask64(k))
		if w.bo == MSBFirst {
			shift := 8 - off - uint(k)
			m := uint8(mask64(k)) << shift
			w.buf[bi] = (w.buf[bi] &^ m) | (chunk << shift)
		} else {
			// Stream bit j (j = 0..k-1) carries chunk bit k-1-j and lands on
			// byte bit off+j, i.e. the k-bit chunk is bit-reversed at off.
			rev := rev8tab[chunk] >> (8 - uint(k))
			m := uint8(mask64(k)) << off
			w.buf[bi] = (w.buf[bi] &^ m) | (rev << off)
		}
		pos += uint64(k)
		n -= k
	}
}

// bitReader reads bits from buf at absolute stream positions. nbits is the
// number of valid bits in buf.
type bitReader struct {
	buf   []byte
	nbits uint64
	bo    BitOrder
}

// get reads n bits (1..64) starting at pos, most significant bit first.
// ok is false when the range exceeds the available input.
func (r *bitReader) get(pos uint64, n int) (v uint64, ok bool) {
	if uint64(n) > r.nbits || pos > r.nbits-uint64(n) {
		return 0, false
	}
	for n > 0 {
		bi := pos >> 3
		off := uint(pos & 7)
		k := 8 - int(off)
		if k > n {
			k = n
		}
		var chunk uint8
		if r.bo == MSBFirst {
			shift := 8 - off - uint(k)
			chunk = (r.buf[bi] >> shift) & uint8(mask64(k))
		} else {
			raw := (r.buf[bi] >> off) & uint8(mask64(k))
			chunk = rev8tab[raw] >> (8 - uint(k))
		}
		v = (v << uint(k)) | uint64(chunk)
		pos += uint64(k)
		n -= k
	}
	return v, true
}

// emitField writes the low n bits of v at *pos using the given byte order
// and advances *pos.
func emitField(w *bitWriter, pos *uint64, v uint64, n int, bo ByteOrder) {
	if n <= 0 {
		return
	}
	if bo == BigEndian {
		w.put(*pos, v, n)
		*pos += uint64(n)
		return
	}
	rem, sh := n, 0
	for rem > 0 {
		k := 8
		if rem < 8 {
			k = rem
		}
		w.put(*pos, (v>>uint(sh))&mask64(k), k)
		*pos += uint64(k)
		rem -= k
		sh += 8
	}
}

// readField reads n bits at *pos using the given byte order and advances
// *pos. On short input *pos is left unchanged and ok is false.
func readField(r *bitReader, pos *uint64, n int, bo ByteOrder) (uint64, bool) {
	if n <= 0 {
		return 0, true
	}
	if bo == BigEndian {
		v, ok := r.get(*pos, n)
		if !ok {
			return 0, false
		}
		*pos += uint64(n)
		return v, true
	}
	var v uint64
	p := *pos
	rem, sh := n, 0
	for rem > 0 {
		k := 8
		if rem < 8 {
			k = rem
		}
		c, ok := r.get(p, k)
		if !ok {
			return 0, false
		}
		v |= c << uint(sh)
		p += uint64(k)
		rem -= k
		sh += 8
	}
	*pos = p
	return v, true
}

func alignPad(pos uint64, m int) int {
	r := pos % uint64(m)
	if r == 0 {
		return 0
	}
	return m - int(r)
}
