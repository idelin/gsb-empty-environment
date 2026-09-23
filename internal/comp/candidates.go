package comp

import "sort"

// Candidate is a repeated fragment found in the training samples.
type Candidate struct {
	Str   string
	Count int   // total (possibly overlapping) occurrences across samples
	Score int64 // optimistic standalone net gain: Count*(len-refCost) - storage
}

// scanPositionsCap bounds the positions scanned per sample while hunting
// candidates; larger samples are scanned at a deterministic stride. This
// only affects the heuristic path, never correctness of what is reported.
const scanPositionsCap = 1 << 22

// GenerateCandidates finds every substring with length in [minLen, maxLen]
// that occurs at least twice in the training samples. The result is sorted
// deterministically and does not depend on the order of samples.
func GenerateCandidates(samples [][]byte, minLen, maxLen int) []Candidate {
	counts := map[string]int{}
	for _, s := range samples {
		stride := 1
		if len(s) > scanPositionsCap {
			stride = len(s)/scanPositionsCap + 1
		}
		for l := minLen; l <= maxLen && l <= len(s); l++ {
			for i := 0; i+l <= len(s); i += stride {
				counts[string(s[i:i+l])]++
			}
		}
	}
	out := make([]Candidate, 0, len(counts))
	for str, cnt := range counts {
		if cnt < 2 {
			continue
		}
		l := int64(len(str))
		score := int64(cnt)*(l-refTokenCost) - entryCost(int(l))
		out = append(out, Candidate{Str: str, Count: cnt, Score: score})
	}
	SortCandidates(out)
	return out
}

// SortCandidates orders candidates by decreasing score with total
// tie-breakers, so every downstream decision is deterministic.
func SortCandidates(cs []Candidate) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Score != cs[j].Score {
			return cs[i].Score > cs[j].Score
		}
		if len(cs[i].Str) != len(cs[j].Str) {
			return len(cs[i].Str) > len(cs[j].Str)
		}
		return cs[i].Str < cs[j].Str
	})
}
