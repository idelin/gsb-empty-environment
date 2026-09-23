package comp

import (
	"bytes"
	"sort"

	"dictcomp/internal/wire"
)

// This file is an independent exponential-time reference implementation.
// It exists to cross-check the optimized solvers in tests and is only
// feasible for tiny inputs.

// BruteBest enumerates every dictionary built from repeated substrings and
// returns the best archive-model total. Unlike exactSelect it applies no
// score filtering and uses an independent recursive parser.
func BruteBest(samples [][]byte, minLen, maxLen int) ([][]byte, int64) {
	set := map[string]struct{}{}
	for _, s := range samples {
		for l := minLen; l <= maxLen && l <= len(s); l++ {
			for i := 0; i+l <= len(s); i++ {
				if countOccurrences(samples, s[i:i+l]) >= 2 {
					set[string(s[i:i+l])] = struct{}{}
				}
			}
		}
	}
	frags := make([]string, 0, len(set))
	for f := range set {
		frags = append(frags, f)
	}
	sort.Strings(frags)
	best := bruteCorpusTotal(samples, nil)
	var bestDict [][]byte
	for mask := 1; mask < (1 << uint(len(frags))); mask++ {
		var dict [][]byte
		for i, f := range frags {
			if mask&(1<<uint(i)) != 0 {
				dict = append(dict, []byte(f))
			}
		}
		if c := bruteCorpusTotal(samples, dict); c < best {
			best = c
			bestDict = dict
		}
	}
	return bestDict, best
}

func countOccurrences(samples [][]byte, sub []byte) int {
	n := 0
	for _, s := range samples {
		for i := 0; i+len(sub) <= len(s); i++ {
			if bytes.Equal(s[i:i+len(sub)], sub) {
				n++
			}
		}
	}
	return n
}

func bruteCorpusTotal(samples [][]byte, dict [][]byte) int64 {
	total := wire.FileOverhead(dict)
	for _, s := range samples {
		total += BruteParseCost(s, dict)
	}
	return total
}

// BruteParseCost is a straightforward recursive exhaustive parser with
// memoization. It shares no code with OptimalParse.
func BruteParseCost(s []byte, dict [][]byte) int64 {
	memo := map[int]int64{}
	var solve func(pos int) int64
	solve = func(pos int) int64 {
		if pos >= len(s) {
			return 0
		}
		if v, ok := memo[pos]; ok {
			return v
		}
		best := int64(1) << 62
		maxRun := len(s) - pos
		if maxRun > litRunMax {
			maxRun = litRunMax
		}
		for l := 1; l <= maxRun; l++ {
			if c := int64(1+l) + solve(pos+l); c < best {
				best = c
			}
		}
		for _, e := range dict {
			if len(e) <= len(s)-pos && bytes.Equal(s[pos:pos+len(e)], e) {
				if c := int64(refTokenCost) + solve(pos+len(e)); c < best {
					best = c
				}
			}
		}
		memo[pos] = best
		return best
	}
	return solve(0)
}
