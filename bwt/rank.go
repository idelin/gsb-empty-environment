package bwt

// RankStride is the number of symbols covered by one rank checkpoint.
// The index stores one 256-entry uint32 checkpoint per stride, i.e.
// 2 bytes of index per symbol of data, instead of a full 4-byte rank
// per position. A rank query costs at most RankStride byte comparisons.
const RankStride = 512

// RankIndex answers rank queries over a byte slice: how many times a
// byte occurs in a prefix. It is the compressed rank directory used to
// evaluate the LF mapping without materializing per-position ranks.
type RankIndex struct {
	data []byte
	// cp[k*256+b] = occurrences of byte b in data[:k*RankStride].
	cp []uint32
}

// NewRankIndex builds a checkpointed rank index over data. The data
// slice is referenced, not copied.
func NewRankIndex(data []byte) *RankIndex {
	n := len(data)
	ncp := n/RankStride + 1
	cp := make([]uint32, ncp*256)
	var cur [256]uint32
	for k := 0; k < ncp; k++ {
		copy(cp[k*256:(k+1)*256], cur[:])
		end := (k + 1) * RankStride
		if end > n {
			end = n
		}
		for _, b := range data[k*RankStride : end] {
			cur[b]++
		}
	}
	return &RankIndex{data: data, cp: cp}
}

// Rank returns the number of occurrences of b in data[:i].
// i must satisfy 0 <= i <= len(data).
func (r *RankIndex) Rank(b byte, i int) int {
	k := i / RankStride
	cnt := int(r.cp[k*256+int(b)])
	for _, x := range r.data[k*RankStride : i] {
		if x == b {
			cnt++
		}
	}
	return cnt
}
