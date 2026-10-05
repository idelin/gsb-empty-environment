package main

// bitset is an explicit multi-word bitmap. It makes no assumption
// that the number of files fits in 64 bits.
type bitset struct {
	words []uint64
}

func newBitset(n int) bitset {
	return bitset{words: make([]uint64, (n+63)/64)}
}

func (b bitset) set(i int) {
	b.words[i>>6] |= uint64(1) << (uint(i) & 63)
}

func (b bitset) get(i int) bool {
	return b.words[i>>6]&(uint64(1)<<(uint(i)&63)) != 0
}

func (b bitset) or(o bitset) bitset {
	for i := range b.words {
		b.words[i] |= o.words[i]
	}
	return b
}

func (b bitset) clone() bitset {
	w := make([]uint64, len(b.words))
	copy(w, b.words)
	return bitset{words: w}
}

func (b bitset) forEach(f func(i int)) {
	for i := range b.words {
		w := b.words[i]
		for w != 0 {
			t := w & (^w + 1) // lowest set bit
			var j int
			for x := t; x > 1; x >>= 1 {
				j++
			}
			f(i*64 + j)
			w &^= t
		}
	}
}
