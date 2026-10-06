package posetmatch

import (
	"hash/fnv"
	"sort"
)

// Bounds for the exhaustive enumeration embedded in no-match reports and in
// the brute-force verifiers. Posets here hold at most a dozen events, but an
// unconstrained 12-event poset has 12! linear extensions, so enumeration is
// capped and the report records whether it closed.
const (
	// MaxExtensions caps how many linear extensions an ExhaustionReport or
	// BruteForceFind will enumerate before giving up (Complete == false).
	MaxExtensions = 2_000_000
	// MaxSampleOrders caps how many witness-free orders are kept verbatim
	// in an ExhaustionReport.
	MaxSampleOrders = 32
)

// Match is a positive result: Pattern occurs contiguously at
// Witness[Start:End], and Witness is a full linear extension of the poset.
type Match struct {
	Pattern []Label
	Witness []EventID
	Start   int
	End     int // exclusive
}

// Segment returns the event IDs realising the pattern inside the witness.
func (m *Match) Segment() []EventID { return m.Witness[m.Start:m.End] }

// ExhaustionReport documents a closed (or capped) search for a pattern that
// was not found: how many label assignments were tried and the full space of
// linear extensions that was enumerated to prove absence.
type ExhaustionReport struct {
	AssignmentsExplored int
	ExtensionsTotal     int
	Complete            bool // false if MaxExtensions truncated enumeration
	Digest              uint64
	Sample              [][]EventID
}

// SearchOutcome is the result of FindPattern: either a Match, or a Report
// proving the search space was closed.
type SearchOutcome struct {
	Found  bool
	Match  *Match
	Report *ExhaustionReport
}

// FindPattern decides whether pattern occurs as a contiguous run of labels
// in some linear extension of the poset.
//
// It enumerates injective assignments of pattern positions to distinct
// events with matching labels. An assignment e1..em is feasible iff
//  1. the poset augmented with the chain e1<...<em stays acyclic, and
//  2. no outside event x is forced between the endpoints (e1<x and x<em).
//
// Under both conditions the chain can be contracted to a single node and any
// linear extension of the contracted poset expands into a witness.
func (p *Poset) FindPattern(pattern []Label) SearchOutcome {
	if len(pattern) == 0 {
		w := p.TopoOrder()
		return SearchOutcome{Found: true, Match: &Match{Pattern: pattern, Witness: w}}
	}
	byLabel := make(map[Label][]EventID)
	for _, id := range p.ids {
		lbl := p.events[id].Label
		byLabel[lbl] = append(byLabel[lbl], id)
	}
	// Multiset feasibility: every label in the pattern must be available in
	// sufficient multiplicity.
	need := make(map[Label]int)
	for _, lbl := range pattern {
		need[lbl]++
		if len(byLabel[lbl]) < need[lbl] {
			return SearchOutcome{Found: false, Report: p.closeReport(0)}
		}
	}
	if len(pattern) > len(p.ids) {
		return SearchOutcome{Found: false, Report: p.closeReport(0)}
	}

	assign := make([]EventID, len(pattern))
	used := make(map[EventID]bool, len(pattern))
	explored := 0
	var match *Match

	var rec func(i int) bool
	rec = func(i int) bool {
		if i == len(pattern) {
			explored++
			if w, start, ok := p.tryAssignment(assign); ok {
				match = &Match{
					Pattern: append([]Label(nil), pattern...),
					Witness: w,
					Start:   start,
					End:     start + len(pattern),
				}
				return true
			}
			return false
		}
		for _, id := range byLabel[pattern[i]] {
			if used[id] {
				continue
			}
			if i > 0 && p.chainClosesCycle(assign[:i], id) {
				continue
			}
			assign[i] = id
			used[id] = true
			if rec(i + 1) {
				return true
			}
			used[id] = false
		}
		return false
	}
	if rec(0) {
		return SearchOutcome{Found: true, Match: match}
	}
	return SearchOutcome{Found: false, Report: p.closeReport(explored)}
}

