package bitpack

// bitWriter packs values LSB-first into a byte stream.
// Handles widths 0..64 without shift overflow.
type bitWriter struct {
	buf   []byte
	cur   uint64 // current partial byte (only low 8 bits used)
	nbits uint   // bits used in cur, 0..7
}

func (w *bitWriter) write(val uint64, n uint) {
	for n > 0 {
		take := 8 - w.nbits
		if take > n {
			take = n
		}
		mask := uint64(1)<<take - 1 // take in 1..8, safe
		w.cur |= (val & mask) << w.nbits
		val >>= take
		n -= take
		w.nbits += take
		if w.nbits == 8 {
			w.buf = append(w.buf, byte(w.cur))
			w.cur = 0
			w.nbits = 0
		}
	}
}

// bytes flushes the final partial byte (zero-padded) and returns the stream.
func (w *bitWriter) bytes() []byte {
	if w.nbits > 0 {
		w.buf = append(w.buf, byte(w.cur))
		w.cur = 0
		w.nbits = 0
	}
	return w.buf
}

// bitReader unpacks LSB-first values from a byte stream.
type bitReader struct {
	buf   []byte
	pos   int  // current byte index
	nbits uint // bits consumed in current byte, 0..7
}

func (r *bitReader) read(n uint) uint64 {
	var v uint64
	var got uint
	for n > 0 {
		take := 8 - r.nbits
		if take > n {
			take = n
		}
		b := uint64(r.buf[r.pos]) >> r.nbits
		mask := uint64(1)<<take - 1
		v |= (b & mask) << got
		got += take
		n -= take
		r.nbits += take
		if r.nbits == 8 {
			r.pos++
			r.nbits = 0
		}
	}
	return v
}
