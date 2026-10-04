package corpustrim

import "math/bits"

// bitset is a fixed-length set of element indices backed by machine words.
type bitset struct {
	w []uint64
}

func newBitset(n int) bitset {
	return bitset{w: make([]uint64, (n+63)/64)}
}

func (b bitset) set(i int) {
	b.w[i>>6] |= 1 << (uint(i) & 63)
}

func (b bitset) test(i int) bool {
	return b.w[i>>6]>>(uint(i)&63)&1 == 1
}

func (b bitset) clone() bitset {
	w := make([]uint64, len(b.w))
	copy(w, b.w)
	return bitset{w: w}
}

func (b bitset) orInPlace(o bitset) {
	for i := range b.w {
		b.w[i] |= o.w[i]
	}
}

// andNot returns a new bitset with the elements of o removed from b.
func (b bitset) andNot(o bitset) bitset {
	r := b.clone()
	for i := range r.w {
		r.w[i] &^= o.w[i]
	}
	return r
}

func (b bitset) popcount() int {
	c := 0
	for _, x := range b.w {
		c += bits.OnesCount64(x)
	}
	return c
}

// popcountAnd returns |b ∩ o| without allocating.
func (b bitset) popcountAnd(o bitset) int {
	c := 0
	for i := range b.w {
		c += bits.OnesCount64(b.w[i] & o.w[i])
	}
	return c
}

func (b bitset) empty() bool {
	for _, x := range b.w {
		if x != 0 {
			return false
		}
	}
	return true
}

func (b bitset) subsetOf(o bitset) bool {
	for i := range b.w {
		if b.w[i]&^o.w[i] != 0 {
			return false
		}
	}
	return true
}

// bits calls fn with every set index, in increasing order.
func (b bitset) foreach(fn func(i int)) {
	for wi, x := range b.w {
		for x != 0 {
			t := x & (-x)
			fn(wi<<6 + bits.TrailingZeros64(x))
			x ^= t
		}
	}
}
