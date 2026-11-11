package netsec

import (
	"math/bits"
	"sort"
	"time"
)

var timeNow = time.Now

// memoEntry is the proven best result of one search state, identified by the
// earliest uncovered event index and the set of consumed events. suffix lists
// candidate indices selected inside that subtree; addedSpan is their combined
// span. A state is memoized only after its whole subtree is exhausted, so a
// cache hit always represents a proven-optimal local result.
type memoEntry struct {
	count     int
	addedSpan int64
	suffix    []int
}

type localResult struct {
	count     int
	addedSpan int64
	suffix    []int
}

// tick charges one enumeration or search step. It returns false once the work
// or wall-clock budget has been exhausted.
func (s *solver) tick() bool {
	if s.stopped {
		return false
	}
	s.work++
	if s.work >= s.budget {
		s.stopped = true
		s.reason = "work budget exhausted"
		return false
	}
	if s.hasClock && s.work&1023 == 0 && !timeNow().Before(s.deadline) {
		s.stopped = true
		s.reason = "time budget exhausted"
		return false
	}
	return true
}

// search runs event-index branch and bound with state memoization. At every
// node the earliest uncovered event must either stay unused or be consumed by
// one candidate containing it, so each feasible selection is explored once.
func (s *solver) search() {
	s.memo = make([]map[uint32]memoEntry, len(s.req.Events)+1)
	for i := range s.memo {
		s.memo[i] = map[uint32]memoEntry{}
	}

	result := s.dfs(0, 0, 0)
	if s.stopped && s.reason == "" {
		s.reason = "budget exhausted"
	}
	if s.stopped {
		// Keep the greedy incumbent; do not adopt a partial subtree result.
		return
	}
	s.adoptWinner(result.suffix, result.addedSpan)
}

func (s *solver) dfs(nextEvent int, used uint32, prefixCount int) localResult {
	if s.stopped {
		return localResult{}
	}
	if !s.tick() {
		return localResult{}
	}

	n := len(s.req.Events)
	first := nextEvent
	for first < n && used&(1<<uint(first)) != 0 {
		first++
	}
	if first == n {
		return localResult{}
	}

	if cached, ok := s.memo[first][used]; ok {
		return localResult{
			count:     cached.count,
			addedSpan: cached.addedSpan,
			suffix:    append([]int(nil), cached.suffix...),
		}
	}

	if !s.canBeatIncumbent(first, used, prefixCount) {
		return localResult{}
	}

	best := localResult{}

	// Branch 1: leave the earliest uncovered event unused.
	best = s.dfs(first+1, used, prefixCount)
	if s.stopped {
		return localResult{}
	}

	// Branch 2: consume it with one compatible candidate. The per-event
	// candidate order is canonical (span, length, template, event IDs), so the
	// first best result a state reaches is also its lexicographically
	// smallest one, making local count/span comparison sufficient.
	for _, ci := range s.byMin[first] {
		cand := s.cands[ci]
		if cand.mask&used != 0 {
			continue
		}
		child := s.dfs(first+1, used|cand.mask, prefixCount+1)
		if s.stopped {
			return localResult{}
		}
		candidate := localResult{
			count:     child.count + 1,
			addedSpan: child.addedSpan + cand.span,
			suffix:    append([]int{ci}, child.suffix...),
		}
		if localBetter(candidate, best) {
			best = candidate
		}
	}

	if !s.stopped {
		s.memo[first][used] = memoEntry{
			count:     best.count,
			addedSpan: best.addedSpan,
			suffix:    append([]int(nil), best.suffix...),
		}
	}
	return best
}

func localBetter(a, b localResult) bool {
	if a.count != b.count {
		return a.count > b.count
	}
	return a.addedSpan < b.addedSpan
}

