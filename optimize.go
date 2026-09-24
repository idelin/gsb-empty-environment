package dod

// indexEntryBytes models the per-block index overhead so the boundary DP
// jointly trades anchor/header cost against payload width cost.
const indexEntryBytes = 28

// optimizeCuts chooses block boundaries (anchor positions) minimizing the
// exact total encoded size: block bytes (with per-segment width optimization
// inside each candidate block) plus per-block index overhead. Positions where
// a delta would overflow int64 force a cut, so extreme values always get a
// fresh anchor instead of wrapping.
//
// Returns cut positions in (0, n]; each block is [prev, cut).
func optimizeCuts(epoch uint64, times, values []int64, maxBlock int) []int {
	n := len(times)
	if n == 0 {
		return nil
	}
	if maxBlock < 1 {
		maxBlock = 1
	}
	dp := make([]int64, n+1)
	cut := make([]int, n+1)
	for j := 1; j <= n; j++ {
		dp[j] = -1
	}
	for i := 0; i < n; i++ {
		if dp[i] < 0 {
			continue
		}
		st, sv := newStreamDP(), newStreamDP()
		var dt0, dv0, dTp, dVp int64
		lim := i + maxBlock
		if lim > n {
			lim = n
		}
		for j := i + 1; j <= lim; j++ {
			m := j - i
			if m >= 2 {
				dt, ok1 := checkedSub(times[j-1], times[j-2])
				dv, ok2 := checkedSub(values[j-1], values[j-2])
				if !ok1 || !ok2 {
					break // overflow: no block starting at i may extend past j-1
				}
				if m == 2 {
					dt0, dv0 = dt, dv
				} else {
					ddt, ok3 := checkedSub(dt, dTp)
					ddv, ok4 := checkedSub(dv, dVp)
					if !ok3 || !ok4 {
						break
					}
					st.push(zigzag(ddt))
					sv.push(zigzag(ddv))
				}
				dTp, dVp = dt, dv
			}
			cand := dp[i] + int64(blockSize(epoch, m, times[i], values[i], dt0, dv0, st, sv)) + indexEntryBytes
			if dp[j] < 0 || cand < dp[j] {
				dp[j] = cand
				cut[j] = i
			}
		}
	}
	var rev []int
	for j := n; j > 0; j = cut[j] {
		rev = append(rev, j)
	}
	cuts := make([]int, len(rev))
	for i, c := range rev {
		cuts[len(rev)-1-i] = c
	}
	return cuts
}
