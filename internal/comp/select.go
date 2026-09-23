package comp

import (
	"bytes"
	"fmt"
	"sort"

	"dictcomp/internal/wire"
)

// Mode selects the dictionary-construction strategy.
type Mode int

const (
	// ModeAuto picks exact search for small inputs and greedy otherwise.
	ModeAuto Mode = iota
	// ModeExact demands exhaustive search; it fails when infeasible.
	ModeExact
	// ModeGreedy forces the heuristic.
	ModeGreedy
)

// Exact-search limits: exhaustive subset enumeration over candidates is
// only attempted when both bounds hold, keeping the search tractable.
const (
	maxExactCandidates = 12
	maxExactBytes      = 1024
	// greedyPoolSize bounds how many top-scoring candidates the heuristic
	// considers for the dictionary.
	greedyPoolSize = 512
)

// Selection is the result of dictionary construction.
type Selection struct {
	Dict       [][]byte
	Exact      bool  // true if the dictionary is provably optimal (exhaustive search)
	Candidates int   // repeated fragments considered
	Maximal    int   // candidates remaining after dominance filtering
	TrainTotal int64 // archive-model train bytes: one header + dictionary + token streams
	EmptyTotal int64 // same, with an empty dictionary
	GainBound  int64 // upper bound on the gain any dictionary could achieve on the train set
}

// OptimalFloor is a lower bound on the train-set total that any dictionary
// could achieve. In exact mode it equals TrainTotal.
func (s *Selection) OptimalFloor() int64 {
	f := s.EmptyTotal - s.GainBound
	if f < 0 {
		f = 0
	}
	return f
}

// SelectDictionary builds a dictionary from training samples. The result
// never depends on the order of samples. If no dictionary repays its own
// storage, Dict is nil and the totals say so honestly.
func SelectDictionary(samples [][]byte, minLen, maxLen int, mode Mode) (*Selection, error) {
	if minLen < 3 {
		return nil, fmt.Errorf("minLen must be >= 3: a reference token costs %d bytes", refTokenCost)
	}
	if minLen > maxLen {
		return nil, fmt.Errorf("minLen (%d) exceeds maxLen (%d)", minLen, maxLen)
	}
	if maxLen > wire.MaxEntryLen {
		return nil, fmt.Errorf("maxLen exceeds format limit %d", wire.MaxEntryLen)
	}
	// Canonical sample order: the dictionary depends on the multiset of
	// samples, never on the order they were supplied in.
	sorted := make([][]byte, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })

	cands := GenerateCandidates(sorted, minLen, maxLen)
	maximal := undominated(cands)
	sel := &Selection{Candidates: len(cands), Maximal: len(maximal)}

	// Upper bound on the gain of ANY dictionary (see dominate.go): every
	// match lies inside the occurrences of some undominated fragment u,
	// and within one occurrence of u a compatible set of matches saves at
	// most len(u)-refCost bytes. On top of that, an encoding can save at
	// most the literal-run control bytes of the unencoded corpus.
	var controlSlack int64
	for _, s := range sorted {
		controlSlack += int64(len(s))/litRunMax + 1
	}
	for _, c := range maximal {
		if per := int64(c.Count) * (int64(len(c.Str)) - refTokenCost); per > 0 {
			sel.GainBound += per
		}
	}
	sel.GainBound += controlSlack

	sel.EmptyTotal = corpusTotal(sorted, nil)

	var totalBytes int
	for _, s := range sorted {
		totalBytes += len(s)
	}
	exactFeasible := len(cands) <= maxExactCandidates && totalBytes <= maxExactBytes

	switch {
	case mode == ModeExact && !exactFeasible:
		return nil, fmt.Errorf("exact mode infeasible: %d candidates (max %d), %d train bytes (max %d)",
			len(cands), maxExactCandidates, totalBytes, maxExactBytes)
	case mode == ModeExact || (mode == ModeAuto && exactFeasible):
		dict, total := exactSelect(sorted, cands)
		sel.Dict = dict
		sel.Exact = true
		sel.TrainTotal = total
		sel.GainBound = sel.EmptyTotal - total // tight: the search was exhaustive
		return sel, nil
	}

	dict := greedySelect(sorted, maximal, greedyPoolSize)
	total := corpusTotal(sorted, dict)
	if total >= sel.EmptyTotal {
		// Honest reporting: the dictionary does not pay for itself.
		dict = nil
		total = sel.EmptyTotal
	}
	sel.Dict = dict
	sel.TrainTotal = total
	return sel, nil
}

