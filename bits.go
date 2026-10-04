package main

// bits is a grow-free fixed-size bitset used for requirement coverage masks.
type bits []uint64

func newBits(n int) bits { return make(bits, (n+63)/64) }

func (b bits) set(i int) { b[i>>6] |= 1 << (uint(i) & 63) }

func (b bits) get(i int) bool { return b[i>>6]>>(uint(i)&63)&1 == 1 }

func (b bits) clone() bits {
	r := make(bits, len(b))
	copy(r, b)
	return r
}

func (b bits) andNot(o bits) bits {
	r := make(bits, len(b))
	for i := range b {
		r[i] = b[i] &^ o[i]
	}
	return r
}

func (b bits) or(o bits) bits {
	r := make(bits, len(b))
	for i := range b {
		r[i] = b[i] | o[i]
	}
	return r
}

func (b bits) and(o bits) bits {
	r := make(bits, len(b))
	for i := range b {
		r[i] = b[i] & o[i]
	}
	return r
}

func (b bits) popcount() int {
	n := 0
	for _, w := range b {
		for w != 0 {
			w &= w - 1
			n++
		}
	}
	return n
}

func (b bits) empty() bool {
	for _, w := range b {
		if w != 0 {
			return false
		}
	}
	return true
}

// subsetOf reports whether every bit set in b is also set in o.
func (b bits) subsetOf(o bits) bool {
	for i := range b {
		if b[i]&^o[i] != 0 {
			return false
		}
	}
	return true
}

// first returns the index of the lowest set bit, or -1.
func (b bits) first() int {
	for i, w := range b {
		if w != 0 {
			for j := 0; j < 64; j++ {
				if w>>uint(j)&1 == 1 {
					return i*64 + j
				}
			}
		}
	}
	return -1
}
