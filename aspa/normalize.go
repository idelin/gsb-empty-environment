package aspa

import (
	"fmt"
	"sort"
	"strings"
)

// normalize flattens AS_PATH segments into the ordered, verifiable
// AS sequence per RFC conventions:
//
//   - AS_CONFED_SEQUENCE and AS_CONFED_SET segments are confederation
//     interior and are stripped before verification.
//   - AS_SEQUENCE ASNs are appended in received order, preserving
//     prepends (duplicate ASNs); duplicates are handled as trivial
//     self-pairs during pair checking.
//   - An AS_SET is unordered, so adjacency across it cannot be
//     determined; hasSet reports true and the caller must return
//     Unknown rather than Valid.
//
// The returned sequence is neighbor-first (seq[0] is the AS that sent
// the route to us). canonical is a stable string form of the full
// path (including sets) used as part of the cache key.
func normalize(path []Segment) (seq []ASN, canonical string, hasSet bool) {
	var b strings.Builder
	for _, seg := range path {
		switch seg.Type {
		case SegSequence:
			for _, a := range seg.ASNs {
				seq = append(seq, a)
				fmt.Fprintf(&b, "%d-", uint32(a))
			}
		case SegSet:
			hasSet = true
			sorted := make([]ASN, len(seg.ASNs))
			copy(sorted, seg.ASNs)
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
			b.WriteString("SET(")
			for i, a := range sorted {
				if i > 0 {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, "%d", uint32(a))
			}
			b.WriteString(")-")
		case SegConfedSequence, SegConfedSet:
			// Confederation segments are not part of the
			// verifiable path and are dropped.
		}
	}
	return seq, strings.TrimSuffix(b.String(), "-"), hasSet
}
