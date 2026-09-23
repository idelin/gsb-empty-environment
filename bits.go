package huffcodec

// bitWriter accumulates bits most-significant-bit first into a byte slice.
type bitWriter struct {
	buf   []byte
	acc   uint32
	nbits uint
}

// writeBits appends the low n bits of v, MSB first.
func (w *bitWriter) writeBits(v uint32, n uint) {
	for n > 0 {
		k := 8 - w.nbits
		if k > n {
			k = n
		}
		shift := n - k
		w.acc = w.acc<<k | (v>>shift)&((1<<k)-1)
		w.nbits += k
		n -= k
		if w.nbits == 8 {
			w.buf = append(w.buf, byte(w.acc))
			w.acc = 0
			w.nbits = 0
		}
	}
}

// flush pads the final byte with zero bits.
func (w *bitWriter) flush() {
	if w.nbits > 0 {
		w.buf = append(w.buf, byte(w.acc<<(8-w.nbits)))
		w.acc = 0
		w.nbits = 0
	}
}
