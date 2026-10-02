// Package dagindex implements a DAG reachability bitmap indexer.
//
// Nodes are organized by topological levels. Each node carries a compressed
// bitmap of its transitive descendant set (including itself), so a
// reachability query Reachable(u, v) is a single bit test: O(1).
//
// Bitmaps are compressed with a sparse-word scheme: the bit space is split
// into 64-bit words and only non-zero words are stored, which keeps memory
// strictly proportional to the number of reachable pairs rather than N^2/64.
package dagindex

// Bitmap is a compressed sparse bitmap. Only non-zero 64-bit words are kept.
type Bitmap struct {
	words map[uint32]uint64
	bits  int // cached popcount, -1 means dirty
}

// NewBitmap returns an empty compressed bitmap.
func NewBitmap() *Bitmap {
	return &Bitmap{words: make(map[uint32]uint64), bits: 0}
}

// Set sets bit i. It reports whether the bit changed.
func (b *Bitmap) Set(i uint32) bool {
	w, off := i>>6, i&63
	old := b.words[w]
	nw := old | 1<<off
	if nw == old {
		return false
	}
	b.words[w] = nw
	b.bits = -1
	return true
}

// Test reports whether bit i is set. O(1).
func (b *Bitmap) Test(i uint32) bool {
	return b.words[i>>6]>>(i&63)&1 == 1
}

// Or unions src into b word by word (bit-parallel, 64 bits per step).
// It reports whether any bit of b changed.
func (b *Bitmap) Or(src *Bitmap) bool {
	changed := false
	for w, sw := range src.words {
		old := b.words[w]
		if nw := old | sw; nw != old {
			b.words[w] = nw
			changed = true
		}
	}
	if changed {
		b.bits = -1
	}
	return changed
}

// Count returns the number of set bits.
func (b *Bitmap) Count() int {
	if b.bits < 0 {
		c := 0
		for _, w := range b.words {
			c += popcount64(w)
		}
		b.bits = c
	}
	return b.bits
}

// Words returns the number of non-zero 64-bit words currently stored.
// This is the actual memory footprint of the bitmap.
func (b *Bitmap) Words() int {
	return len(b.words)
}

// All calls fn for every set bit index in ascending order.
func (b *Bitmap) All(fn func(uint32)) {
	// Collect word indices for deterministic ascending iteration.
	idxs := make([]uint32, 0, len(b.words))
	for w := range b.words {
		idxs = append(idxs, w)
	}
	for i := 0; i < len(idxs); i++ {
		for j := i + 1; j < len(idxs); j++ {
			if idxs[j] < idxs[i] {
				idxs[i], idxs[j] = idxs[j], idxs[i]
			}
		}
	}
	for _, w := range idxs {
		word := b.words[w]
		for word != 0 {
			tz := trailingZeros64(word)
			fn(w<<6 | uint32(tz))
			word &= word - 1
		}
	}
}

func popcount64(x uint64) int {
	x -= x >> 1 & 0x5555555555555555
	x = (x & 0x3333333333333333) + (x>>2)&0x3333333333333333
	x = (x + x>>4) & 0x0f0f0f0f0f0f0f0f
	return int(x * 0x0101010101010101 >> 56)
}

func trailingZeros64(x uint64) int {
	if x == 0 {
		return 64
	}
	n := 0
	for x&1 == 0 {
		n++
		x >>= 1
	}
	return n
}
