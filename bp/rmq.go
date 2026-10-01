package bp

// rmq is a sparse table for O(1) range-minimum queries over the superblock
// minimum-excess values. It is a derived structure: it is rebuilt from the
// (verified) superblock summaries and is never serialized. Memory is
// O((n/SuperblockSize) log(n/SuperblockSize)) int32s, i.e. sublinear in n.
type rmq struct {
	log  []int32   // log[i] = floor(log2(i)), i >= 1
	mins [][]int32 // mins[k][i] = min over [i, i+2^k)
	n    int
}

func newRMQ(vals []int32) *rmq {
	n := len(vals)
	lg := make([]int32, n+2)
	for i := 2; i < len(lg); i++ {
		lg[i] = lg[i/2] + 1
	}
	levels := int(lg[n]) + 1
	mins := make([][]int32, levels)
	mins[0] = append([]int32(nil), vals...)
	for k := 1; k < levels; k++ {
		prev := mins[k-1]
		m := n - (1 << k) + 1
		cur := make([]int32, m)
		half := 1 << (k - 1)
		for i := 0; i < m; i++ {
			a, b := prev[i], prev[i+half]
			if b < a {
				a = b
			}
			cur[i] = a
		}
		mins[k] = cur
	}
	return &rmq{log: lg, mins: mins, n: n}
}

// rangeMin returns the minimum over indices [lo, hi] inclusive.
func (r *rmq) rangeMin(lo, hi int) int32 {
	k := r.log[hi-lo+1]
	a := r.mins[k][lo]
	b := r.mins[k][hi-(1<<k)+1]
	if b < a {
		return b
	}
	return a
}

func (r *rmq) bytes() int {
	b := len(r.log) * 4
	for _, lvl := range r.mins {
		b += len(lvl) * 4
	}
	return b
}

// prevSbLEQ returns the rightmost superblock index si <= S whose minimum
// excess is <= t, or -1 if there is none. O(log numSB) table probes.
func (ix *Index) prevSbLEQ(S, t int, acc *int) int {
	si := S
	for k := int(ix.sbRMQ.log[S+1]); k >= 0; k-- {
		j := si - (1 << k) + 1
		if j >= 0 {
			*acc++
			if int(ix.sbRMQ.rangeMin(j, si)) > t {
				si = j - 1
			}
		}
	}
	if si >= 0 {
		*acc++
		if ix.sbAbsMin(si) <= t {
			return si
		}
	}
	return -1
}

// nextSbLEQ returns the leftmost superblock index si >= S whose minimum
// excess is <= t, or -1 if there is none. O(log numSB) table probes.
func (ix *Index) nextSbLEQ(S, t int, acc *int) int {
	si := S
	numSB := ix.sbRMQ.n
	for k := int(ix.sbRMQ.log[numSB-S]); k >= 0; k-- {
		j := si + (1 << k) - 1
		if j < numSB {
			*acc++
			if int(ix.sbRMQ.rangeMin(si, j)) > t {
				si = j + 1
			}
		}
	}
	if si < numSB {
		*acc++
		if ix.sbAbsMin(si) <= t {
			return si
		}
	}
	return -1
}
