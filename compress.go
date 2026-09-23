package slz

import (
	"encoding/binary"
	"hash/crc32"
)

// matchIndex is a bounded hash-chain index over 3-byte prefixes.
// The chain table has WindowSize slots, so memory and search depth are
// bounded and entries older than the window are unreachable.
type matchIndex struct {
	head [hashSize]int32
	prev [WindowSize]int32
}

func hash3(b []byte) uint32 {
	h := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	h *= 2654435761
	return h >> (32 - hashBits)
}

func newMatchIndex() *matchIndex {
	m := &matchIndex{}
	for i := range m.head {
		m.head[i] = -1
	}
	return m
}

// findMatches fills bestLen/bestDist with, for every position, the longest
// match within the window (ties broken by smallest distance, since the
// chain is walked most-recent-first and only strictly longer matches
// replace the current best).
func findMatches(data []byte, bestLen, bestDist []int) {
	n := len(data)
	idx := newMatchIndex()
	for i := 0; i+MinMatch <= n; i++ {
		h := hash3(data[i:])
		cand := idx.head[h]
		idx.prev[i&(WindowSize-1)] = idx.head[h]
		idx.head[h] = int32(i)

		limit := i - WindowSize
		maxL := MaxMatch
		if rem := n - i; rem < maxL {
			maxL = rem
		}
		best, bdist := 0, 0
		for depth := 0; cand >= 0 && depth < maxChain; depth++ {
			j := int(cand)
			if j <= limit {
				break // evicted from the window; chain slot still valid
			}
			// Cheap reject: a longer match must extend past the current best.
			if best < maxL && data[j+best] == data[i+best] {
				l := 0
				for l < maxL && data[j+l] == data[i+l] {
					l++
				}
				if l > best {
					best, bdist = l, i-j
					if best >= maxL {
						break
					}
				}
			}
			cand = idx.prev[j&(WindowSize-1)]
		}
		bestLen[i] = best
		bestDist[i] = bdist
	}
}

// optimalParse runs the minimum-bit parse over the whole input.
//
// Every match length in [MinMatch, bestLen[i]] costs the same fixed
// MatchCost bits, so the DP only needs the longest match per position:
//
//	cost[i] = min( LiteralCost + cost[i+1],
//	               MatchCost + min_{l in [MinMatch,bestLen[i]]} cost[i+l] )
//
// Fixed tie-breaking rules, applied in order:
//  1. a match wins over a literal on equal cost;
//  2. among equal-cost match lengths, the longest wins;
//  3. among equal lengths, the smallest distance wins (from findMatches).
func optimalParse(data []byte, bestLen, bestDist []int) (cost []int, choiceLen, choiceDist []int) {
	n := len(data)
	cost = make([]int, n+1)
	choiceLen = make([]int, n)
	choiceDist = make([]int, n)
	for i := n - 1; i >= 0; i-- {
		best := LiteralCost + cost[i+1]
		chosen := 0 // 0 = literal
		for l := bestLen[i]; l >= MinMatch; l-- {
			if c := MatchCost + cost[i+l]; c <= best {
				best, chosen = c, l
			}
		}
		cost[i] = best
		choiceLen[i] = chosen
		if chosen > 0 {
			choiceDist[i] = bestDist[i]
		}
	}
	return cost, choiceLen, choiceDist
}

// greedyBits is the longest-match-first parser, kept for comparison and
// for demonstrating that greedy is not optimal.
func greedyBits(data []byte) int {
	n := len(data)
	bestLen := make([]int, n)
	bestDist := make([]int, n)
	findMatches(data, bestLen, bestDist)
	bits := 0
	for i := 0; i < n; {
		if bestLen[i] >= MinMatch {
			bits += MatchCost
			i += bestLen[i]
		} else {
			bits += LiteralCost
			i++
		}
	}
	return bits
}

// OptimalBits returns the token-stream bit length chosen by the
// minimum-bit parser for data.
func OptimalBits(data []byte) int {
	n := len(data)
	bestLen := make([]int, n)
	bestDist := make([]int, n)
	findMatches(data, bestLen, bestDist)
	cost, _, _ := optimalParse(data, bestLen, bestDist)
	return cost[0]
}

// GreedyBits returns the token-stream bit length of the greedy
// longest-match parser for data.
func GreedyBits(data []byte) int { return greedyBits(data) }

// parseData returns per-position choices. When reset is false the parse
// may reference anything inside the sliding window; when reset is true
// the data is parsed as a self-contained chunk.
func parseData(data []byte) (choiceLen, choiceDist []int) {
	n := len(data)
	bestLen := make([]int, n)
	bestDist := make([]int, n)
	findMatches(data, bestLen, bestDist)
	_, choiceLen, choiceDist = optimalParse(data, bestLen, bestDist)
	return choiceLen, choiceDist
}

// Compress encodes data as a single block whose checkpoint resets the
// dictionary window.
func Compress(data []byte) []byte {
	return CompressBlocks(data, []int{len(data)}, true)
}

// CompressBlocks encodes data as a sequence of blocks with the given
// uncompressed sizes (which must sum to len(data)). Each block boundary
// is a checkpoint. If resetEach is true every block resets the dictionary
// window (blocks are fully independent); otherwise the window carries
// over and matches may reference earlier blocks within WindowSize.
func CompressBlocks(data []byte, sizes []int, resetEach bool) []byte {
	total := 0
	for _, s := range sizes {
		if s < 0 {
			panic("slz: negative block size")
		}
		total += s
	}
	if total != len(data) {
		panic("slz: block sizes do not sum to len(data)")
	}

	out := make([]byte, 0, len(data)/2+headerSize)
	out = append(out, Magic...)
	out = append(out, Version, 0)
	var u64 [8]byte
	binary.LittleEndian.PutUint64(u64[:], uint64(len(data)))
	out = append(out, u64[:]...)
	var u32 [4]byte
	binary.LittleEndian.PutUint32(u32[:], crc32.ChecksumIEEE(data))
	out = append(out, u32[:]...)

	pos := 0
	var fullLen, fullDist []int
	if !resetEach {
		// Parse the whole stream once so matches may reach back into
		// earlier blocks within the window.
		fullLen, fullDist = parseData(data)
	}
	for bi, size := range sizes {
		reset := resetEach || bi == 0
		var choiceLen, choiceDist []int
		base := 0
		if reset {
			choiceLen, choiceDist = parseData(data[pos : pos+size])
			base = pos
		} else {
			choiceLen, choiceDist = fullLen, fullDist
		}

		w := &bitWriter{}
		end := pos + size
		for i := pos; i < end; {
			l := choiceLen[i-base]
			if l > 0 && i+l > end {
				// Tokens must not cross a block boundary.
				l = end - i
				if l < MinMatch {
					l = 0
				}
			}
			if l == 0 {
				w.writeBits(0, 1)
				w.writeBits(uint32(data[i]), 8)
				i++
			} else {
				w.writeBits(1, 1)
				w.writeBits(uint32(choiceDist[i-base]-1), WindowBits)
				w.writeBits(uint32(l-MinMatch), LenBits)
				i += l
			}
		}
		bitLen := w.bits()
		payload := w.finish()

		binary.LittleEndian.PutUint32(u32[:], uint32(size))
		out = append(out, u32[:]...)
		binary.LittleEndian.PutUint32(u32[:], uint32(bitLen))
		out = append(out, u32[:]...)
		flags := byte(0)
		if reset {
			flags |= flagResetWindow
		}
		out = append(out, flags)
		out = append(out, payload...)
		pos = end
	}
	return out
}
