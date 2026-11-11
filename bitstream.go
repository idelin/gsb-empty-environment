package tsblock

// bitWriter packs bits most-significant-bit first into a byte slice.
type bitWriter struct {
	buf  []byte
	cur  byte // bits accumulated for the current byte, left-aligned
	used uint // number of valid bits in cur: 0..7
	n    uint // total number of bits written
}

// writeBits writes the lowest nbits bits of v. nbits must be in [0, 64].
func (w *bitWriter) writeBits(v uint64, nbits uint) {
	for nbits > 0 {
		free := 8 - w.used
		if nbits <= free {
			w.cur |= byte(lowMask(v, nbits)) << (free - nbits)
			w.used += nbits
			w.n += nbits
			if w.used == 8 {
				w.flush()
			}
			return
		}
		// Take the top free bits of the remaining value and complete the byte.
		shift := nbits - free
		w.cur |= byte((v >> shift) & lowMask(^uint64(0), free))
		w.used = 8
		w.n += free
		nbits -= free
		w.flush()
	}
}

// lowMask returns the lowest nbits bits of v, tolerating nbits == 64 where
// 1<<nbits would overflow to zero.
func lowMask(v uint64, nbits uint) uint64 {
	if nbits >= 64 {
		return v
	}
	return v & (1<<nbits - 1)
}

// flush appends the current byte and resets the partial-byte accumulator.
func (w *bitWriter) flush() {
	if w.used == 0 {
		return
	}
	w.buf = append(w.buf, w.cur)
	w.cur = 0
	w.used = 0
}

// bitLen returns the number of bits written so far.
func (w *bitWriter) bitLen() uint { return w.n }

// bytes returns the written bytes, including a final partially filled byte.
func (w *bitWriter) bytes() []byte {
	out := append([]byte(nil), w.buf...)
	if w.used > 0 {
		out = append(out, w.cur)
	}
	return out
}

// bitReader unpacks bits most-significant-bit first from a byte slice.
type bitReader struct {
	buf  []byte
	pos  uint // current byte position
	used uint // bits already consumed in buf[pos]: 0..7
}

// readBits reads nbits bits (0..64) into the low bits of the returned value.
func (r *bitReader) readBits(nbits uint) (uint64, error) {
	var v uint64
	for nbits > 0 {
		if r.pos >= uint(len(r.buf)) {
			return 0, ErrCorrupt
		}
		avail := 8 - r.used
		take := nbits
		if take > avail {
			take = avail
		}
		remaining := avail - take
		chunk := uint64((r.buf[r.pos] >> remaining) & byte(lowMask(^uint64(0), take)))
		v = v<<take | chunk
		r.used += take
		nbits -= take
		if r.used == 8 {
			r.pos++
			r.used = 0
		}
	}
	return v, nil
}

// bitPos reports how many bits have been consumed.
func (r *bitReader) bitPos() uint { return r.pos*8 + r.used }
