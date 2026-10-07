package facility

import "math/bits"

// bitset 是一个定长位集合,用于表示需求点覆盖情况。
type bitset struct {
	words []uint64
}

func newBitset(n int) bitset {
	return bitset{words: make([]uint64, (n+63)/64)}
}

func (b bitset) set(i int) {
	b.words[i/64] |= 1 << uint(i%64)
}

func (b bitset) get(i int) bool {
	return b.words[i/64]&(1<<uint(i%64)) != 0
}

// orInto 将 src 按位或进 dst,dst 就地修改。
func orInto(dst, src bitset) {
	for i := range dst.words {
		dst.words[i] |= src.words[i]
	}
}

// weightedCount 返回置位位对应权值之和(每个需求点只计一次,重叠覆盖不重复计分)。
func (b bitset) weightedCount(weights []int64) int64 {
	var total int64
	for w, word := range b.words {
		for word != 0 {
			i := w*64 + bits.TrailingZeros64(word)
			total += weights[i]
			word &= word - 1
		}
	}
	return total
}
