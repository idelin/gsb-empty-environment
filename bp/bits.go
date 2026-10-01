package bp

import "math/bits"

// bitvec is a packed bit vector: 1 bit per parenthesis ('(' = 1, ')' = 0).
type bitvec struct {
	words []uint64
	n     int
}

func newBitvec(n int) bitvec {
	return bitvec{words: make([]uint64, (n+63)/64), n: n}
}

func (b *bitvec) set(i int) {
	b.words[i>>6] |= uint64(1) << (uint(i) & 63)
}

func (b *bitvec) get(i int) bool {
	return b.words[i>>6]>>(uint(i)&63)&1 == 1
}

// rank1 counts set bits in positions [0, i).
func (b *bitvec) rank1(i int) int {
	w := i >> 6
	off := uint(i) & 63
	c := 0
	for k := 0; k < w; k++ {
		c += bits.OnesCount64(b.words[k])
	}
	if off > 0 {
		c += bits.OnesCount64(b.words[w] & ((uint64(1) << off) - 1))
	}
	return c
}

func bitsOnes(w uint64) int { return bits.OnesCount64(w) }
