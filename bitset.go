package gatetable

// bitset is a fixed 128-bit set.  MaxPeriod is 96, so one two-word set is
// enough for every tick bitmap in this package.
type bitset struct {
	lo uint64
	hi uint64
}

func (b *bitset) set(i int) {
	if i < 64 {
		b.lo |= uint64(1) << uint(i)
	} else {
		b.hi |= uint64(1) << uint(i-64)
	}
}

func (b bitset) test(i int) bool {
	if i < 64 {
		return b.lo&(uint64(1)<<uint(i)) != 0
	}
	return b.hi&(uint64(1)<<uint(i-64)) != 0
}

func (b bitset) intersects(other bitset) bool {
	return b.lo&other.lo != 0 || b.hi&other.hi != 0
}

func (b *bitset) or(other bitset) {
	b.lo |= other.lo
	b.hi |= other.hi
}

func (b *bitset) andNot(other bitset) {
	b.lo &^= other.lo
	b.hi &^= other.hi
}

// circularBitMask returns the ticks occupied by [start,start+length) modulo
// period.  length must be in [1,period].
func circularBitMask(start, length, period int) bitset {
	var out bitset
	for i := 0; i < length; i++ {
		out.set((start + i) % period)
	}
	return out
}
