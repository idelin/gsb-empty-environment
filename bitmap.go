// Package starjoin implements a star-schema join optimization:
// dimension-table filters are rewritten into in-memory bitmap
// join filters that are applied to the fact table in a single
// multi-dimensional pass before the real join happens.
package starjoin

// Bitmap is a compact bit set backed by a uint64 slice.
type Bitmap struct {
	words []uint64
	n     uint64
	count uint64
}

// NewBitmap returns a Bitmap able to hold n bits.
func NewBitmap(n uint64) *Bitmap {
	if n == 0 {
		n = 1
	}
	return &Bitmap{words: make([]uint64, (n+63)/64), n: n}
}

// Set marks bit i. Indices >= Size() are ignored.
func (b *Bitmap) Set(i uint64) {
	if i >= b.n {
		return
	}
	w := i / 64
	mask := uint64(1) << (i % 64)
	if b.words[w]&mask == 0 {
		b.words[w] |= mask
		b.count++
	}
}

// Test reports whether bit i is set.
func (b *Bitmap) Test(i uint64) bool {
	if i >= b.n {
		return false
	}
	return b.words[i/64]&(uint64(1)<<(i%64)) != 0
}

// Count returns the number of set bits.
func (b *Bitmap) Count() uint64 { return b.count }

// Size returns the number of addressable bits.
func (b *Bitmap) Size() uint64 { return b.n }
