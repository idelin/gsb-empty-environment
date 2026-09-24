package dodts

// bitWriter appends MSB-first bits to a byte slice. flush pads the final
// byte with zero bits. Bytes are preallocated through ensureBytes and the
// current fill position is tracked explicitly.
type bitWriter struct {
	buf       []byte
	bytePos   int
	cur       byte
	nbits     uint // used bits in cur, 0..7
	totalBits int
}

func (w *bitWriter) reset() {
	w.buf = w.buf[:0]
	w.bytePos = 0
	w.cur = 0
	w.nbits = 0
	w.totalBits = 0
}

// bytes returns the encoded bytes, padding the tail with zero bits.
func (w *bitWriter) bytes() []byte {
	out := make([]byte, w.byteLen())
	copy(out, w.buf[:w.bytePos])
	if w.nbits > 0 {
		out[w.bytePos] = w.cur
	}
	return out
}

func (w *bitWriter) byteLen() int {
	return (w.totalBits + 7) >> 3
}

func (w *bitWriter) writeBits(v uint64, width uint) {
	if width == 0 {
		return
	}
	w.totalBits += int(width)
	for width > 0 {
		if w.bytePos >= len(w.buf) {
			w.buf = append(w.buf, 0)
		}
		space := 8 - w.nbits
		take := width
		if take > space {
			take = space
		}
		shift := width - take
		var chunk byte
		if take == 64 {
			chunk = byte(v >> (shift))
		} else {
			chunk = byte((v >> shift) & ((uint64(1) << take) - 1))
		}
		w.cur |= chunk << (space - take)
		w.nbits += take
		width -= take
		if w.nbits == 8 {
			w.buf[w.bytePos] = w.cur
			w.bytePos++
			w.cur = 0
			w.nbits = 0
		}
	}
}

// bitReader reads MSB-first bits. Reading past the end of the payload
// returns ErrCorrupt so truncated or oversized width codes cannot cause
// unbounded behavior.
type bitReader struct {
	buf   []byte
	pos   int
	cur   byte
	nbits uint // remaining unread bits in cur, 0..8
}

func (r *bitReader) reset(buf []byte) {
	r.buf = buf
	r.pos = 0
	r.cur = 0
	r.nbits = 0
}

func (r *bitReader) readBit() (uint64, error) {
	if r.nbits == 0 {
		if r.pos >= len(r.buf) {
			return 0, ErrCorrupt
		}
		r.cur = r.buf[r.pos]
		r.pos++
		r.nbits = 8
	}
	r.nbits--
	bit := uint64((r.cur >> 7) & 1)
	r.cur <<= 1
	return bit, nil
}

func (r *bitReader) readBits(width uint) (uint64, error) {
	if width == 0 {
		return 0, nil
	}
	if width > 64 {
		return 0, ErrCorrupt
	}
	var v uint64
	for i := uint(0); i < width; i++ {
		b, err := r.readBit()
		if err != nil {
			return 0, err
		}
		v = (v << 1) | b
	}
	return v, nil
}
