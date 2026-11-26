package nsec3

import "bytes"

// CoversHash implements the NSEC3 ring interval test of RFC 5155 section
// 3.3. A record with hashed owner name H and next hashed owner name N
// covers a name hash X when X falls in the open interval (H, N) along the
// hash ring:
//
//   - H == N: the single-record chain covers the entire ring, i.e. every
//     X != H (and cannot cover its own owner);
//   - H < N:  covers iff H < X < N (ordinary interval);
//   - H > N:  the interval wraps through zero and covers iff
//     X > H || X < N.
//
// Equality with either endpoint is never a cover: an exact match is a
// different, stronger proof.
func CoversHash(owner, next, x []byte) bool {
	if len(owner) != len(next) || len(owner) != len(x) {
		return false
	}
	cmpON := bytes.Compare(owner, next)
	cmpOX := bytes.Compare(owner, x)
	cmpNX := bytes.Compare(next, x)
	switch {
	case cmpON == 0:
		// Whole-ring chain: covers everything except the owner itself.
		return cmpOX != 0
	case cmpON < 0:
		// Ordinary interval owner < x < next.
		return cmpOX < 0 && cmpNX > 0
	default:
		// Wrapping interval owner > next: the interval runs from owner
		// up through the ring zero to next, so x > owner or x < next.
		return cmpOX < 0 || cmpNX > 0
	}
}

// RecordCoversHash reports whether the given NSEC3 record's interval
// covers x.
func RecordCoversHash(r *Record, x []byte) bool {
	if r == nil || len(r.OwnerHash) != len(x) {
		return false
	}
	return CoversHash(r.OwnerHash, r.NextHash, x)
}
