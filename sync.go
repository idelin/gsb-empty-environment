package gsb

// Sync performs one push-pull anti-entropy exchange between two nodes:
//
//  1. Compare 4-byte digest CRCs; equal means no work.
//  2. On mismatch, exchange per-interval digests to locate the divergent
//     hash-ring intervals.
//  3. Pull (and push) only the members inside those intervals and merge
//     both ways.
//
// It reports whether a divergence was detected and reconciled.
func Sync(a, b *Node) bool {
	if a.DigestSum() == b.DigestSum() {
		return false
	}
	diff := a.digest.DiffSegments(&b.digest)
	aMembers := a.membersInSegments(diff)
	bMembers := b.membersInSegments(diff)
	a.Merge(bMembers)
	b.Merge(aMembers)
	return true
}
