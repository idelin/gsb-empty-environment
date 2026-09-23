package rlecomp

import "math"

// segment computes a minimum-total-byte block partitioning of src using
// dynamic programming over real header costs (headerLen). It is exact:
// literal blocks always end at a run start, at end of input, or at the
// MaxBlockLen cap (splitting an over-long literal into maximal pieces is
// optimal because headerLen is non-decreasing and the piece count is
// minimized); repeat blocks always cover the maximal run up to the cap.
//
// Running time is O(n * r) where r is the number of runs of length >= 2
// inside a MaxBlockLen window; inputs without runs are O(n).
func segment(src []byte) []seg {
	n := len(src)
	if n == 0 {
		return nil
	}

	runLen := make([]int, n)
	runLen[n-1] = 1
	for i := n - 2; i >= 0; i-- {
		if src[i] == src[i+1] {
			runLen[i] = runLen[i+1] + 1
		} else {
			runLen[i] = 1
		}
	}
	// nextRun[i] = smallest index >= i starting a run of length >= 2, or n.
	nextRun := make([]int, n+1)
	nextRun[n] = n
	for i := n - 1; i >= 0; i-- {
		if runLen[i] >= 2 {
			nextRun[i] = i
		} else {
			nextRun[i] = nextRun[i+1]
		}
	}

	const inf = int64(math.MaxInt64)
	dp := make([]int64, n+1)
	type choice struct {
		repeat bool
		length int
	}
	ch := make([]choice, n+1)

	for i := n - 1; i >= 0; i-- {
		best := inf
		if runLen[i] >= 2 {
			r := min(runLen[i], MaxBlockLen)
			if c := dp[i+r] + int64(headerLen(r)) + 1; c < best {
				best = c
				ch[i] = choice{repeat: true, length: r}
			}
		}
		lim := min(i+MaxBlockLen, n)
		// Literal ending at a run start (next block can be a repeat).
		for j := nextRun[i+1]; j < n && j <= lim; j = nextRun[j+1] {
			if c := dp[j] + int64(j-i) + int64(headerLen(j-i)); c < best {
				best = c
				ch[i] = choice{length: j - i}
			}
		}
		// Literal ending at end of input, or forced split at the cap.
		if c := dp[lim] + int64(lim-i) + int64(headerLen(lim-i)); c < best {
			best = c
			ch[i] = choice{length: lim - i}
		}
		dp[i] = best
	}

	var out []seg
	for i := 0; i < n; {
		c := ch[i]
		out = append(out, seg{repeat: c.repeat, start: i, length: c.length})
		i += c.length
	}
	return out
}

// optimalCost returns the exact minimum encoded payload size for src.
func optimalCost(src []byte) int64 {
	total := int64(0)
	for _, s := range segment(src) {
		total += int64(s.segCost())
	}
	return total
}

// trailingRunStart returns the start index of the maximal run of equal
// bytes at the end of b. The caller must not finalize that run because
// future input may extend it.
func trailingRunStart(b []byte) int {
	t := len(b) - 1
	for t > 0 && b[t-1] == b[len(b)-1] {
		t--
	}
	return t
}
