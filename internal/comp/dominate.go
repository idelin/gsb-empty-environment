package comp

// Dominance filtering.
//
// A candidate B is dominated when some one-byte extension A of B (one byte
// prepended or appended) occurs exactly as often as B. Occurrence counts are
// monotonic under extension, so equality of counts means B occurs only
// inside occurrences of A: every match of B lies inside a match of A, and A
// saves at least as many bytes there. Dominated fragments therefore add
// nothing to either the candidate pool or the gain bound.
//
// The undominated set yields a valid upper bound on the gain of ANY
// dictionary: every match of every possible dictionary entry lies inside
// the occurrences of some undominated fragment u, and within one occurrence
// of u a compatible set of matches saves at most len(u)-2 bytes (each match
// token costs 2). Overlaps between different u's regions only inflate the
// sum, which keeps it an upper bound.

// undominated returns the candidates that are not dominated, preserving
// their deterministic order.
func undominated(cands []Candidate) []Candidate {
	cnt := make(map[string]int, len(cands))
	for _, c := range cands {
		cnt[c.Str] = c.Count
	}
	dominated := make(map[string]struct{}, len(cands))
	for _, c := range cands {
		s := c.Str
		if len(s) < 2 {
			continue
		}
		// Any length-(len(s)-1) substring of s with the same count is
		// dominated by s.
		if sub := s[1:]; cnt[sub] == c.Count {
			dominated[sub] = struct{}{}
		}
		if sub := s[:len(s)-1]; cnt[sub] == c.Count {
			dominated[sub] = struct{}{}
		}
	}
	out := cands[:0:0]
	for _, c := range cands {
		if _, ok := dominated[c.Str]; !ok {
			out = append(out, c)
		}
	}
	return out
}
