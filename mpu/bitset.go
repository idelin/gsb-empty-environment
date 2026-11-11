package mpu

// bitSet 是微型地址空间上的地址集合，按 64 位字组织。
type bitSet struct {
	n     int
	words []uint64
}

func newBitSet(size int) bitSet {
	return bitSet{n: size, words: make([]uint64, (size+63)/64)}
}

func (s bitSet) set(i int) {
	if i < 0 || i >= s.n {
		panic("mpu: bitset index out of range")
	}
	s.words[i>>6] |= 1 << uint(i&63)
}

func (s bitSet) setRange(from, to int) {
	for i := from; i < to; i++ {
		s.set(i)
	}
}

func (s bitSet) get(i int) bool {
	if i < 0 || i >= s.n {
		return false
	}
	return s.words[i>>6]&(1<<uint(i&63)) != 0
}

func (s bitSet) clone() bitSet {
	w := make([]uint64, len(s.words))
	copy(w, s.words)
	return bitSet{n: s.n, words: w}
}

// sub 返回 s &^ o，即属于 s 但不属于 o 的地址。
func (s bitSet) sub(o bitSet) bitSet {
	r := newBitSet(s.n)
	for i := range r.words {
		r.words[i] = s.words[i] &^ o.words[i]
	}
	return r
}

// intersects 报告两个集合是否有交集。
func (s bitSet) intersects(o bitSet) bool {
	for i := range s.words {
		if s.words[i]&o.words[i] != 0 {
			return true
		}
	}
	return false
}

func (s bitSet) equals(o bitSet) bool {
	for i := range s.words {
		if s.words[i] != o.words[i] {
			return false
		}
	}
	return true
}

func (s bitSet) count() int {
	c := 0
	for _, w := range s.words {
		c += onesCount64(w)
	}
	return c
}

// first 返回最小置位下标，集合为空时返回 -1。
func (s bitSet) first() int {
	for i, w := range s.words {
		if w != 0 {
			return i*64 + trailingZeros64(w)
		}
	}
	return -1
}

func onesCount64(w uint64) int {
	c := 0
	for w != 0 {
		w &= w - 1
		c++
	}
	return c
}

func trailingZeros64(w uint64) int {
	if w == 0 {
		return 64
	}
	c := 0
	for w&1 == 0 {
		w >>= 1
		c++
	}
	return c
}