// corpusTotal is the archive-model size of a corpus: one file header, the
// dictionary, and the optimal token stream of every sample.
func corpusTotal(samples [][]byte, dict [][]byte) int64 {
	total := wire.FileOverhead(dict)
	for _, s := range samples {
		_, c := OptimalParse(s, dict)
		total += c
	}
	return total
}

// exactSelect enumerates every subset of candidates and evaluates each with
// the exact optimal parse, returning the provably best dictionary (the
// empty dictionary is subset zero, so it is always considered).
func exactSelect(samples [][]byte, cands []Candidate) ([][]byte, int64) {
	k := len(cands)
	entries := make([][]byte, k)
	for i, c := range cands {
		entries[i] = []byte(c.Str)
	}
	best := corpusTotal(samples, nil)
	var bestDict [][]byte
	for mask := 1; mask < (1 << uint(k)); mask++ {
		var dict [][]byte
		for i := 0; i < k; i++ {
			if mask&(1<<uint(i)) != 0 {
				dict = append(dict, entries[i])
			}
		}
		if c := corpusTotal(samples, dict); c < best {
			best = c
			bestDict = dict
		}
	}
	return bestDict, best
}

// greedySelect is the heuristic used for larger inputs. It walks candidates
// in decreasing optimistic-score order and accepts a fragment only when its
// occurrences that are still compatible with already-claimed matches repay
// the fragment's storage. Compatibility is enforced with per-sample claimed
// interval sets, so overlapping matches are never counted twice.
func greedySelect(samples [][]byte, cands []Candidate, poolSize int) [][]byte {
	var pool []Candidate
	for _, c := range cands {
		if c.Score <= 0 {
			continue
		}
		pool = append(pool, c)
		if len(pool) >= poolSize {
			break
		}
	}
	if len(pool) == 0 {
		return nil
	}
	patterns := make([][]byte, len(pool))
	plens := make([]int, len(pool))
	for i, c := range pool {
		patterns[i] = []byte(c.Str)
		plens[i] = len(c.Str)
	}
	ac := buildAutomaton(patterns)

	// Collect occurrence positions per sample per pattern.
	positions := make([][][]int, len(samples))
	claimed := make([]*intervalSet, len(samples))
	for si, s := range samples {
		positions[si] = make([][]int, len(pool))
		claimed[si] = &intervalSet{}
		state := 0
		for i := 0; i < len(s); i++ {
			b := s[i]
			for {
				if ns, ok := ac.next[state][b]; ok {
					state = ns
					break
				}
				if state == 0 {
					break
				}
				state = ac.fail[state]
			}
			for _, id := range ac.out[state] {
				positions[si][id] = append(positions[si][id], i-plens[id]+1)
			}
		}
	}

	var dict [][]byte
	for id := range pool {
		l := plens[id]
		chosen := make([][][2]int, len(samples)) // per-sample intervals to claim
		uses := 0
		for si := range samples {
			lastEnd := -1
			for _, pos := range positions[si][id] {
				if pos < lastEnd {
					continue // overlaps an occurrence we already picked
				}
				if claimed[si].conflicts(pos, pos+l) {
					continue // conflicts with an already-claimed match
				}
				chosen[si] = append(chosen[si], [2]int{pos, pos + l})
				lastEnd = pos + l
				uses++
			}
		}
		gain := int64(uses)*(int64(l)-refTokenCost) - entryCost(l)
		if gain <= 0 {
			continue
		}
		for si := range samples {
			claimed[si].add(chosen[si])
		}
		dict = append(dict, patterns[id])
	}
	return dict
}

// intervalSet is a sorted, non-overlapping set of claimed [start, end)
// intervals. Claims are added in batches that are themselves sorted and
// conflict-free, so a merge keeps the set sorted.
type intervalSet struct {
	ivs [][2]int
}

// conflicts reports whether [a, b) overlaps any claimed interval.
func (s *intervalSet) conflicts(a, b int) bool {
	i := sort.Search(len(s.ivs), func(i int) bool { return s.ivs[i][1] > a })
	return i < len(s.ivs) && s.ivs[i][0] < b
}

// add merges a sorted batch of new intervals into the set.
func (s *intervalSet) add(batch [][2]int) {
	if len(batch) == 0 {
		return
	}
	merged := make([][2]int, 0, len(s.ivs)+len(batch))
	i, j := 0, 0
	for i < len(s.ivs) && j < len(batch) {
		if s.ivs[i][0] < batch[j][0] {
			merged = append(merged, s.ivs[i])
			i++
		} else {
			merged = append(merged, batch[j])
			j++
		}
	}
	merged = append(merged, s.ivs[i:]...)
	merged = append(merged, batch[j:]...)
	s.ivs = merged
}
