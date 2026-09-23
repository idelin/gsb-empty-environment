package blockpack

import "crypto/sha256"

var padHash = sha256.Sum256([]byte("blockpack pad"))

func nodeHash(l, r Digest) Digest {
	return hashParts([]byte("node\x00"), l[:], r[:])
}

// merkleLevels builds all levels of a Merkle tree over leaves, padding the
// leaf layer to a power of two with a fixed pad hash. levels[0] are the
// (padded) leaves, the last level holds the single root.
func merkleLevels(leaves []Digest) [][][32]byte {
	n := 1
	for n < len(leaves) {
		n <<= 1
	}
	lvl := make([]Digest, n)
	copy(lvl, leaves)
	for i := len(leaves); i < n; i++ {
		lvl[i] = padHash
	}
	levels := [][][32]byte{lvl}
	for len(lvl) > 1 {
		next := make([]Digest, len(lvl)/2)
		for i := range next {
			next[i] = nodeHash(lvl[2*i], lvl[2*i+1])
		}
		levels = append(levels, next)
		lvl = next
	}
	return levels
}

func merkleRoot(leaves []Digest) Digest {
	if len(leaves) == 0 {
		return padHash
	}
	levels := merkleLevels(leaves)
	return levels[len(levels)-1][0]
}

// proveRangeHashes returns the sibling hashes needed to verify the
// contiguous leaf range [start, end) against the Merkle root, in the order
// a left-to-right tree traversal encounters them.
func proveRangeHashes(leaves []Digest, start, end int) ([]Digest, error) {
	if start < 0 || end > len(leaves) || start >= end {
		return nil, errRange
	}
	levels := merkleLevels(leaves)
	var out []Digest
	proveRec(levels, len(levels)-1, 0, len(levels[0]), start, end, &out)
	return out, nil
}

func proveRec(levels [][][32]byte, k, lo, hi, start, end int, out *[]Digest) {
	if end <= lo || hi <= start {
		*out = append(*out, levels[k][lo>>uint(k)])
		return
	}
	if start <= lo && hi <= end {
		return
	}
	mid := (lo + hi) / 2
	proveRec(levels, k-1, lo, mid, start, end, out)
	proveRec(levels, k-1, mid, hi, start, end, out)
}

// verifyRangeHashes recomputes the Merkle root from the leaves in
// [start, end) plus the sibling hashes, and reports whether it equals root.
// It consumes exactly len(rangeLeaves) leaves and len(siblings) siblings;
// anything left over or missing fails.
func verifyRangeHashes(root Digest, totalLeaves, start, end int, rangeLeaves, siblings []Digest) bool {
	if totalLeaves <= 0 || start < 0 || end > totalLeaves || start >= end {
		return false
	}
	if len(rangeLeaves) != end-start {
		return false
	}
	n := 1
	depth := 0
	for n < totalLeaves {
		n <<= 1
		depth++
	}
	li, si := 0, 0
	got, ok := verifyRec(depth, 0, n, start, end, rangeLeaves, &li, siblings, &si)
	return ok && li == len(rangeLeaves) && si == len(siblings) && got == root
}

func verifyRec(k, lo, hi, start, end int, leaves []Digest, li *int, sibs []Digest, si *int) (Digest, bool) {
	if end <= lo || hi <= start {
		if *si >= len(sibs) {
			return Digest{}, false
		}
		s := sibs[*si]
		*si++
		return s, true
	}
	if start <= lo && hi <= end {
		count := hi - lo
		if *li+count > len(leaves) {
			return Digest{}, false
		}
		lvl := make([]Digest, count)
		copy(lvl, leaves[*li:*li+count])
		*li += count
		for len(lvl) > 1 {
			next := make([]Digest, len(lvl)/2)
			for i := range next {
				next[i] = nodeHash(lvl[2*i], lvl[2*i+1])
			}
			lvl = next
		}
		return lvl[0], true
	}
	mid := (lo + hi) / 2
	l, ok := verifyRec(k-1, lo, mid, start, end, leaves, li, sibs, si)
	if !ok {
		return Digest{}, false
	}
	r, ok := verifyRec(k-1, mid, hi, start, end, leaves, li, sibs, si)
	if !ok {
		return Digest{}, false
	}
	return nodeHash(l, r), true
}
