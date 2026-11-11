package netsec

import "math"

// inc returns x+1, saturating at math.MaxInt64.
func inc(x int64) int64 {
	if x == math.MaxInt64 {
		return math.MaxInt64
	}
	return x + 1
}

// dec returns x-1, saturating at math.MinInt64.
func dec(x int64) int64 {
	if x == math.MinInt64 {
		return math.MinInt64
	}
	return x - 1
}

// mulCap returns a*b while it stays below cap; once the product would reach or
// exceed cap it returns cap. It never overflows.
func mulCap(a, b, cap int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a >= cap || b >= cap || a > cap/b {
		return cap
	}
	p := a * b
	if p >= cap {
		return cap
	}
	return p
}

// windowWidth returns the number of integer ticks in the inclusive window
// hi-lo+1 without overflowing; it saturates at math.MaxInt64. Unsigned
// modular subtraction gives the true span for any lo <= hi because the span
// fits in uint64 except for the full-range case, which is handled first.
func windowWidth(lo, hi int64) int64 {
	if lo == math.MinInt64 && hi == math.MaxInt64 {
		return math.MaxInt64
	}
	width := uint64(hi) - uint64(lo) + 1
	if width > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(width)
}
