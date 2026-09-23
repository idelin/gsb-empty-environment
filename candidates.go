package main

import (
	"bytes"
	"sort"
)

// candidate is a repeated substring considered for the dictionary.
type candidate struct {
	phrase []byte
	count  int // max number of non-overlapping occurrences in the train set
	gain   int // standalone gain: count*(len-matchTokenCost) - entry storage
}

// dictEntryCost is the serialized per-entry overhead inside a dictionary file.
func dictEntryCost(phraseLen int) int { return 1 + phraseLen }

// standaloneGain estimates the saving of a phrase used count times, ignoring
// interactions with other phrases. Overlapping occurrences of the phrase
// itself are already excluded by the non-overlapping count.
func standaloneGain(phraseLen, count int) int {
	return count*(phraseLen-matchTokenCost) - dictEntryCost(phraseLen)
}

// maxCandidatesCap bounds the candidate set so counting stays tractable on
// large inputs. Generation is deterministic, so truncation is deterministic.
const maxCandidatesCap = 2000000

// generateCandidates finds all substrings of length [minLen, maxLen] that
// repeat in the train set and could have positive standalone gain. Samples
// must be sorted by the caller so the result does not depend on input order.
func generateCandidates(samples [][]byte, minLen, maxLen int) (cands []candidate, truncated bool) {
	// Count overlapping occurrences per distinct substring first; it is a
	// cheap upper bound that prunes hopeless candidates before the exact
	// non-overlapping count.
	overlap := make(map[string]int)
	total := 0
	for _, s := range samples {
		for i := 0; i < len(s); i++ {
			for l := minLen; l <= maxLen && i+l <= len(s); l++ {
				if total >= maxCandidatesCap {
					truncated = true
					break
				}
				overlap[string(s[i:i+l])]++
				total++
			}
			if truncated {
				break
			}
		}
		if truncated {
			break
		}
	}
	// Deterministic iteration order.
	keys := make([]string, 0, len(overlap))
	for k, cnt := range overlap {
		if standaloneGain(len(k), cnt) > 0 { // overlapping count overestimates, so this filter is safe
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := []byte(k)
		cnt := 0
		for _, s := range samples {
			cnt += countNonOverlapping(s, p)
		}
		if g := standaloneGain(len(p), cnt); g > 0 {
			cands = append(cands, candidate{phrase: p, count: cnt, gain: g})
		}
	}
	return cands, truncated
}

// countNonOverlapping returns the maximum number of non-overlapping
// occurrences of p in s. Leftmost-greedy is optimal for equal-length
// intervals, so this is an upper bound on the uses of p in any parse.
func countNonOverlapping(s, p []byte) int {
	cnt := 0
	for i := 0; i+len(p) <= len(s); {
		if j := bytes.Index(s[i:], p); j >= 0 {
			cnt++
			i += j + len(p)
		} else {
			break
		}
	}
	return cnt
}

// sortSamplesCanonical orders samples deterministically by content so the
// result of training does not depend on the order samples were supplied.
func sortSamplesCanonical(samples [][]byte) {
	sort.Slice(samples, func(i, j int) bool {
		return bytes.Compare(samples[i], samples[j]) < 0
	})
}
