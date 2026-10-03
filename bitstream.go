package tans

// bitWriter appends bits to a byte buffer, LSB-first. The decoder reads the
// stream back in last-in-first-out order (from the end of the buffer), which
// is exactly what the ANS stack discipline requires.
type bitWriter struct {
	buf   []byte
	acc   uint64
	nbits uint
}

// write appends the n low bits of v. n must be <= 56.
func (w *bitWriter) write(v uint64, n uint) {
	w.acc |= (v & ((uint64(1) << n) - 1)) << w.nbits
	w.nbits += n
	for w.nbits >= 8 {
		w.buf = append(w.buf, byte(w.acc))
		w.acc >>= 8
		w.nbits -= 8
	}
}

// flush pads the accumulator to a whole byte with zero bits and returns the
// number of padding bits added (0-7).
func (w *bitWriter) flush() (pad uint) {
	if w.nbits > 0 {
		pad = 8 - w.nbits
		w.buf = append(w.buf, byte(w.acc))
		w.acc = 0
		w.nbits = 0
	}
	return pad
}

// bitReader reads bits back from the end of the buffer (LIFO). Bits past the
// logical start of the stream read as zero, so truncated input cannot panic.
type bitReader struct {
	buf   []byte
	pos   int // index of the next byte to load, counting down
	acc   uint64
	nbits uint
	// starved is set once the stream is exhausted and zero bits are
	// synthesized; a well-formed stream never starves the reader.
	starved bool
}

// newBitReader prepares to read buf backwards. pad is the number of zero
// padding bits the writer added in the high bits of the last byte.
func newBitReader(buf []byte, pad uint) *bitReader {
	r := &bitReader{buf: buf, pos: len(buf)}
	if pad > 0 && len(buf) > 0 {
		r.pos--
		r.acc = uint64(r.buf[r.pos]) & ((uint64(1) << (8 - pad)) - 1)
		r.nbits = 8 - pad
	}
	return r
}

// read pops the n most recently written bits.
func (r *bitReader) read(n uint) uint64 {
	for r.nbits < n {
		if r.pos > 0 {
			r.pos--
			r.acc = r.acc<<8 | uint64(r.buf[r.pos])
		} else {
			r.acc <<= 8
			r.starved = true
		}
		r.nbits += 8
	}
	r.nbits -= n
	v := r.acc >> r.nbits
	r.acc &= (uint64(1) << r.nbits) - 1
	return v
}
