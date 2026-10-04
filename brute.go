package main

// BruteForce solves the same lexicographic optimization by exhaustively
// enumerating every contiguous partition into k groups and every integer
// representative assignment per group. Only feasible for small inputs;
// used to verify the exact DP solver.
func BruteForce(w []int64, k int) (int, int64) {
	n := len(w)
	m := 0
	for _, c := range w {
		if c > 0 {
			m++
		}
	}
	if m == 0 {
		return 0, 0
	}
	if k < 1 {
		k = 1
	}
	if k > m {
		k = m
	}

	// segOpts[a][b] lists (maxErr, sse) for every representative 0..n-1
	// of segment [a,b]; empty list if the segment has no pixels.
	type opt struct {
		err int
		sse int64
	}
	segOpts := make([][][]opt, n)
	for a := 0; a < n; a++ {
		segOpts[a] = make([][]opt, n)
		for b := a; b < n; b++ {
			var cnt int64
			for i := a; i <= b; i++ {
				cnt += w[i]
			}
			if cnt == 0 {
				continue
			}
			for r := 0; r < n; r++ {
				var sse int64
				me := 0
				for i := a; i <= b; i++ {
					if w[i] == 0 {
						continue
					}
					d := iabs(i - r)
					if d > me {
						me = d
					}
					sse += w[i] * int64(d) * int64(d)
				}
				segOpts[a][b] = append(segOpts[a][b], opt{me, sse})
			}
		}
	}

	bestErr := int(1 << 30)
	bestSSE := int64(1<<62 - 1)

	// Recurse over segments of the current partition, combining
	// representative choices lexicographically: (max err, sum sse).
	var combine func(segIdx int, cuts []int, curErr int, curSSE int64)
	combine = func(segIdx int, cuts []int, curErr int, curSSE int64) {
		if curErr > bestErr {
			return
		}
		if segIdx == len(cuts)-1 {
			if curErr < bestErr || (curErr == bestErr && curSSE < bestSSE) {
				bestErr, bestSSE = curErr, curSSE
			}
			return
		}
		for _, o := range segOpts[cuts[segIdx]][cuts[segIdx+1]-1] {
			e := curErr
			if o.err > e {
				e = o.err
			}
			combine(segIdx+1, cuts, e, curSSE+o.sse)
		}
	}

	// Recurse over compositions of [0,n) into k contiguous segments,
	// each containing at least one non-empty level.
	cuts := make([]int, 0, k+1)
	cuts = append(cuts, 0)
	var partition func(start, left int)
	partition = func(start, left int) {
		if left == 1 {
			cuts = append(cuts, n)
			combine(0, cuts, 0, 0)
			cuts = cuts[:len(cuts)-1]
			return
		}
		for end := start + 1; end <= n-left+1; end++ {
			if len(segOpts[start][end-1]) == 0 {
				continue
			}
			cuts = append(cuts, end)
			partition(end, left-1)
			cuts = cuts[:len(cuts)-1]
		}
	}
	partition(0, k)

	return bestErr, bestSSE
}