// canBeatIncumbent applies a safe cardinality upper bound. Any chain added
// from this node consumes at least minRemaining distinct unused events, so the
// number of additional chains is bounded by unused/minRemaining. Prune when
// even that bound cannot reach the incumbent cardinality.
func (s *solver) canBeatIncumbent(first int, used uint32, prefixCount int) bool {
	n := len(s.req.Events)
	eventsLeft := 0
	minRemaining := -1
	for evIdx := first; evIdx < n; evIdx++ {
		bit := uint32(1) << uint(evIdx)
		if used&bit == 0 {
			eventsLeft++
			if length := s.minLenEv[evIdx]; length != -1 &&
				(minRemaining == -1 || length < minRemaining) {
				minRemaining = length
			}
		}
	}
	room := 0
	if minRemaining > 0 {
		room = eventsLeft / minRemaining
	}
	return prefixCount+room >= s.winnerCount
}

// adoptWinner replaces the incumbent when a complete feasible selection wins
// on the ordered objective: most chains, then smallest total span, then the
// smallest event-ID fingerprint.
func (s *solver) adoptWinner(chosen []int, span int64) {
	count := len(chosen)
	if count < s.winnerCount {
		return
	}
	if count > s.winnerCount || span < s.winnerSpan ||
		(span == s.winnerSpan && s.fingerprintLess(chosen, s.winner)) {
		s.winner = append([]int(nil), chosen...)
		s.winnerCount = count
		s.winnerSpan = span
	}
}

// fingerprintLess compares assignments as ordered lists of chain signatures;
// each signature is the sorted tuple of consumed event IDs plus the template
// name. Sorting makes the comparison independent of discovery order.
func (s *solver) fingerprintLess(a, b []int) bool {
	sigA := s.fingerprint(a)
	sigB := s.fingerprint(b)
	for i := 0; i < len(sigA) && i < len(sigB); i++ {
		if sigA[i] != sigB[i] {
			return sigA[i] < sigB[i]
		}
	}
	return len(sigA) < len(sigB)
}

func (s *solver) fingerprint(selection []int) []string {
	sigs := make([]string, 0, len(selection))
	for _, ci := range selection {
		cand := s.cands[ci]
		ids := make([]string, 0, len(cand.events)+1)
		for _, evIdx := range cand.events {
			ids = append(ids, s.req.Events[evIdx].ID)
		}
		sort.Strings(ids)
		ids = append(ids, s.req.Templates[cand.template].Name)
		sigs = append(sigs, joinSignature(ids))
	}
	sort.Strings(sigs)
	return sigs
}

func joinSignature(parts []string) string {
	total := 0
	for _, part := range parts {
		total += len(part) + 1
	}
	out := make([]byte, 0, total)
	for i, part := range parts {
		if i > 0 {
			out = append(out, 0)
		}
		out = append(out, part...)
	}
	return string(out)
}

// buildAssignment converts the winning internal selection into the public
// result, ordering chains by their first consumed event for stable output.
func (s *solver) buildAssignment() *Assignment {
	selected := append([]int(nil), s.winner...)
	sort.Slice(selected, func(a, b int) bool {
		return s.cands[selected[a]].minIdx < s.cands[selected[b]].minIdx
	})

	chains := make([]Chain, 0, len(selected))
	var used uint32
	for _, ci := range selected {
		cand := s.cands[ci]
		if cand.mask&used != 0 {
			panic("netsec: internal error, overlapping chains in assignment")
		}
		used |= cand.mask
		ids := make([]string, len(cand.events))
		for k, evIdx := range cand.events {
			ids[k] = s.req.Events[evIdx].ID
		}
		chains = append(chains, Chain{
			TemplateName: s.req.Templates[cand.template].Name,
			EventIDs:     ids,
		})
	}

	return &Assignment{
		Chains:        chains,
		EventCount:    bits.OnesCount32(used),
		TotalSpan:     s.winnerSpan,
		ProvenOptimal: !s.stopped,
		Complete:      !s.stopped,
		Reason:        s.reason,
	}
}
