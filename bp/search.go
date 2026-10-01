package bp

// This file holds the two core excess-search primitives plus the range-min
// query. Skipping happens at two levels: superblocks via the sparse-table
// RMQ over superblock minima (O(log) probes), blocks via the per-superblock
// sparse table over block minima (O(log) probes). Only a single block is
// ever scanned bit by bit, so queries never scan the parenthesis string.

// searchForward returns the first position j >= from with Excess(j) == t,
// or -1 if there is none.
func (ix *Index) searchForward(from, t int, acc *int) int {
	j := from
	cur := ix.excessAcc(j, acc)
	for {
		if cur == t {
			return j
		}
		if j >= ix.n {
			return -1
		}
		if j%SuperblockSize == 0 {
			si := ix.nextSbLEQ(j/SuperblockSize, t, acc)
			if si < 0 {
				// No position in [j, n) reaches t; only position n
				// itself (excess 0) can qualify.
				if t == 0 {
					return ix.n
				}
				return -1
			}
			if si > j/SuperblockSize {
				j = si * SuperblockSize
				cur = ix.excessAcc(j, acc)
				continue
			}
			// si == j/SuperblockSize: descend to block level below.
		}
		bi := j / BlockSize
		bs := bi * BlockSize
		end := bs + BlockSize
		if j == bs && end <= ix.n {
			si := bs / SuperblockSize
			rbi := bi - si*blocksPerSB
			cnt := blocksPerSB
			if rem := len(ix.blks) - si*blocksPerSB; rem < cnt {
				cnt = rem
			}
			tb := ix.nextBlockLEQ(si, rbi, cnt, t-int(ix.sbs[si].absExcess), acc)
			if tb < 0 {
				// No qualifying block left in this superblock.
				j = (si + 1) * SuperblockSize
				if j > ix.n {
					j = ix.n
				}
				cur = ix.excessAcc(j, acc)
				continue
			}
			if tb > rbi {
				j = (si*blocksPerSB + tb) * BlockSize
				cur = ix.excessAcc(j, acc)
				continue
			}
			// tb == rbi: scan this block bit by bit below.
		}
		if end > ix.n {
			end = ix.n
		}
		*acc++
		for j < end {
			if ix.bits.get(j) {
				cur++
			} else {
				cur--
			}
			j++
			if cur == t {
				return j
			}
		}
	}
}

// searchBackward returns the last position j <= from with Excess(j) == t,
// or -1 if there is none.
func (ix *Index) searchBackward(from, t int, acc *int) int {
	j := from
	cur := ix.excessAcc(j, acc)
	for {
		if cur == t {
			return j
		}
		if j%SuperblockSize == 0 && j > 0 {
			si := ix.prevSbLEQ(j/SuperblockSize-1, t, acc)
			if si < 0 {
				return -1
			}
			if si < j/SuperblockSize-1 {
				j = (si + 1) * SuperblockSize
				cur = ix.excessAcc(j, acc)
				continue
			}
			// si == j/SuperblockSize-1: descend to block level below.
		}
		bi := j / BlockSize
		bs := bi * BlockSize
		if j == bs {
			if bi == 0 {
				return -1
			}
			si := (bs - 1) / SuperblockSize // superblock holding block bi-1
			rbi := (bi - 1) - si*blocksPerSB
			tb := ix.prevBlockLEQ(si, rbi, t-int(ix.sbs[si].absExcess), acc)
			if tb < 0 {
				// No qualifying block left in this superblock.
				j = si * SuperblockSize
				cur = ix.excessAcc(j, acc)
				continue
			}
			if tb < rbi {
				j = (si*blocksPerSB + tb + 1) * BlockSize
				cur = ix.excessAcc(j, acc)
				continue
			}
			// tb == rbi: step into block bi-1 and scan it bit by bit.
			if ix.bits.get(bs - 1) {
				cur--
			} else {
				cur++
			}
			j = bs - 1
			continue
		}
		*acc++
		for j > bs {
			if ix.bits.get(j - 1) {
				cur--
			} else {
				cur++
			}
			j--
			if cur == t {
				return j
			}
		}
	}
}

// minExcessRange returns min Excess(j) for lo <= j < hi.
func (ix *Index) minExcessRange(lo, hi int, acc *int) int {
	m := int(^uint(0) >> 1)
	j := lo
	cur := ix.excessAcc(j, acc)
	for j < hi {
		bi := j / BlockSize
		bs := bi * BlockSize
		end := bs + BlockSize
		if j == bs {
			if j%SuperblockSize == 0 && j+SuperblockSize <= hi {
				// Whole superblocks [si1, si2) fit inside [j, hi).
				si1 := j / SuperblockSize
				si2 := hi / SuperblockSize
				*acc++
				if v := int(ix.sbRMQ.rangeMin(si1, si2-1)); v < m {
					m = v
				}
				j = si2 * SuperblockSize
				if j < hi {
					cur = ix.excessAcc(j, acc)
				}
				continue
			}
			if end <= hi {
				// Run of full blocks [bi, b2] inside this superblock.
				si := bs / SuperblockSize
				maxBi := (si+1)*blocksPerSB - 1
				if rem := len(ix.blks) - 1; rem < maxBi {
					maxBi = rem
				}
				b2 := hi/BlockSize - 1
				if maxBi < b2 {
					b2 = maxBi
				}
				if b2 >= bi {
					*acc++
					v := int(ix.rangeMinBlocksRel(si, bi-si*blocksPerSB, b2-si*blocksPerSB)) +
						int(ix.sbs[si].absExcess)
					if v < m {
						m = v
					}
					j = (b2 + 1) * BlockSize
					if j < hi {
						cur = ix.excessAcc(j, acc)
					}
					continue
				}
			}
		}
		if end > hi {
			end = hi
		}
		*acc++
		for j < end {
			if cur < m {
				m = cur
			}
			if ix.bits.get(j) {
				cur++
			} else {
				cur--
			}
			j++
		}
	}
	return m
}
