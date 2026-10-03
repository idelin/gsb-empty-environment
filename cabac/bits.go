package cabac

// bitWriter appends bits MSB-first into a byte slice.
type bitWriter struct {
	buf   []byte
	cur   byte
	nbits uint // bits filled in cur
	total uint64
}

func (w *bitWriter) writeBit(b uint32) {
	w.cur <<= 1
	if b&1 != 0 {
		w.cur |= 1
	}
	w.nbits++
	w.total++
	if w.nbits == 8 {
		w.buf = append(w.buf, w.cur)
		w.cur = 0
		w.nbits = 0
	}
}

// bytes pads the final partial byte with zero bits and returns the stream.
func (w *bitWriter) bytes() []byte {
	if w.nbits > 0 {
		w.cur <<= 8 - w.nbits
		w.buf = append(w.buf, w.cur)
		w.cur = 0
		w.nbits = 0
	}
	return w.buf
}

// bitReader consumes bits MSB-first; reads past the end return zero bits,
// which is exactly what the arithmetic decoder needs after encoder flush.
type bitReader struct {
	buf   []byte
	pos   int
	nbits uint
}

func (r *bitReader) readBit() uint32 {
	if r.pos >= len(r.buf) {
		return 0
	}
	b := (r.buf[r.pos] >> (7 - r.nbits)) & 1
	r.nbits++
	if r.nbits == 8 {
		r.pos++
		r.nbits = 0
	}
	return uint32(b)
}
