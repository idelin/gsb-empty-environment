package tans

// bitWriter appends bits to a byte slice, LSB-first. The encoder writes
// forward; the decoder reads the finished stream backward (see bitReaderRev),
// which is what allows tANS to run the encoder over the input in reverse.
type bitWriter struct {
	buf   []byte
	acc   uint64 // bits not yet flushed to buf
	nbits uint   // number of valid bits in acc
	total uint64 // total bits written
}

// writeBits appends the low k bits of v (k <= 32).
func (w *bitWriter) writeBits(v uint32, k uint) {
	w.acc |= uint64(v&((1<<k)-1)) << w.nbits
	w.nbits += k
	for w.nbits >= 8 {
		w.buf = append(w.buf, byte(w.acc))
		w.acc >>= 8
		w.nbits -= 8
	}
	w.total += uint64(k)
}

// close flushes the final partial byte, zero-padded.
func (w *bitWriter) close() {
	if w.nbits > 0 {
		w.buf = append(w.buf, byte(w.acc))
		w.acc = 0
		w.nbits = 0
	}
}

// bitReaderRev reads a bitstream produced by bitWriter in reverse: the last
// bits written are read first. buf must have at least 4 bytes of slack beyond
// the highest bit position ever accessed (Decode guarantees this).
type bitReaderRev struct {
	buf []byte
	pos uint64 // next bit position to read below
}

// readBits consumes the k bits ending at the current position and returns
// them in the same bit order they were written (k <= 24).
func (r *bitReaderRev) readBits(k uint) uint32 {
	if k == 0 {
		return 0
	}
	r.pos -= uint64(k)
	bytePos := r.pos >> 3
	shift := r.pos & 7
	chunk := uint64(r.buf[bytePos]) |
		uint64(r.buf[bytePos+1])<<8 |
		uint64(r.buf[bytePos+2])<<16
	return uint32(chunk>>shift) & ((1 << k) - 1)
}
