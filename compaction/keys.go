package compaction

// prevKey returns the lexicographically greatest key strictly smaller than k.
// It is used to turn an exclusive end boundary into an inclusive largest key.
func prevKey(k UserKey) UserKey {
	b := []byte(k)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] > 0 {
			b[i]--
			return UserKey(b[:i+1])
		}
	}
	return ""
}

// maxUserKey returns the larger of two keys, treating empty as -infinity.
func maxUserKey(a, b UserKey) UserKey {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if a > b {
		return a
	}
	return b
}

// clipTombstone intersects tombstone t with the half-open span [lo, hi). An
// empty hi means +infinity. The returned RangeTombstone is non-empty when
// true is reported. This is the exact per-span clipping that prevents old
// keys from resurrecting in subcompactions beyond the tombstone's start span.
func clipTombstone(t RangeTombstone, lo, hi UserKey) (RangeTombstone, bool) {
	start := maxUserKey(t.Start, lo)
	var end UserKey
	if hi == "" || t.End == "" {
		end = t.End
		if hi != "" && end == "" {
			end = hi
		}
	} else if t.End < hi {
		end = t.End
	} else {
		end = hi
	}
	if end != "" && start >= end {
		return RangeTombstone{}, false
	}
	return RangeTombstone{Start: start, End: end, Seq: t.Seq}, true
}
