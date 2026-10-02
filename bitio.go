package deflate

// bitWriter packs bits LSB-first, as required by RFC 1951.
type bitWriter struct {
	out   []byte
	buf   uint32
	nbits uint
}

func (w *bitWriter) writeBits(v uint32, n uint) {
	w.buf |= v << w.nbits
	w.nbits += n
	for w.nbits >= 8 {
		w.out = append(w.out, byte(w.buf))
		w.buf >>= 8
		w.nbits -= 8
	}
}

// alignByte flushes the partially filled byte, zero-padding the high bits.
func (w *bitWriter) alignByte() {
	if w.nbits > 0 {
		w.out = append(w.out, byte(w.buf))
		w.buf = 0
		w.nbits = 0
	}
}

func (w *bitWriter) finish() []byte {
	w.alignByte()
	return w.out
}

// bitLen reports the number of bits written so far.
func (w *bitWriter) bitLen() int {
	return len(w.out)*8 + int(w.nbits)
}

// bitReader unpacks bits LSB-first.
type bitReader struct {
	data  []byte
	pos   int
	buf   uint32
	nbits uint
}

func (r *bitReader) readBits(n uint) (uint32, error) {
	for r.nbits < n {
		if r.pos >= len(r.data) {
			return 0, errUnexpectedEOF
		}
		r.buf |= uint32(r.data[r.pos]) << r.nbits
		r.pos++
		r.nbits += 8
	}
	v := r.buf & ((1 << n) - 1)
	r.buf >>= n
	r.nbits -= n
	return v, nil
}

// alignByte discards bits up to the next byte boundary.
func (r *bitReader) alignByte() {
	r.buf = 0
	r.nbits = 0
}
