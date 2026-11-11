package absexec

// bitset is a fixed-size set of byte indices backed by 64-bit words.
type bitset struct {
	words []uint64
	size  int
}

func newBitset(size int) bitset {
	return bitset{words: make([]uint64, (size+63)/64), size: size}
}

func (b bitset) clone() bitset {
	words := make([]uint64, len(b.words))
	copy(words, b.words)
	return bitset{words: words, size: b.size}
}

// set marks every index in the half-open interval [lo, hi).
func (b bitset) set(lo, hi int) {
	if hi <= lo {
		return
	}
	for i := lo; i < hi; i++ {
		b.words[i>>6] |= 1 << uint(i&63)
	}
}

func (b bitset) has(i int) bool {
	return i >= 0 && i < b.size && b.words[i>>6]&(1<<uint(i&63)) != 0
}

// intersectInPlace keeps only the bits present in other.
func (b bitset) intersectInPlace(other bitset) {
	for i := range b.words {
		b.words[i] &= other.words[i]
	}
}

// unionInPlace adds every bit present in other.
func (b bitset) unionInPlace(other bitset) {
	for i := range b.words {
		b.words[i] |= other.words[i]
	}
}

// subsetOf reports whether every bit in b is also set in other.
func (b bitset) subsetOf(other bitset) bool {
	for i := range b.words {
		if b.words[i]&^other.words[i] != 0 {
			return false
		}
	}
	return true
}

func (b bitset) empty() bool {
	for _, w := range b.words {
		if w != 0 {
			return false
		}
	}
	return true
}

// intervals returns the maximal set-bit runs as half-open intervals.
func (b bitset) intervals() []Interval {
	var out []Interval
	i := 0
	for i < b.size {
		if !b.has(i) {
			i++
			continue
		}
		lo := i
		for i < b.size && b.has(i) {
			i++
		}
		out = append(out, Interval{Lo: lo, Hi: i})
	}
	return out
}

// gapIntervals returns the maximal zero-bit runs restricted to [lo, hi).
func (b bitset) gapIntervals(lo, hi int) []Interval {
	var out []Interval
	i := lo
	for i < hi {
		if b.has(i) {
			i++
			continue
		}
		start := i
		for i < hi && !b.has(i) {
			i++
		}
		out = append(out, Interval{Lo: start, Hi: i})
	}
	return out
}
