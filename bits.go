package deflate

// bitWriter packs bits LSB-first, as required by RFC 1951.
type bitWriter struct {
	buf []byte
	cur uint32
	n   uint
}

func (w *bitWriter) writeBits(v uint32, n uint) {
	w.cur |= v << w.n
	w.n += n
	for w.n >= 8 {
		w.buf = append(w.buf, byte(w.cur))
		w.cur >>= 8
		w.n -= 8
	}
}

// writeCode writes a Huffman code, most-significant bit first.
func (w *bitWriter) writeCode(code uint16, length uint8) {
	for i := int(length) - 1; i >= 0; i-- {
		w.writeBits(uint32(code>>i)&1, 1)
	}
}

func (w *bitWriter) alignByte() {
	if w.n > 0 {
		w.buf = append(w.buf, byte(w.cur))
		w.cur = 0
		w.n = 0
	}
}

// writeBytes appends raw bytes; the writer must be byte-aligned.
func (w *bitWriter) writeBytes(b []byte) {
	w.buf = append(w.buf, b...)
}

func (w *bitWriter) bytes() []byte {
	w.alignByte()
	return w.buf
}

// bitReader reads bits LSB-first from a byte slice.
type bitReader struct {
	data []byte
	pos  int
	bit  uint
	err  error
}

func (r *bitReader) readBit() uint32 {
	if r.err != nil {
		return 0
	}
	if r.pos >= len(r.data) {
		r.err = errEOF
		return 0
	}
	b := (r.data[r.pos] >> r.bit) & 1
	r.bit++
	if r.bit == 8 {
		r.bit = 0
		r.pos++
	}
	return uint32(b)
}

func (r *bitReader) readBits(n uint) uint32 {
	var v uint32
	for i := uint(0); i < n; i++ {
		v |= r.readBit() << i
	}
	return v
}

func (r *bitReader) alignByte() {
	if r.bit != 0 {
		r.bit = 0
		r.pos++
	}
}
