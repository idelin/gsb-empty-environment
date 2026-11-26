// Package bgp is documented in types.go. This file anchors the overview and
// the canonical usage example.
//
// Typical workflow:
//
//	sel := bgp.NewSelector(bgp.Config{
//	    LocalAS:         65000,
//	    MemberAS:        64512,              // zero when not a confederation
//	    RouterID:        mustID("10.0.0.1"),
//	    ClusterID:       mustID("10.0.0.2"), // zero when not an RR
//	    DeterministicMED: true,
//	    AlwaysCompareMED: false,
//	    MissingMED:       bgp.MissingMEDAsZero,
//	})
//
//	result := sel.Select(candidates)        // legality filter + best path
//	if result.Best == nil {
//	    // every candidate was rejected; see result.Rejected
//	}
//
//	reflections := sel.Reflect(result.Best, peers)
//	for _, entry := range reflections.Entries {
//	    if entry.Advert == nil {
//	        continue // entry.Reason explains the suppression
//	    }
//	    send(entry.Target, entry.Advert.Path)
//	}
//
// # Design guarantees
//
// DeterministicMED groups candidates by the first non-confederation
// neighbouring AS, fully orders each group (MED always compared inside the
// group), and only then compares group winners using the standard tie-break
// sequence. Cross-group MED participates solely when AlwaysCompareMED is set.
// Because every comparator step and the final peer-IP / caller-ID tie break
// form a total order, the output is identical for every input permutation.
//
// Confederation handling follows RFC 5065: AS_CONFED_SEQUENCE and
// AS_CONFED_SET never contribute to AS_PATH length and are skipped while
// deriving the neighbouring AS. Route-reflector loop prevention follows
// RFC 4456: a path whose ORIGINATOR_ID equals the local RouterID or whose
// CLUSTER_LIST contains the local ClusterID is discarded, and reflected
// updates prepend the local ClusterID while preserving ORIGINATOR_ID.
package bgp