// chainClosesCycle reports whether appending the chain edge
// chain[last] -> next to the poset augmented with the chain edges among
// chain would close a cycle, i.e. whether next already reaches chain[last].
func (p *Poset) chainClosesCycle(chain []EventID, next EventID) bool {
	target := chain[len(chain)-1]
	seen := make(map[EventID]bool)
	stack := []EventID{next}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true
		}
		if seen[cur] {
			continue
		}
		seen[cur] = true
		for to := range p.edges[cur] {
			stack = append(stack, to)
		}
		for i := 0; i+1 < len(chain); i++ {
			if chain[i] == cur {
				stack = append(stack, chain[i+1])
			}
		}
	}
	return false
}

// closureWithChain computes the transitive closure of the poset augmented
// with the chain edges assign[0] -> ... -> assign[m-1].
func (p *Poset) closureWithChain(assign []EventID) map[EventID]map[EventID]bool {
	adj := make(map[EventID][]EventID, len(p.ids))
	for _, id := range p.ids {
		for to := range p.edges[id] {
			adj[id] = append(adj[id], to)
		}
	}
	for i := 0; i+1 < len(assign); i++ {
		adj[assign[i]] = append(adj[assign[i]], assign[i+1])
	}
	cl := make(map[EventID]map[EventID]bool, len(p.ids))
	for _, id := range p.ids {
		reach := make(map[EventID]bool)
		stack := append([]EventID(nil), adj[id]...)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if reach[cur] {
				continue
			}
			reach[cur] = true
			stack = append(stack, adj[cur]...)
		}
		cl[id] = reach
	}
	return cl
}

// tryAssignment tests one full injective assignment e1..em and, on success,
// builds a witness linear extension by contracting the chain to a supernode.
func (p *Poset) tryAssignment(assign []EventID) (witness []EventID, start int, ok bool) {
	m := len(assign)
	inSeg := make(map[EventID]bool, m)
	for _, id := range assign {
		inSeg[id] = true
	}
	cl := p.closureWithChain(assign)
	first, last := assign[0], assign[m-1]

	// Condition 2: no outside event may be forced strictly between the
	// segment endpoints.
	for _, id := range p.ids {
		if inSeg[id] {
			continue
		}
		if cl[first][id] && cl[id][last] {
			return nil, 0, false
		}
	}

	// Contract the segment to a supernode S. x < S iff x reaches the last
	// segment event; S < x iff the first segment event reaches x. Condition 2
	// is exactly what keeps this contracted poset acyclic.
	const super = EventID("\x00segment")
	var realNodes []EventID
	for _, id := range p.ids {
		if !inSeg[id] {
			realNodes = append(realNodes, id)
		}
	}
	all := append(append([]EventID(nil), realNodes...), super)
	indeg := make(map[EventID]int, len(all))
	adj := make(map[EventID][]EventID, len(all))
	for _, id := range all {
		indeg[id] = 0
	}
	add := func(from, to EventID) {
		adj[from] = append(adj[from], to)
		indeg[to]++
	}
	for _, x := range realNodes {
		for to := range p.edges[x] {
			if !inSeg[to] {
				add(x, to)
			}
		}
		if cl[x][last] {
			add(x, super)
		}
		if cl[first][x] {
			add(super, x)
		}
	}

	used := make(map[EventID]bool, len(all))
	for emitted := 0; emitted < len(all); emitted++ {
		progress := false
		for _, id := range all {
			if used[id] || indeg[id] != 0 {
				continue
			}
			used[id] = true
			progress = true
			if id == super {
				start = len(witness)
				witness = append(witness, assign...)
			} else {
				witness = append(witness, id)
			}
			for _, to := range adj[id] {
				indeg[to]--
			}
			break
		}
		if !progress {
			return nil, 0, false // unreachable given condition 2
		}
	}
	return witness, start, true
}

