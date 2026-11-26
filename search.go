package archiveproof

import (
	"fmt"
	"math/bits"
	"sort"
)

// Options controls the exhaustive search and clocking enumeration.  The zero
// value is useful: sensible defaults are substituted.
type Options struct {
	// MaxExactCandidates is the largest candidate set for which the minimum
	// certificate is found by exhaustive branch-and-bound.  Larger archives
	// use a deterministic shortest-path union followed by minimality pruning.
	MaxExactCandidates int

	// MaxExactClockings is the largest literal integer clock domain that is
	// enumerated cell by cell.
	MaxExactClockings int

	// MaxScenarios caps representative clockings for large domains.
	MaxScenarios int
}

func (o Options) normalized() Options {
	if o.MaxExactCandidates <= 0 {
		o.MaxExactCandidates = 24
	}
	if o.MaxExactCandidates > 63 {
		o.MaxExactCandidates = 63
	}
	if o.MaxExactClockings <= 0 {
		o.MaxExactClockings = defaultMaxExactClockings
	}
	if o.MaxScenarios <= 0 {
		o.MaxScenarios = defaultMaxScenarios
	}
	return o
}

// BuildCertificate validates an archive, enumerates the compatible clockings,
// and returns a fixed certificate.  Approved records are disclosed in full;
// hidden records only through licensed shared commitments.  Candidate graphs
// up to Options.MaxExactCandidates are minimized exactly; larger graphs use a
// deterministic shortest-path union followed by redundancy elimination.
func BuildCertificate(a *Archive, options ...*Options) (*Certificate, error) {
	var o Options
	if len(options) > 0 && options[0] != nil {
		o = *options[0]
	}
	o = o.normalized()
	if err := a.Validate(); err != nil {
		return nil, err
	}
	idx, err := a.buildIndex()
	if err != nil {
		return nil, err
	}
	domains, err := a.ClockDomains()
	if err != nil {
		return nil, err
	}
	cs, domains, err := buildCandidateSet(a, idx, domains)
	if err != nil {
		return nil, err
	}
	if cs.len() == 0 {
		return nil, fmt.Errorf("%w: no approved or licensed evidence", ErrNoCertificate)
	}
	edges := buildEdges(a, idx, cs)
	acceptGroups, err := resolveReferenceGroups(cs, idx, a.Acceptance)
	if err != nil {
		return nil, err
	}
	genesisGroups, err := resolveReferenceGroups(cs, idx, a.Genesis)
	if err != nil {
		return nil, err
	}
	genesis := flattenGroups(genesisGroups)
	if len(genesis) == 0 {
		return nil, fmt.Errorf("%w: no resolvable genesis", ErrNoCertificate)
	}
	cs, edges, _, _ = pruneCandidates(cs, edges, acceptGroups, genesis)
	acceptGroups, err = resolveReferenceGroups(cs, idx, a.Acceptance)
	if err != nil {
		return nil, err
	}
	genesisGroups, err = resolveReferenceGroups(cs, idx, a.Genesis)
	if err != nil {
		return nil, err
	}
	genesis = flattenGroups(genesisGroups)
	if len(genesis) == 0 {
		return nil, fmt.Errorf("%w: no resolvable genesis", ErrNoCertificate)
	}
	constraints := constraintsForEdges(cs, edges)
	clockings, err := enumerateClockings(domains, constraints, o.MaxExactClockings, o.MaxScenarios)
	if err != nil {
		return nil, err
	}
	scenarios, err := buildScenarioGraphs(cs, edges, acceptGroups, genesis, clockings)
	if err != nil {
		return nil, err
	}

	var selected []int
	if cs.len() <= o.MaxExactCandidates {
		selected, err = exactSelection(cs, scenarios)
	} else {
		selected, err = greedySelection(cs, scenarios)
	}
	if err != nil {
		return nil, err
	}

	cert, err := assembleCertificate(cs, domains, selected, a.Genesis, genesisGroups, a.Acceptance, acceptGroups)
	if err != nil {
		return nil, err
	}
	if err := cert.Validate(); err != nil {
		return nil, err
	}
	if err := cert.ValidateAgainst(a); err != nil {
		return nil, err
	}
	return cert, nil
}

