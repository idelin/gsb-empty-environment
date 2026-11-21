package admission

import "sort"

// countVec is an aggregate count of accepted pairs by joint category. Only
// category counts are propagated across block boundaries; individual pairs
// are never stored.
type countVec struct {
	counts map[string]int
	total  int
}

func emptyVec() countVec {
	return countVec{counts: map[string]int{}}
}

func (v countVec) clone() countVec {
	c := make(map[string]int, len(v.counts))
	for k, n := range v.counts {
		c[k] = n
	}
	return countVec{counts: c, total: v.total}
}

// dominates reports whether a is component-wise no larger than b. Any quota
// that admits b therefore also admits a, so b can be pruned when a is kept.
func (a countVec) dominates(b countVec) bool {
	if a.total > b.total {
		return false
	}
	strictlySmaller := a.total < b.total
	// Compare every category appearing on either side.
	allCats := map[string]bool{}
	for cat := range a.counts {
		allCats[cat] = true
	}
	for cat := range b.counts {
		allCats[cat] = true
	}
	for cat := range allCats {
		av, bv := a.counts[cat], b.counts[cat]
		if av > bv {
			return false
		}
		if av < bv {
			strictlySmaller = true
		}
	}
	return strictlySmaller
}

// equalVec reports whether two aggregate vectors carry identical counts.
func (a countVec) equalVec(b countVec) bool {
	if a.total != b.total || len(a.counts) != len(b.counts) {
		return false
	}
	for cat, n := range a.counts {
		if b.counts[cat] != n {
			return false
		}
	}
	return true
}

func (a countVec) add(b countVec) countVec {
	c := a.clone()
	for cat, n := range b.counts {
		c.counts[cat] += n
	}
	c.total = a.total + b.total
	return c
}

// withinQuota reports whether the vector respects every aggregate quota.
func (p *problem) withinQuota(v countVec) bool {
	if p.quota.Total > 0 && v.total > p.quota.Total {
		return false
	}
	for cat, limit := range p.quota.MaxPerCategory {
		if v.counts[cat] > limit {
			return false
		}
	}
	return true
}

type localState struct {
	mask uint64
	vec  countVec
}

// blockInfo holds the reusable boundary-state machinery for one block.
type blockInfo struct {
	block    int
	groups   []int
	boundary []string
	bit      map[string]uint64

	children []int
	// parentMask marks boundary batches shared with the parent block.
	parentMask uint64
	// childMask[c] marks, in this block's bit space, the separator to child c.
	childMask map[int]uint64
	// childBitTranslate[c] maps a child-side separator bit to this block's bit.
	childBitTranslate map[int]map[uint64]uint64

	// local is the block-local enumeration, Pareto-pruned per boundary mask.
	local map[uint64][]localState
}

type solver struct {
	p       *problem
	infos   []blockInfo
	cache   []map[uint64][]countVec
	cacheOK []bool
	// recomputes[b] counts cache-miss evaluations of block b's frontier.
	recomputes []int
}

func newSolver(p *problem) *solver {
	s := &solver{
		p:          p,
		infos:      make([]blockInfo, len(p.blockEdges)),
		cache:      make([]map[uint64][]countVec, len(p.blockEdges)),
		cacheOK:    make([]bool, len(p.blockEdges)),
		recomputes: make([]int, len(p.blockEdges)),
	}
	// First pass: groups and boundary bits.
	for b := range p.blockEdges {
		s.buildBase(b)
	}
	// Second pass: tree relationships referencing neighbor bit spaces.
	for b := range p.blockEdges {
		s.buildRelationships(b)
	}
	// Third pass: local enumerations.
	for b := range p.blockEdges {
		s.infos[b].local = s.enumerateBlock(b, -1, false)
	}
	return s
}

func (s *solver) buildBase(b int) {
	p := s.p
	info := blockInfo{
		block:             b,
		bit:               make(map[string]uint64),
		childMask:         make(map[int]uint64),
		childBitTranslate: make(map[int]map[uint64]uint64),
	}

	groupSeen := make(map[int]bool)
	vertexSet := make(map[string]bool)
	for _, ei := range p.blockEdges[b] {
		gi := p.edgeGroup[ei]
		if !groupSeen[gi] {
			groupSeen[gi] = true
			info.groups = append(info.groups, gi)
		}
		e := p.edges[ei]
		vertexSet[e.L] = true
		vertexSet[e.R] = true
	}
	sort.Ints(info.groups)

	var boundary []string
	for v := range vertexSet {
		if p.boundaryBatches[v] {
			boundary = append(boundary, v)
		}
	}
	sort.Strings(boundary)
	info.boundary = boundary
	for i, v := range boundary {
		info.bit[v] = uint64(1) << uint(i)
	}
	s.infos[b] = info
}