// closeReport enumerates the whole space of linear extensions (up to
// MaxExtensions) to document a closed search.
func (p *Poset) closeReport(assignments int) *ExhaustionReport {
	rep := &ExhaustionReport{AssignmentsExplored: assignments}
	h := fnv.New64a()
	count, complete := p.LinearExtensions(MaxExtensions, func(order []EventID) bool {
		for _, id := range order {
			h.Write([]byte(id))
			h.Write([]byte{','})
		}
		h.Write([]byte{';'})
		if len(rep.Sample) < MaxSampleOrders {
			rep.Sample = append(rep.Sample, order)
		}
		return true
	})
	rep.ExtensionsTotal = count
	rep.Complete = complete
	rep.Digest = h.Sum64()
	return rep
}

// findPatternInOrder locates the first contiguous occurrence of pattern in a
// concrete order of events.
func (p *Poset) findPatternInOrder(order []EventID, pattern []Label) (int, bool) {
	if len(pattern) == 0 {
		return 0, true
	}
	if len(pattern) > len(order) {
		return 0, false
	}
	for i := 0; i+len(pattern) <= len(order); i++ {
		ok := true
		for j, lbl := range pattern {
			if p.events[order[i+j]].Label != lbl {
				ok = false
				break
			}
		}
		if ok {
			return i, true
		}
	}
	return 0, false
}

// BruteForceFind is the independent verifier: it enumerates every linear
// extension (up to MaxExtensions) and scans each for the pattern. It returns
// the first witness found. When the extension space exceeds MaxExtensions a
// negative answer is inconclusive.
func (p *Poset) BruteForceFind(pattern []Label) (witness []EventID, start int, found bool) {
	p.LinearExtensions(MaxExtensions, func(order []EventID) bool {
		if s, ok := p.findPatternInOrder(order, pattern); ok {
			witness = order
			start = s
			return false
		}
		return true
	})
	return witness, start, witness != nil
}

// PermutationFind is the full-permutation filter: it walks all n!
// permutations of the events, keeps those that are linear extensions, and
// scans each for the pattern. It exists to cross-check FindPattern on small
// posets (n <= 8 recommended) and to exercise duplicate labels and
// concurrent events.
func (p *Poset) PermutationFind(pattern []Label) (witness []EventID, start int, found bool) {
	perm := append([]EventID(nil), p.ids...)
	var rec func(i int) bool
	rec = func(i int) bool {
		if i == len(perm) {
			if !p.IsLinearExtension(perm) {
				return true
			}
			if s, ok := p.findPatternInOrder(perm, pattern); ok {
				witness = append([]EventID(nil), perm...)
				start = s
				return false
			}
			return true
		}
		for j := i; j < len(perm); j++ {
			perm[i], perm[j] = perm[j], perm[i]
			if !rec(i + 1) {
				return false
			}
			perm[i], perm[j] = perm[j], perm[i]
		}
		return true
	}
	rec(0)
	return witness, start, witness != nil
}

// CrossCheck runs FindPattern and the independent BruteForceFind verifier,
// reporting whether they agree. It is the library-level hook for full
// enumeration checking.
func (p *Poset) CrossCheck(pattern []Label) (outcome SearchOutcome, agree bool) {
	outcome = p.FindPattern(pattern)
	_, _, bruteFound := p.BruteForceFind(pattern)
	return outcome, outcome.Found == bruteFound
}

// TimeOrderCheck is the naive baseline the library exists to refute: sort
// events by timestamp and scan for the pattern. It is unsound in both
// directions — concurrent events may need reordering, and timestamp order
// may violate precedence — so it must never be used to decide matches.
func (p *Poset) TimeOrderCheck(pattern []Label) bool {
	evs := p.Events()
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].Time < evs[j].Time })
	order := make([]EventID, len(evs))
	for i, ev := range evs {
		order[i] = ev.ID
	}
	_, ok := p.findPatternInOrder(order, pattern)
	return ok
}
