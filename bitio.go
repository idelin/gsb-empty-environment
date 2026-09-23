package slz

// bitWriter packs bits LSB-first into a byte slice.
type bitWriter struct {
	buf   []byte
	cur   uint32
	nbits uint
}

func (w *bitWriter) writeBits(v uint32, n uint) {
	w.cur |= v << w.nbits
	w.nbits += n
	for w.nbits >= 8 {
		w.buf = append(w.buf, byte(w.cur))
		w.cur >>= 8
		w.nbits -= 8
	}
}

func (w *bitWriter) bits() int {
	return len(w.buf)*8 + int(w.nbits)
}

func (w *bitWriter) finish() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, byte(w.cur))
		w.cur = 0
		w.nbits = 0
	}
	return w.buf
}

// bitReader reads bits LSB-first; any overrun is an error.
type bitReader struct {
	data   []byte
	bitPos int
}

func (r *bitReader) readBits(n int) (uint32, error) {
	if n < 0 || r.bitPos+n > len(r.data)*8 {
		return 0, errCorrupt
	}
	var v uint32
	for k := 0; k < n; k++ {
		p := r.bitPos + k
		v |= uint32((r.data[p>>3]>>uint(p&7))&1) << uint(k)
	}
	r.bitPos += n
	return v, nil
}