func (s *solver) buildRelationships(b int) {
	p := s.p
	info := s.infos[b]
	for nb, sep := range p.blockTree[b] {
		var mask uint64
		for _, v := range sep {
			mask |= info.bit[v]
		}
		if p.blockParent[b] == nb {
			info.parentMask = mask
			continue
		}
		if p.blockParent[nb] != b {
			continue
		}
		info.children = append(info.children, nb)
		info.childMask[nb] = mask
		translate := make(map[uint64]uint64)
		for _, v := range sep {
			translate[s.infos[nb].bit[v]] = info.bit[v]
		}
		info.childBitTranslate[nb] = translate
	}
	sort.Ints(info.children)
	s.infos[b] = info
}

// enumerateBlock enumerates every legal local matching of the block over its
// decision groups. When forcedGroup >= 0 that group is fixed to included
// (forceInclude true) or excluded (false); all other groups remain free.
func (s *solver) enumerateBlock(b, forcedGroup int, forceInclude bool) map[uint64][]localState {
	p := s.p
	info := s.infos[b]

	internal := make(map[string]bool)
	vertexSet := make(map[string]bool)
	for _, gi := range info.groups {
		for _, ei := range p.groups[gi].edges {
			e := p.edges[ei]
			vertexSet[e.L] = true
			vertexSet[e.R] = true
		}
	}
	for v := range vertexSet {
		if _, isBoundary := info.bit[v]; !isBoundary {
			internal[v] = true
		}
	}

	out := make(map[uint64][]localState)

	var rec func(idx int, mask uint64, matched map[string]bool, vec countVec)
	rec = func(idx int, mask uint64, matched map[string]bool, vec countVec) {
		if idx == len(info.groups) {
			for v := range internal {
				if !matched[v] {
					return
				}
			}
			if !p.withinQuota(vec) {
				return
			}
			insertLocal(out, mask, localState{mask: mask, vec: vec})
			return
		}
		gi := info.groups[idx]

		// Exclude branch (not allowed when the group is forced included).
		if forcedGroup != gi || !forceInclude {
			rec(idx+1, mask, cloneBoolMap(matched), vec)
		}

		// Include branch (not allowed when the group is forced excluded).
		if forcedGroup == gi && !forceInclude {
			return
		}

		// Reject the include branch locally when a vertex is already matched,
		// without disturbing the exclude branch already taken above.
		for _, ei := range p.groups[gi].edges {
			e := p.edges[ei]
			if matched[e.L] || matched[e.R] {
				return
			}
		}
		nextMatched := cloneBoolMap(matched)
		nextVec := vec
		nextMask := mask
		for _, ei := range p.groups[gi].edges {
			e := p.edges[ei]
			nextMatched[e.L] = true
			nextMatched[e.R] = true
			if bit, ok := info.bit[e.L]; ok {
				nextMask |= bit
			}
			if bit, ok := info.bit[e.R]; ok {
				nextMask |= bit
			}
			nextVec = addEdge(nextVec, e)
		}
		rec(idx+1, nextMask, nextMatched, nextVec)
	}

	rec(0, 0, map[string]bool{}, emptyVec())
	return out
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func addEdge(v countVec, e Edge) countVec {
	c := v.clone()
	c.counts[e.JointCategory]++
	c.total++
	return c
}

func insertLocal(out map[uint64][]localState, mask uint64, candidate localState) {
	existing := out[mask]
	kept := make([]localState, 0, len(existing)+1)
	dominated := false
	for _, st := range existing {
		if st.vec.dominates(candidate.vec) {
			kept = append(kept, existing...)
			dominated = true
			break
		}
		if !candidate.vec.dominates(st.vec) {
			kept = append(kept, st)
		}
	}
	if !dominated {
		kept = append(kept, candidate)
	}
	out[mask] = kept
}

func insertVec(out map[uint64][]countVec, key uint64, candidate countVec) {
	existing := out[key]
	kept := make([]countVec, 0, len(existing)+1)
	dominated := false
	for _, v := range existing {
		if v.equalVec(candidate) {
			kept = append(kept, existing...)
			dominated = true
			break
		}
		if v.dominates(candidate) {
			kept = append(kept, existing...)
			dominated = true
			break
		}
		if !candidate.dominates(v) {
			kept = append(kept, v)
		}
	}
	if !dominated {
		kept = append(kept, candidate)
	}
	out[key] = kept
}

// ancestorPathSet returns the set of blocks on the path from the root to
// forcedBlock (inclusive). With no forced block it returns an empty set.
func (s *solver) ancestorPathSet(forcedBlock int) map[int]bool {
	set := map[int]bool{}
	if forcedBlock < 0 {
		return set
	}
	for b := forcedBlock; b != -2; b = s.p.blockParent[b] {
		set[b] = true
		if b == s.p.blockRoot {
			break
		}
	}
	return set
}

// frontier computes the Pareto frontier of aggregate vectors achievable by
// the subtree rooted at block b, keyed by which separator batches toward the
// parent are matched inside the subtree. Cached messages are reused for all
// blocks off the forced-block path; no global pairing is ever enumerated.
func (s *solver) frontier(b, forcedBlock, forcedGroup int, forceInclude bool) map[uint64][]countVec {
	if (forcedBlock < 0 || !s.subtreeContains(b, forcedBlock)) && s.cacheOK[b] {
		return s.cache[b]
	}
	s.recomputes[b]++
	info := s.infos[b]

	localConfigs := info.local
	if forcedBlock == b {
		localConfigs = s.enumerateBlock(b, forcedGroup, forceInclude)
	}

	childFrontiers := make(map[int]map[uint64][]countVec, len(info.children))
	for _, c := range info.children {
		childForced := -1
		if forcedBlock >= 0 && s.subtreeContains(c, forcedBlock) {
			childForced = forcedBlock
		}
		childFrontiers[c] = s.frontier(c, childForced, forcedGroup, forceInclude)
	}

	result := make(map[uint64][]countVec)
	for mask, states := range localConfigs {
		s.combineStates(b, mask, states, childFrontiers, result)
	}

	if forcedBlock < 0 {
		s.cache[b] = result
		s.cacheOK[b] = true
	}
	return result
}

// subtreeContains reports whether block target is inside the subtree rooted
// at block b, using parent pointers rooted at blockRoot.
func (s *solver) subtreeContains(b, target int) bool {
	for x := target; ; x = s.p.blockParent[x] {
		if x == b {
			return true
		}
		if x == s.p.blockRoot {
			return false
		}
	}
}

func (s *solver) combineStates(b int, localMask uint64, states []localState,
	childFrontiers map[int]map[uint64][]countVec, result map[uint64][]countVec) {
	info := s.infos[b]

	// Each child separator must agree on which shared batches are matched.
	compatible := make(map[int][]uint64)
	for c, frontier := range childFrontiers {
		translate := info.childBitTranslate[c]
		var masks []uint64
		// A shared batch is matched exactly once: either inside this block
		// (the local child-separator mask) or inside the child subtree (the
		// translated child mask), so the two must be bitwise complements on
		// the separator's bit space.
		for childMask := range frontier {
			var translated uint64
			for childBit, localBit := range translate {
				if childMask&childBit != 0 {
					translated |= localBit
				}
			}
			if translated|(localMask&info.childMask[c]) == info.childMask[c] &&
				translated&(localMask&info.childMask[c]) == 0 {
				masks = append(masks, childMask)
			}
		}
		if len(masks) == 0 {
			return
		}
		compatible[c] = masks
	}

	var recChild func(i int, vec countVec)
	recChild = func(i int, vec countVec) {
		if i == len(info.children) {
			if !s.p.withinQuota(vec) {
				return
			}
			// At the parent boundary only parent-separator bits matter; the
			// root has no parent, so its only valid message key is zero.
			var key uint64
			if b != s.p.blockRoot {
				key = localMask & info.parentMask
			}
			insertVec(result, key, vec)
			return
		}
		c := info.children[i]
		for _, childMask := range compatible[c] {
			for _, childVec := range childFrontiers[c][childMask] {
				merged := vec.add(childVec)
				if s.p.withinQuota(merged) {
					recChild(i+1, merged)
				}
			}
		}
	}

	for _, st := range states {
		recChild(0, st.vec)
	}
}

// feasible reports whether a complete pairing exists, optionally with one
// decision group forced to included or excluded.
func (s *solver) feasible(forcedGroup int, forceInclude bool) bool {
	forcedBlock := -1
	if forcedGroup >= 0 {
		forcedBlock = s.p.blockOfGroup[forcedGroup]
	}
	root := s.p.blockRoot
	frontier := s.frontier(root, forcedBlock, forcedGroup, forceInclude)
	vectors := frontier[0]
	return len(vectors) > 0
}
