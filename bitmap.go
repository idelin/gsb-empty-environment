package main

// bitmap is an explicit multi-word bit set over file indices. It does not
// assume any upper bound (such as 64) on the number of files.
type bitmap struct {
	w []uint64
}

func newBitmap(n int) bitmap {
	return bitmap{w: make([]uint64, (n+63)/64)}
}

func (b bitmap) set(i int) {
	b.w[i>>6] |= uint64(1) << (uint(i) & 63)
}

func (b bitmap) clear(i int) {
	b.w[i>>6] &^= uint64(1) << (uint(i) & 63)
}

func (b bitmap) get(i int) bool {
	return b.w[i>>6]&(uint64(1)<<(uint(i)&63)) != 0
}

func (b bitmap) clone() bitmap {
	w := make([]uint64, len(b.w))
	copy(w, b.w)
	return bitmap{w: w}
}

func (b bitmap) orIn(o bitmap) {
	for i := range b.w {
		b.w[i] |= o.w[i]
	}
}

func (b bitmap) intersects(o bitmap) bool {
	for i := range b.w {
		if b.w[i]&o.w[i] != 0 {
			return true
		}
	}
	return false
}

// subsetOf reports whether every bit of b is also set in o.
func (b bitmap) subsetOf(o bitmap) bool {
	for i := range b.w {
		if b.w[i]&^o.w[i] != 0 {
			return false
		}
	}
	return true
}

// forEachSet calls f for every set bit index, in increasing order.
func (b bitmap) forEachSet(n int, f func(i int)) {
	for i := 0; i < n; i++ {
		if b.get(i) {
			f(i)
		}
	}
}
