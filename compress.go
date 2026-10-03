package dagreach

import "math/bits"

// CompressedIndex is a memory-optimized, immutable snapshot of an
// Indexer. Each bitmap row is stored as:
//
//	presence bitmap: one bit per word, set iff that word is non-zero
//	packed words:    only the non-zero uint64 words, back to back
//
// A query locates its word by a popcount over the presence bitmap, so it
// stays O(1) for any fixed row width, while memory is strictly bounded
// by the number of words that actually carry information.
type CompressedIndex struct {
	n            int
	words        int
	presence     [][]uint64 // presence[u]: bit w set iff row u word w is non-zero
	packed       [][]uint64 // packed[u]: the non-zero words of row u
	nonZeroWords int
}

// Compress snapshots the dense index into its compressed form.
func (idx *Indexer) Compress() *CompressedIndex {
	presenceWords := (idx.words + wordBits - 1) / wordBits
	c := &CompressedIndex{
		n:        idx.n,
		words:    idx.words,
		presence: make([][]uint64, idx.n),
		packed:   make([][]uint64, idx.n),
	}
	for u := 0; u < idx.n; u++ {
		row := idx.reach[u]
		pres := make([]uint64, presenceWords)
		var pack []uint64
		for w, word := range row {
			if word != 0 {
				pres[w/wordBits] |= 1 << (uint(w) % wordBits)
				pack = append(pack, word)
				c.nonZeroWords++
			}
		}
		c.presence[u] = pres
		c.packed[u] = pack
	}
	return c
}

// Size returns the number of nodes.
func (c *CompressedIndex) Size() int { return c.n }

// Reachable reports whether v is reachable from u, answering directly
// from the compressed representation: one presence-bit check plus a
// popcount to locate the packed word.
func (c *CompressedIndex) Reachable(u, v int) bool {
	if u < 0 || u >= c.n || v < 0 || v >= c.n {
		return false
	}
	w := v / wordBits
	pres := c.presence[u]
	pw := pres[w/wordBits]
	bit := uint(w) % wordBits
	if pw&(1<<bit) == 0 {
		return false
	}
	// Offset of word w inside packed[u] = number of present words below w.
	offset := 0
	for i := 0; i < w/wordBits; i++ {
		offset += bits.OnesCount64(pres[i])
	}
	offset += bits.OnesCount64(pw & ((1 << bit) - 1))
	return c.packed[u][offset]&(1<<(uint(v)%wordBits)) != 0
}

// MemoryBytes estimates the compressed footprint: the presence bitmaps
// plus one uint64 per non-zero word.
func (c *CompressedIndex) MemoryBytes() int {
	presenceWords := (c.words + wordBits - 1) / wordBits
	return c.n*presenceWords*8 + c.nonZeroWords*8
}

// NonZeroWords reports how many bitmap words survived compression.
func (c *CompressedIndex) NonZeroWords() int { return c.nonZeroWords }

// CompressionRatio returns denseBytes / compressedBytes; values above 1
// mean the compressed form is smaller.
func (c *CompressedIndex) CompressionRatio(denseBytes int) float64 {
	if denseBytes <= 0 {
		return 1
	}
	return float64(denseBytes) / float64(c.MemoryBytes())
}
