package prescreen

// MaxSlices is the maximum number of request or authorization subspaces per
// object. It bounds the inclusion-exclusion fan-out to 2^MaxSlices.
const MaxSlices = 12

// IntersectionTerm is one non-empty inclusion-exclusion term: the
// intersection of the affine sets indexed by Sets.
type IntersectionTerm struct {
	Sets []int
	Aff  Affine
	// Sign is +1 for odd-sized subsets and -1 for even-sized ones.
	Sign int8
}

// UnionProof holds every non-empty intersection term and the resulting
// inclusion-exclusion cardinality. It is an algebraic certificate: verifying
// it only needs ranks, never block enumeration.
type UnionProof struct {
	Terms     []IntersectionTerm
	UnionSize uint64
}

// UnionInclusionExclusion computes |union(sets)| by enumerating only the
// 2^n-1 index combinations (n <= MaxSlices), intersecting affines and
// alternating their ranks. Empty intersections prune all supersets.
func UnionInclusionExclusion(sets []Affine) UnionProof {
	proof := UnionProof{Terms: make([]IntersectionTerm, 0, 1<<uint(len(sets)))}
	var dfs func(start int, cur Affine, idx []int)
	dfs = func(start int, cur Affine, idx []int) {
		for i := start; i < len(sets); i++ {
			next := cur.Intersect(sets[i])
			if next.Empty {
				continue
			}
			nextIdx := append(idx, i)
			proof.Terms = append(proof.Terms, IntersectionTerm{
				Sets: append([]int(nil), nextIdx...),
				Aff:  next,
				Sign: signFor(len(nextIdx)),
			})
			dfs(i+1, next, nextIdx)
		}
	}
	dfs(0, FullSpace(), nil)
	var unionSize uint64
	for _, term := range proof.Terms {
		if term.Sign > 0 {
			unionSize += term.Aff.Size()
		} else {
			unionSize -= term.Aff.Size()
		}
	}
	proof.UnionSize = unionSize
	return proof
}

// CoverUnion tests algebraically whether covered is contained in the union of
// the covering affine sets. Coverage holds exactly when the two
// cardinalities are equal; the verifier recomputes every rank.
func CoverUnion(covered Affine, covering []Affine) (coveredSize, unionSize uint64, proof UnionProof, ok bool) {
	if covered.Empty {
		return 0, 0, UnionProof{}, true
	}
	intersections := make([]Affine, len(covering))
	nonEmpty := make([]Affine, 0, len(covering))
	for i, set := range covering {
		intersections[i] = covered.Intersect(set)
		if !intersections[i].Empty {
			nonEmpty = append(nonEmpty, intersections[i])
		}
	}
	proof = UnionInclusionExclusion(nonEmpty)
	return covered.Size(), proof.UnionSize, proof, proof.UnionSize == covered.Size()
}

// affineKey is a deterministic canonical identity for an affine set, used to
// detect physical aliases (identical images from different request slices).
func affineKey(af Affine) string {
	if af.Empty {
		return "\x00empty"
	}
	buf := make([]byte, 0, 1+len(af.Dir)*8+8)
	buf = append(buf, byte(len(af.Dir)))
	for _, d := range af.Dir {
		var b [8]byte
		putUint64(b[:], uint64(d))
		buf = append(buf, b[:]...)
	}
	var b [8]byte
	putUint64(b[:], uint64(af.Offset))
	buf = append(buf, b[:]...)
	return string(buf)
}

func putUint64(b []byte, v uint64) {
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (8 * (7 - i)))
	}
}
