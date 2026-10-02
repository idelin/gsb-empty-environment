package starjoin

// Bitmap is a dense bit set over the domain [0, n).
type Bitmap struct {
	words []uint64
	n     int
	count int
}

// NewBitmap allocates a bitmap covering n bits.
func NewBitmap(n int) *Bitmap {
	return &Bitmap{words: make([]uint64, (n+63)/64), n: n}
}

// Set marks bit i. Out-of-range indexes are ignored so sparse foreign keys
// never panic the filter.
func (b *Bitmap) Set(i int) {
	if i < 0 || i >= b.n {
		return
	}
	w := i >> 6
	mask := uint64(1) << (uint(i) & 63)
	if b.words[w]&mask == 0 {
		b.words[w] |= mask
		b.count++
	}
}

// Test reports whether bit i is set.
func (b *Bitmap) Test(i int) bool {
	if i < 0 || i >= b.n {
		return false
	}
	return b.words[i>>6]&(uint64(1)<<(uint(i)&63)) != 0
}

// Count returns the number of set bits.
func (b *Bitmap) Count() int { return b.count }

// Size returns the domain size the bitmap covers.
func (b *Bitmap) Size() int { return b.n }
