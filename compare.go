package fpcodec

// BitsEqual compares two bit patterns of the same width exactly.
//
// Float comparison must go through bit patterns, never through host float
// `==`: NaN != NaN under IEEE equality, and +0 == -0, both of which would
// silently corrupt round-trip checks. BitsEqual treats patterns as opaque
// integers, so NaN payloads and the sign of zero compare correctly.
func BitsEqual(w Width, a, b uint64) bool {
	return maskBits(w, a) == maskBits(w, b)
}

// AllBitsEqual compares two slices of bit patterns element-wise.
func AllBitsEqual(w Width, a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !BitsEqual(w, a[i], b[i]) {
			return false
		}
	}
	return true
}
