package archiveproof

import (
	"encoding/binary"
	"fmt"
	"sort"
)

type candidate struct {
	index  int
	node   *Node
	shared *SharedEvidence
}

func (c candidate) source() ServiceID {
	if c.node != nil {
		return c.node.ClockSource()
	}
	return c.shared.Source
}

func (c candidate) timestamp() int64 {
	if c.node != nil {
		return c.node.Time
	}
	return c.shared.Time
}

func (c candidate) offset() Offset {
	if c.node != nil {
		return c.node.Offset
	}
	return c.shared.Offset
}

func (c candidate) ref() Reference {
	if c.node != nil {
		return RefNode(c.node.ID)
	}
	return RefCommitment(c.shared.Commitment)
}

type candidateSet struct {
	items        []candidate
	nodeIdx      map[NodeID]int
	commitIdx    map[Commitment]int
	hiddenCommit map[NodeID]Commitment
}

func (s *candidateSet) add(c candidate) int {
	c.index = len(s.items)
	s.items = append(s.items, c)
	if c.node != nil {
		s.nodeIdx[c.node.ID] = c.index
	} else {
		s.commitIdx[c.shared.Commitment] = c.index
	}
	return c.index
}

func (s *candidateSet) len() int { return len(s.items) }

func buildCandidateSet(a *Archive, idx *archiveIndex, domains map[ServiceID]Offset) (*candidateSet, map[ServiceID]Offset, error) {
	cs := &candidateSet{
		nodeIdx:      make(map[NodeID]int),
		commitIdx:    make(map[Commitment]int),
		hiddenCommit: make(map[NodeID]Commitment),
	}
	for id, n := range idx.hidden {
		cs.hiddenCommit[id] = n.NodeCommitment()
	}
	// Only approved nodes are ever certificate candidates.
	nodeIDs := make([]NodeID, 0, len(idx.approved))
	for id := range idx.approved {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	for _, id := range nodeIDs {
		cs.add(candidate{node: idx.approved[id]})
	}

	licenses := a.licenseMap(idx)

	outDomains := make(map[ServiceID]Offset, len(domains))
	for k, v := range domains {
		outDomains[k] = v
	}
	// A licensed shared clock source that is not grounded in an approved
	// record gets its own domain.  Multiple entries for that source are
	// intersected; entries that cannot cover the resulting domain are dropped.
	sharedIDs := make([]Commitment, 0, len(licenses))
	for c := range licenses {
		sharedIDs = append(sharedIDs, c)
	}
	sort.Slice(sharedIDs, func(i, j int) bool { return sharedIDs[i].String() < sharedIDs[j].String() })
	for _, c := range sharedIDs {
		s := idx.shared[c]
		if _, ok := outDomains[s.Source]; ok {
			continue
		}
		outDomains[s.Source] = s.Offset
	}
	// Recompute candidate-only source domains as the intersection of all
	// licensed entries referencing that source.
	candidateOnly := map[ServiceID]bool{}
	for _, c := range sharedIDs {
		s := idx.shared[c]
		if _, grounded := domains[s.Source]; !grounded {
			candidateOnly[s.Source] = true
		}
	}
	for src := range candidateOnly {
		first := true
		var dom Offset
		for _, c := range sharedIDs {
			s := idx.shared[c]
			if s.Source != src {
				continue
			}
			if first {
				dom = s.Offset
				first = false
				continue
			}
			next, ok := dom.Intersect(s.Offset)
			if !ok {
				// Keep the first domain and simply omit incompatible entries;
				// selecting either family remains possible.
				break
			}
			dom = next
		}
		if !first {
			outDomains[src] = dom
		}
	}
	for _, c := range sharedIDs {
		s := idx.shared[c]
		dom, ok := outDomains[s.Source]
		if !ok || !s.Offset.ContainsInterval(dom) {
			continue
		}
		pub := s
		if pub.LicensedBy == "" {
			cp := *s
			cp.LicensedBy = licenses[c]
			pub = &cp
		}
		cs.add(candidate{shared: pub})
	}
	return cs, outDomains, nil
}

type edge struct {
	from int
	to   int
	gap  Gap
}

func buildEdges(a *Archive, idx *archiveIndex, cs *candidateSet) []edge {
	var edges []edge
	seen := make(map[[4]int64]struct{})
	add := func(from, to int, gap Gap) {
		if from < 0 || to < 0 || from >= cs.len() || to >= cs.len() || from == to {
			return
		}
		if !gap.Valid() {
			return
		}
		k := [4]int64{int64(from), int64(to), gap.Lo, gap.Hi}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		edges = append(edges, edge{from: from, to: to, gap: gap})
	}
	// A hidden node can be represented by any shared commitment whose signed
	// cover set includes it.
	covers := make(map[NodeID][]int)
	for i := range cs.items {
		it := cs.items[i]
		if it.shared == nil {
			continue
		}
		for _, id := range it.shared.Covers {
			covers[id] = append(covers[id], i)
		}
	}
	resolveRenewal := func(r Renewal) []int {
		if r.OldID != "" {
			if i, ok := cs.nodeIdx[r.OldID]; ok {
				return []int{i}
			}
			if !r.OldCommitment.IsZero() {
				if i, ok := cs.commitIdx[r.OldCommitment]; ok {
					return []int{i}
				}
			}
			if c, ok := cs.hiddenCommit[r.OldID]; ok {
				if i, ok := cs.commitIdx[c]; ok {
					return []int{i}
				}
			}
			return covers[r.OldID]
		}
		if !r.OldCommitment.IsZero() {
			if i, ok := cs.commitIdx[r.OldCommitment]; ok {
				return []int{i}
			}
		}
		return nil
	}
	for i := range cs.items {
		it := cs.items[i]
		if it.node != nil {
			for _, link := range it.node.Commits {
				if j, ok := cs.commitIdx[link.Commitment]; ok {
					add(i, j, link.Gap)
				}
			}
			for _, r := range it.node.Renews {
				for _, j := range resolveRenewal(r) {
					add(i, j, r.Gap)
				}
			}
			continue
		}
		for _, r := range it.shared.RenewsNodes {
			for _, j := range resolveRenewal(r) {
				add(i, j, r.Gap)
			}
		}
		for _, r := range it.shared.RenewsShared {
			if j, ok := cs.commitIdx[r.To]; ok {
				add(i, j, r.Gap)
			}
		}
	}
	return edges
}

func constraintsForEdges(cs *candidateSet, edges []edge) []ClockConstraint {
	out := make([]ClockConstraint, 0, len(edges))
	for _, e := range edges {
		f := cs.items[e.from]
		t := cs.items[e.to]
		out = append(out, ClockConstraint{
			FromSource: f.source(),
			ToSource:   t.source(),
			FromTime:   f.timestamp(),
			ToTime:     t.timestamp(),
			Gap:        e.gap,
		})
	}
	return out
}

func resolveReferenceGroups(cs *candidateSet, idx *archiveIndex, refs []Reference) ([][]int, error) {
	groups := make([][]int, 0, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		if ref.Node != "" {
			if i, ok := cs.nodeIdx[ref.Node]; ok {
				groups = append(groups, []int{i})
				continue
			}
			if _, hidden := idx.hidden[ref.Node]; hidden {
				var group []int
				if c, ok := cs.hiddenCommit[ref.Node]; ok {
					if i, ok := cs.commitIdx[c]; ok {
						group = append(group, i)
					}
				}
				for i := range cs.items {
					it := cs.items[i]
					if it.shared == nil {
						continue
					}
					for _, id := range it.shared.Covers {
						if id == ref.Node {
							group = append(group, i)
							break
						}
					}
				}
				if len(group) == 0 {
					return nil, fmt.Errorf("%w: hidden record %q has no licensed shared commitment", ErrNoCertificate, ref.Node)
				}
				groups = append(groups, dedupeInts(group))
				continue
			}
			return nil, fmt.Errorf("%w: reference to node %q", ErrNoCertificate, ref.Node)
		}
		i, ok := cs.commitIdx[ref.Commitment]
		if !ok {
			return nil, fmt.Errorf("%w: reference to commitment %s", ErrNoCertificate, ref.Commitment)
		}
		groups = append(groups, []int{i})
	}
	return groups, nil
}

func dedupeInts(in []int) []int {
	if len(in) < 2 {
		return in
	}
	seen := make(map[int]struct{}, len(in))
	out := in[:0]
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

type scenarioGraph struct {
	adj          [][]int
	acceptGroups [][]int
	genesis      []int
}

const allNodesMask = ^uint64(0)

func buildScenarioGraphs(cs *candidateSet, edges []edge, acceptGroups [][]int, genesis []int, clockings []Clocking) ([]scenarioGraph, error) {
	out := make([]scenarioGraph, 0, len(clockings))
	seen := make(map[string]struct{})
	for _, clk := range clockings {
		adj := make([][]int, cs.len())
		for _, e := range edges {
			f := cs.items[e.from]
			t := cs.items[e.to]
			ok, err := gapAllows(f.timestamp(), t.timestamp(), f.source(), t.source(), e.gap, clk)
			if err != nil {
				return nil, err
			}
			if ok {
				adj[e.from] = append(adj[e.from], e.to)
			}
		}
		sg := scenarioGraph{adj: adj, acceptGroups: acceptGroups, genesis: genesis}
		for _, group := range acceptGroups {
			ok := false
			for _, a := range group {
				if pathExists(adj, allNodesMask, a, genesis) {
					ok = true
					break
				}
			}
			if !ok {
				return nil, fmt.Errorf("%w: no permitted chain at clocking %v", ErrNoCertificate, clk)
			}
		}
		if cs.len() <= 63 {
			key := scenarioAdjacencyKey(adj)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
		}
		out = append(out, sg)
	}
	return out, nil
}

func scenarioAdjacencyKey(adj [][]int) string {
	buf := make([]byte, 8*len(adj))
	for i, row := range adj {
		var mask uint64
		for _, v := range row {
			if v >= 0 && v < 64 {
				mask |= uint64(1) << uint(v)
			}
		}
		binary.LittleEndian.PutUint64(buf[8*i:], mask)
	}
	return string(buf)
}

func pathExists(adj [][]int, mask uint64, start int, goals []int) bool {
	return findPath(adj, mask, start, goals) != nil
}

func nodeAllowed(mask uint64, w int) bool {
	if mask == allNodesMask {
		return true
	}
	if w < 0 || w >= 64 {
		return false
	}
	return mask&(uint64(1)<<uint(w)) != 0
}

func findPath(adj [][]int, mask uint64, start int, goals []int) []int {
	if start < 0 || start >= len(adj) || !nodeAllowed(mask, start) {
		return nil
	}
	goal := make(map[int]bool, len(goals))
	for _, g := range goals {
		if g >= 0 && g < len(adj) {
			goal[g] = true
		}
	}
	if len(goal) == 0 {
		return nil
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
			if w < 0 || w >= len(adj) || !nodeAllowed(mask, w) {
				continue
			}
			if prev[w] != -2 {
				continue
			}
			prev[w] = v
			queue = append(queue, w)
		}
	}
	return nil
}
