package efano

// bitvec is a growable little-endian bit stream: bit i lives in
// words[i/64] at position i%64 (least-significant bit first).
type bitvec struct {
	nbits uint64
	words []uint64
}

func mask64(n uint) uint64 {
	if n >= 64 {
		return ^uint64(0)
	}
	return (uint64(1) << n) - 1
}

func (b *bitvec) get(i uint64) uint64 {
	return (b.words[i/64] >> (i % 64)) & 1
}

// getBits reads n (0..64) bits starting at bit offset off.
func (b *bitvec) getBits(off uint64, n uint) uint64 {
	if n == 0 {
		return 0
	}
	w := off / 64
	o := uint(off % 64)
	v := b.words[w] >> o
	if o+n > 64 {
		v |= b.words[w+1] << (64 - o)
	}
	return v & mask64(n)
}

func (b *bitvec) appendBit(bit uint64) {
	if b.nbits%64 == 0 {
		b.words = append(b.words, 0)
	}
	if bit != 0 {
		b.words[b.nbits/64] |= uint64(1) << (b.nbits % 64)
	}
	b.nbits++
}

// appendBits appends the n low bits of val, least-significant bit first.
func (b *bitvec) appendBits(val uint64, n uint) {
	if n == 0 {
		return
	}
	val &= mask64(n)
	off := uint(b.nbits % 64)
	if off == 0 {
		b.words = append(b.words, val)
	} else {
		b.words[len(b.words)-1] |= val << off
		if off+n > 64 {
			b.words = append(b.words, val>>(64-off))
		}
	}
	b.nbits += uint64(n)
}

// appendZeros extends the vector with k zero bits (words are zero-filled).
func (b *bitvec) appendZeros(k uint64) {
	if k == 0 {
		return
	}
	b.nbits += k
	need := (b.nbits + 63) / 64
	for uint64(len(b.words)) < need {
		b.words = append(b.words, 0)
	}
}

// bytes returns the little-endian byte image, ceil(nbits/8) long.
func (b *bitvec) bytes() []byte {
	n := (b.nbits + 7) / 8
	out := make([]byte, n)
	for i := uint64(0); i < n; i++ {
		out[i] = byte(b.words[i/8] >> (8 * (i % 8)))
	}
	return out
}

// bitvecFromBytes rebuilds a bitvec of nbits from its byte image.
func bitvecFromBytes(data []byte, nbits uint64) bitvec {
	words := make([]uint64, (nbits+63)/64)
	for i := uint64(0); i < uint64(len(data)); i++ {
		words[i/8] |= uint64(data[i]) << (8 * (i % 8))
	}
	return bitvec{nbits: nbits, words: words}
}

// paddingZero reports whether all bits at positions >= nbits are zero.
func (b *bitvec) paddingZero() bool {
	rem := uint(b.nbits % 64)
	if rem == 0 {
		return true
	}
	return b.words[len(b.words)-1]>>rem == 0
}
