package bp

// blocksPerSB is the number of summary blocks inside one superblock.
const blocksPerSB = SuperblockSize / BlockSize

// blockLevels is the number of sparse-table levels over one superblock's
// blocks; 2^(blockLevels-1) >= blocksPerSB.
const blockLevels = 5

// blockLog[i] = floor(log2(i)) for 0 <= i <= blocksPerSB.
var blockLog = [blocksPerSB + 1]int{0, 0, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 3, 4}

// Level 0 of the per-superblock sparse table (the block minima themselves)
// is derived from the block summaries on the fly; levels 1..blockLevels-1
// are stored, each packed to its real width blocksPerSB-2^k+1.
// Memory: numSuperblocks * blockTableWidth * 2 bytes, sublinear in n.

// blockLevelOff[k] is the offset of sparse level k inside one superblock's
// packed table, for k = 1..blockLevels-1.
var blockLevelOff = [blockLevels]int{0, 0, 15, 28, 37}

// blockTableWidth is the packed per-superblock table size in int16 entries:
// sum over k=1..blockLevels-1 of (blocksPerSB - 2^k + 1) = 15+13+9+1.
const blockTableWidth = 38

// buildBlockRMQ derives the tables from the (verified) block summaries; they
// are never serialized.
func (ix *Index) buildBlockRMQ() {
	ix.blkRMQ = make([]int16, len(ix.sbs)*blockTableWidth)
	for si := range ix.sbs {
		for k := 1; k < blockLevels; k++ {
			half := 1 << (k - 1)
			dst := si*blockTableWidth + blockLevelOff[k]
			for i := 0; i+(1<<k) <= blocksPerSB; i++ {
				a := ix.blockMinRel(si, k-1, i)
				b := ix.blockMinRel(si, k-1, i+half)
				if b < a {
					a = b
				}
				ix.blkRMQ[dst+i] = a
			}
		}
	}
}

// blockMinRel returns the min excess of the block covering relative index i
// at sparse level k (window 2^k starting at i) within superblock si,
// relative to the superblock start. Missing blocks (short last superblock)
// read as +infinity.
func (ix *Index) blockMinRel(si, k, i int) int16 {
	if k == 0 {
		gi := si*blocksPerSB + i
		if gi >= len(ix.blks) {
			return 32767
		}
		blk := &ix.blks[gi]
		return blk.relExcess + blk.relMin
	}
	return ix.blkRMQ[si*blockTableWidth+blockLevelOff[k]+i]
}

// rangeMinBlocksRel returns the minimum block min-excess (relative to the
// superblock start) over relative block indices [lo, hi] of superblock si.
func (ix *Index) rangeMinBlocksRel(si, lo, hi int) int16 {
	k := blockLog[hi-lo+1]
	a := ix.blockMinRel(si, k, lo)
	b := ix.blockMinRel(si, k, hi-(1<<k)+1)
	if b < a {
		return b
	}
	return a
}

// prevBlockLEQ returns the rightmost relative block index b <= rbi inside
// superblock si whose minimum excess (relative to the superblock start) is
// <= tRel, or -1. O(blockLevels) probes.
func (ix *Index) prevBlockLEQ(si, rbi, tRel int, acc *int) int {
	b := rbi
	for k := blockLevels - 1; k >= 0; k-- {
		j := b - (1 << k) + 1
		if j >= 0 {
			*acc++
			if int(ix.rangeMinBlocksRel(si, j, b)) > tRel {
				b = j - 1
			}
		}
	}
	if b >= 0 {
		*acc++
		if int(ix.blockMinRel(si, 0, b)) <= tRel {
			return b
		}
	}
	return -1
}

// nextBlockLEQ returns the leftmost relative block index b >= rbi inside
// superblock si (which holds cnt blocks) whose minimum excess relative to
// the superblock start is <= tRel, or -1. O(blockLevels) probes.
func (ix *Index) nextBlockLEQ(si, rbi, cnt, tRel int, acc *int) int {
	b := rbi
	for k := blockLevels - 1; k >= 0; k-- {
		j := b + (1 << k) - 1
		if j < cnt {
			*acc++
			if int(ix.rangeMinBlocksRel(si, b, j)) > tRel {
				b = j + 1
			}
		}
	}
	if b < cnt {
		*acc++
		if int(ix.blockMinRel(si, 0, b)) <= tRel {
			return b
		}
	}
	return -1
}
