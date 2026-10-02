package main

// bitWriter appends bits MSB-first into a byte slice.
type bitWriter struct {
	buf []byte
	cur byte
	n   uint
}

func (w *bitWriter) WriteBits(v uint32, n int) {
	for i := n - 1; i >= 0; i-- {
		w.cur = w.cur<<1 | byte(v>>uint(i))&1
		w.n++
		if w.n == 8 {
			w.buf = append(w.buf, w.cur)
			w.cur, w.n = 0, 0
		}
	}
}

// Bytes flushes the partial last byte, padding with zero bits.
func (w *bitWriter) Bytes() []byte {
	if w.n > 0 {
		w.buf = append(w.buf, w.cur<<(8-w.n))
		w.cur, w.n = 0, 0
	}
	return w.buf
}

func (w *bitWriter) BitLen() int {
	return len(w.buf)*8 + int(w.n)
}

// bitReader reads bits MSB-first from a byte slice.
type bitReader struct {
	data []byte
	pos  int // absolute bit position
}

func (r *bitReader) BitLen() int { return len(r.data) * 8 }

func (r *bitReader) ReadBit() (uint32, bool) {
	if r.pos >= len(r.data)*8 {
		return 0, false
	}
	b := (r.data[r.pos/8] >> uint(7-uint(r.pos%8))) & 1
	r.pos++
	return uint32(b), true
}

func (r *bitReader) ReadBits(n int) (uint32, bool) {
	var v uint32
	for i := 0; i < n; i++ {
		b, ok := r.ReadBit()
		if !ok {
			return 0, false
		}
		v = v<<1 | b
	}
	return v, true
}

// PeekBits reads n bits starting at absolute bit position pos without moving.
func (r *bitReader) PeekBits(pos, n int) (uint32, bool) {
	if pos < 0 || pos+n > len(r.data)*8 {
		return 0, false
	}
	var v uint32
	for i := 0; i < n; i++ {
		p := pos + i
		v = v<<1 | uint32((r.data[p/8]>>uint(7-uint(p%8)))&1)
	}
	return v, true
}

// AlignByte advances to the next byte boundary.
func (r *bitReader) AlignByte() {
	if r.pos%8 != 0 {
		r.pos += 8 - r.pos%8
	}
}
