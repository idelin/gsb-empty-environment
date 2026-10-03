package huffsync

// BitString is a sequence of bits packed MSB-first into bytes.
// It is the common currency between encoder, channel (damage) and decoder.
type BitString struct {
	data  []byte
	nbits int
}

// Len returns the number of valid bits.
func (b *BitString) Len() int { return b.nbits }

// At returns the i-th bit.
func (b *BitString) At(i int) bool {
	return b.data[i>>3]&(0x80>>uint(i&7)) != 0
}

// Bytes returns the packed byte representation (last byte zero-padded).
func (b *BitString) Bytes() []byte {
	return append([]byte(nil), b.data...)
}

func (b *BitString) appendBit(bit bool) {
	if b.nbits&7 == 0 {
		b.data = append(b.data, 0)
	}
	if bit {
		b.data[b.nbits>>3] |= 0x80 >> uint(b.nbits&7)
	}
	b.nbits++
}

// appendBits writes the n low-order bits of v, most-significant first.
func (b *BitString) appendBits(v uint64, n int) {
	for i := n - 1; i >= 0; i-- {
		b.appendBit(v>>uint(i)&1 == 1)
	}
}

// DeleteBits simulates a burst bit loss on the channel: it returns a copy
// of b with the bits in [start, start+n) removed. All later bits shift down.
func DeleteBits(b *BitString, start, n int) *BitString {
	out := &BitString{}
	for i := 0; i < b.nbits; i++ {
		if i >= start && i < start+n {
			continue
		}
		out.appendBit(b.At(i))
	}
	return out
}

// FlipBit simulates a single-bit inversion on the channel.
func FlipBit(b *BitString, i int) *BitString {
	out := &BitString{data: append([]byte(nil), b.data...), nbits: b.nbits}
	out.data[i>>3] ^= 0x80 >> uint(i&7)
	return out
}
