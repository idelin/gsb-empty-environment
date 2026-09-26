package bitvec

import "math/bits"

// Rank1 returns the number of one bits in [0, i). The bound is unified:
// i is clamped to bitLen, so Rank1(BitLen()) always equals TotalOnes().
// Cost: one superblock entry, at most BlocksPerSuper-1 directory
// entries, and at most one decoded block.
func (m *Mapped) Rank1(i uint64, mx *Metrics) uint64 {
	if i > m.bitLen {
		i = m.bitLen
	}
	b := uint32(i / BlockBits)
	off := uint(i % BlockBits)
	cum, ok := m.cumBeforeBlock(b, mx)
	if !ok {
		return 0
	}
	if off == 0 {
		return cum
	}
	w, ok := m.blockWords(b, mx)
	if !ok {
		return cum
	}
	full := off / 64
	rem := off % 64
	var c uint64
	for wi := uint(0); wi < full; wi++ {
		c += uint64(bits.OnesCount64(w[wi]))
	}
	if rem > 0 {
		c += uint64(bits.OnesCount64(w[full] & ((uint64(1) << rem) - 1)))
	}
	return cum + c
}

// Select1 returns the position of the k-th one bit (0-based). ok is
// false when k >= TotalOnes() — in particular an all-zero bitmap has no
// select result. The select sample index provides a starting block hint
// that is validated against the rank index before use; corrupted hints
// degrade to a safe scan, never to an out-of-bounds access.
func (m *Mapped) Select1(k uint64, mx *Metrics) (pos uint64, ok bool) {
	if k >= m.totalOnes || m.numBlocks == 0 {
		return 0, false
	}
	lo := uint32(0)
	cum := uint64(0)
	if m.numSamples > 0 {
		si := k / uint64(m.selRate)
		if si >= uint64(m.numSamples) {
			si = uint64(m.numSamples) - 1
		}
		if blk, ok := m.u32(m.selOff+si*sampleEntrySize, mx); ok && blk < m.numBlocks {
			if c, ok2 := m.cumBeforeBlock(blk, mx); ok2 && c <= k {
				lo, cum = blk, c
			}
		}
	}
	for b := lo; b < m.numBlocks; b++ {
		_, pop, _, ok := m.dirEntry(b, mx)
		if !ok {
			return 0, false
		}
		if cum+uint64(pop) > k {
			w, ok := m.blockWords(b, mx)
			if !ok {
				return 0, false
			}
			t := k - cum
			for wi := range w {
				pc := uint64(bits.OnesCount64(w[wi]))
				if t < pc {
					x := w[wi]
					for ; t > 0; t-- {
						x &= x - 1
					}
					return uint64(b)*BlockBits + uint64(wi)*64 + uint64(bits.TrailingZeros64(x)), true
				}
				t -= pc
			}
			return 0, false // inconsistent index: no result, no crash
		}
		cum += uint64(pop)
	}
	return 0, false
}