// pruneCandidates removes records that cannot lie on any acceptance-to-genesis
// path even when every renewal edge is treated as valid.  Static reachability
// is deliberately optimistic: clocking-dependent validity can only remove
// edges, so this pruning never removes a record that a real clocking needs.
func pruneCandidates(cs *candidateSet, edges []edge, acceptGroups [][]int, genesis []int) (*candidateSet, []edge, [][]int, []int) {
	n := cs.len()
	fwd := make([][]int, n)
	rev := make([][]int, n)
	for _, e := range edges {
		fwd[e.from] = append(fwd[e.from], e.to)
		rev[e.to] = append(rev[e.to], e.from)
	}
	reachableFromAccept := make([]bool, n)
	queue := make([]int, 0, n)
	for _, g := range acceptGroups {
		for _, v := range g {
			if v >= 0 && v < n && !reachableFromAccept[v] {
				reachableFromAccept[v] = true
				queue = append(queue, v)
			}
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range fwd[v] {
			if !reachableFromAccept[w] {
				reachableFromAccept[w] = true
				queue = append(queue, w)
			}
		}
	}
	reachesGenesis := make([]bool, n)
	queue = queue[:0]
	for _, v := range genesis {
		if v >= 0 && v < n && !reachesGenesis[v] {
			reachesGenesis[v] = true
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range rev[v] {
			if !reachesGenesis[w] {
				reachesGenesis[w] = true
				queue = append(queue, w)
			}
		}
	}
	keep := make([]bool, n)
	for i := 0; i < n; i++ {
		if reachableFromAccept[i] && reachesGenesis[i] {
			keep[i] = true
		}
	}
	// Endpoints stay, even if there is no path; the scenario builder will
	// report the precise ErrNoCertificate.
	for _, g := range acceptGroups {
		for _, v := range g {
			if v >= 0 && v < n {
				keep[v] = true
			}
		}
	}
	for _, v := range genesis {
		if v >= 0 && v < n {
			keep[v] = true
		}
	}
	remap := make([]int, n)
	for i := range remap {
		remap[i] = -1
	}
	cs2 := &candidateSet{
		nodeIdx:      map[NodeID]int{},
		commitIdx:    map[Commitment]int{},
		hiddenCommit: cs.hiddenCommit,
	}
	for i := 0; i < n; i++ {
		if !keep[i] {
			continue
		}
		it := cs.items[i]
		remap[i] = cs2.add(it)
	}
	var edges2 []edge
	for _, e := range edges {
		from, to := remap[e.from], remap[e.to]
		if from < 0 || to < 0 {
			continue
		}
		edges2 = append(edges2, edge{from: from, to: to, gap: e.gap})
	}
	groups2 := make([][]int, 0, len(acceptGroups))
	for _, g := range acceptGroups {
		var ng []int
		for _, v := range g {
			if remap[v] >= 0 {
				ng = append(ng, remap[v])
			}
		}
		if len(ng) == 0 {
			// Should be impossible after forcing endpoints to stay.
			groups2 = append(groups2, nil)
		} else {
			groups2 = append(groups2, dedupeInts(ng))
		}
	}
	genesis2 := make([]int, 0, len(genesis))
	for _, v := range genesis {
		if remap[v] >= 0 {
			genesis2 = append(genesis2, remap[v])
		}
	}
	genesis2 = dedupeInts(genesis2)
	return cs2, edges2, groups2, genesis2
}

func flattenGroups(groups [][]int) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, g := range groups {
		for _, v := range g {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	sort.Ints(out)
	return out
}

type searchProblem struct {
	n         int
	scenarios []scenarioGraph
	lowerMemo map[uint64]int
}

func exactSelection(cs *candidateSet, scenarios []scenarioGraph) ([]int, error) {
	p := &searchProblem{n: cs.len(), scenarios: scenarios, lowerMemo: make(map[uint64]int)}
	lb := p.lowerBound(0)
	if lb >= infCost {
		return nil, fmt.Errorf("%w: some clocking has no permitted chain", ErrNoCertificate)
	}
	order := candidateOrder(cs, scenarios)
	for k := lb; k <= p.n; k++ {
		var dfs func(pos int, mask uint64, budget int) (uint64, bool)
		dfs = func(pos int, mask uint64, budget int) (uint64, bool) {
			if p.allConnected(mask) {
				return mask, true
			}
			if budget == 0 {
				return 0, false
			}
			if lb := p.lowerBound(mask); lb > budget {
				return 0, false
			}
			if pos >= len(order) {
				return 0, false
			}
			v := order[pos]
			if bits.OnesCount64(mask) >= k {
				return 0, false
			}
			if out, ok := dfs(pos+1, mask|(uint64(1)<<uint(v)), budget-1); ok {
				return out, true
			}
			return dfs(pos+1, mask, budget)
		}
		if mask, ok := dfs(0, 0, k); ok {
			return maskIndices(mask, p.n), nil
		}
	}
	return nil, fmt.Errorf("%w: exhaustive search found no certificate", ErrNoCertificate)
}

func maskIndices(mask uint64, n int) []int {
	out := make([]int, 0, bits.OnesCount64(mask))
	for i := 0; i < n; i++ {
		if mask&(uint64(1)<<uint(i)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

const infCost = int(^uint(0) >> 1)

func (p *searchProblem) allConnected(mask uint64) bool {
	for i := range p.scenarios {
		if !scenarioConnected(mask, &p.scenarios[i]) {
			return false
		}
	}
	return true
}

func scenarioConnected(mask uint64, sc *scenarioGraph) bool {
	for _, group := range sc.acceptGroups {
		ok := false
		for _, a := range group {
			if !nodeAllowed(mask, a) {
				continue
			}
			if pathExists(sc.adj, mask, a, sc.genesis) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func (p *searchProblem) lowerBound(mask uint64) int {
	if v, ok := p.lowerMemo[mask]; ok {
		return v
	}
	lb := 0
	for i := range p.scenarios {
		v := scenarioAdditional(mask, &p.scenarios[i])
		if v > lb {
			lb = v
		}
		if lb >= infCost {
			break
		}
	}
	p.lowerMemo[mask] = lb
	return lb
}

// scenarioAdditional is a 0-1 BFS lower bound on how many unselected
// candidates must be added for this scenario alone.  Selected candidates cost
// zero; unselected ones cost one.  Taking the maximum across scenarios is a
// valid lower bound for a shared certificate.
func scenarioAdditional(mask uint64, sc *scenarioGraph) int {
	best := 0
	for _, group := range sc.acceptGroups {
		d := groupAdditional(mask, sc, group)
		if d > best {
			best = d
		}
	}
	return best
}

func groupAdditional(mask uint64, sc *scenarioGraph, starts []int) int {
	n := len(sc.adj)
	dist := make([]int, n)
	for i := range dist {
		dist[i] = infCost
	}
	deque := make([]int, 0, n)
	pushFront := func(v int) { deque = append([]int{v}, deque...) }
	pushBack := func(v int) { deque = append(deque, v) }
	for _, a := range starts {
		if a < 0 || a >= n {
			continue
		}
		cost := 0
		if !nodeAllowed(mask, a) {
			cost = 1
		}
		if cost < dist[a] {
			dist[a] = cost
			if cost == 0 {
				pushFront(a)
			} else {
				pushBack(a)
			}
		}
	}
	goal := make([]bool, n)
	for _, g := range sc.genesis {
		if g >= 0 && g < n {
			goal[g] = true
		}
	}
	for len(deque) > 0 {
		v := deque[0]
		deque = deque[1:]
		if goal[v] {
			return dist[v]
		}
		for _, w := range sc.adj[v] {
			if w < 0 || w >= n {
				continue
			}
			cost := 1
			if nodeAllowed(mask, w) {
				cost = 0
			}
			nd := dist[v] + cost
			if nd >= dist[w] {
				continue
			}
			dist[w] = nd
			if cost == 0 {
				pushFront(w)
			} else {
				pushBack(w)
			}
		}
	}
	return infCost
}

func candidateOrder(cs *candidateSet, scenarios []scenarioGraph) []int {
	score := make([]int, cs.len())
	// Endpoint evidence is usually on every useful path.
	for i := range scenarios {
		for _, group := range scenarios[i].acceptGroups {
			for _, v := range group {
				score[v] += 3
			}
		}
		for _, v := range scenarios[i].genesis {
			score[v] += 2
		}
		for _, group := range scenarios[i].acceptGroups {
			for _, a := range group {
				if p := findPath(scenarios[i].adj, allNodesMask, a, scenarios[i].genesis); p != nil {
					for _, v := range p {
						score[v]++
					}
				}
			}
		}
	}
	order := make([]int, cs.len())
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		if score[order[i]] != score[order[j]] {
			return score[order[i]] > score[order[j]]
		}
		return order[i] < order[j]
	})
	return order
}

func greedySelection(cs *candidateSet, scenarios []scenarioGraph) ([]int, error) {
	selected := make(map[int]bool)
	for i := range scenarios {
		for _, group := range scenarios[i].acceptGroups {
			var path []int
			for _, a := range group {
				if p := findPath(scenarios[i].adj, allNodesMask, a, scenarios[i].genesis); p != nil {
					path = p
					break
				}
			}
			if path == nil {
				return nil, fmt.Errorf("%w: no permitted chain", ErrNoCertificate)
			}
			for _, v := range path {
				selected[v] = true
			}
		}
	}
	// Prune in descending usefulness order; repeat because pruning one node
	// can make another redundant.
	for {
		freq := make([]int, cs.len())
		for i := range scenarios {
			for _, group := range scenarios[i].acceptGroups {
				for _, a := range group {
					if !selected[a] {
						continue
					}
					if p := restrictedPath(scenarios[i].adj, selected, a, scenarios[i].genesis); p != nil {
						for _, v := range p {
							freq[v]++
						}
					}
				}
			}
		}
		order := make([]int, 0, len(selected))
		for v := range selected {
			order = append(order, v)
		}
		sort.Slice(order, func(i, j int) bool {
			if freq[order[i]] != freq[order[j]] {
				return freq[order[i]] < freq[order[j]]
			}
			return order[i] < order[j]
		})
		removed := false
		for _, v := range order {
			delete(selected, v)
			if allScenariosConnected(scenarios, selected) {
				removed = true
				continue
			}
			selected[v] = true
		}
		if !removed {
			break
		}
	}
	out := make([]int, 0, len(selected))
	for v := range selected {
		out = append(out, v)
	}
	sort.Ints(out)
	return out, nil
}

func allScenariosConnected(scenarios []scenarioGraph, selected map[int]bool) bool {
	for i := range scenarios {
		sc := &scenarios[i]
		for _, group := range sc.acceptGroups {
			ok := false
			for _, a := range group {
				if !selected[a] {
					continue
				}
				if restrictedPath(sc.adj, selected, a, sc.genesis) != nil {
					ok = true
					break
				}
			}
			if !ok {
				return false
			}
		}
	}
	return true
}

func restrictedPath(adj [][]int, allowed map[int]bool, start int, goals []int) []int {
	if !allowed[start] {
		return nil
	}
	goal := make(map[int]bool, len(goals))
	for _, g := range goals {
		goal[g] = true
	}
	prev := make([]int, len(adj))
	for i := range prev {
		prev[i] = -2
	}
	queue := []int{start}
	prev[start] = -1
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if goal[v] {
			var rev []int
			for u := v; u != -1; u = prev[u] {
				rev = append(rev, u)
			}
			for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
				rev[i], rev[j] = rev[j], rev[i]
			}
			return rev
		}
		for _, w := range adj[v] {
			if !allowed[w] || prev[w] != -2 {
				continue
			}
			prev[w] = v
			queue = append(queue, w)
		}
	}
	return nil
}
