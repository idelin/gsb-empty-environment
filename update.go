package dom

import "sort"

// UpdateResult describes the outcome of an incremental edge update.
type UpdateResult struct {
	// Analysis is the recomputed analysis. When the update cannot change
	// anything (duplicate edge, missing edge, edge sourced at an
	// unreachable node) it is the receiver itself.
	Analysis *Analysis
	// Affected lists the nodes whose dominator information was
	// recomputed, in ascending order.
	Affected []int
	// BecameReachable / BecameUnreachable list the nodes that changed
	// reachability because of the update.
	BecameReachable   []int
	BecameUnreachable []int
}

// AddEdge inserts from->to into the underlying graph and recomputes only
// the affected region: the nodes reachable from `to`, which are the only
// ones whose path sets (and hence dominators) can change. Nodes outside
// the region keep their previous dominator sets and form a fixed boundary
// for the recomputation.
func (a *Analysis) AddEdge(from, to int) *UpdateResult {
	res := &UpdateResult{Analysis: a}
	if !a.g.AddEdge(from, to) {
		return res // duplicate edge: nothing changes
	}
	if !a.reachableBefore(from) {
		return res // an unreachable source cannot gain paths
	}
	return a.recompute(a.regionFrom(to, a.g.reachableSet(nil)), res)
}

// RemoveEdge deletes from->to from the underlying graph and recomputes
// only the affected region: the nodes that were reachable from `to` before
// the removal, which are the only ones whose paths could have used the
// deleted edge.
func (a *Analysis) RemoveEdge(from, to int) *UpdateResult {
	res := &UpdateResult{Analysis: a}
	if !a.g.HasEdge(from, to) {
		return res
	}
	region := map[int]struct{}{}
	if a.reachableBefore(from) {
		region = a.regionFrom(to, a.reachable)
	}
	a.g.RemoveEdge(from, to)
	return a.recompute(region, res)
}

// regionFrom collects the nodes reachable from start in the current graph,
// intersected with limit.
func (a *Analysis) regionFrom(start int, limit map[int]struct{}) map[int]struct{} {
	region := make(map[int]struct{})
	for n := range a.g.reachableFrom([]int{start}, nil) {
		if _, ok := limit[n]; ok {
			region[n] = struct{}{}
		}
	}
	return region
}

func (a *Analysis) reachableBefore(n int) bool {
	_, ok := a.reachable[n]
	return ok
}

// recompute builds a new Analysis in which only region is recomputed;
// every other reachable node keeps its previous dominator information.
func (a *Analysis) recompute(region map[int]struct{}, res *UpdateResult) *UpdateResult {
	g := a.g
	na := &Analysis{
		g:         g,
		reachable: g.reachableSet(nil),
		dom:       make(map[int]map[int]struct{}, len(a.dom)),
		idom:      make(map[int]int, len(a.idom)),
	}
	// Carry over the untouched nodes.
	for n := range na.reachable {
		if _, inRegion := region[n]; inRegion {
			continue
		}
		na.dom[n] = copySet(a.dom[n])
		if d, ok := a.idom[n]; ok {
			na.idom[n] = d
		}
	}
	for _, n := range g.Nodes() {
		if _, ok := na.reachable[n]; !ok {
			na.unreachable = append(na.unreachable, n)
		}
	}
	// Recompute the region nodes that are still reachable.
	live := make(map[int]struct{}, len(region))
	for n := range region {
		if _, ok := na.reachable[n]; ok {
			live[n] = struct{}{}
		}
	}
	na.computeRegion(live)
	na.deriveIDom(live)

	for n := range region {
		if a.reachableBefore(n) || na.Reachable(n) {
			res.Affected = append(res.Affected, n)
		}
		if !a.reachableBefore(n) && na.Reachable(n) {
			res.BecameReachable = append(res.BecameReachable, n)
		}
		if a.reachableBefore(n) && !na.Reachable(n) {
			res.BecameUnreachable = append(res.BecameUnreachable, n)
		}
	}
	sort.Ints(res.Affected)
	sort.Ints(res.BecameReachable)
	sort.Ints(res.BecameUnreachable)
	res.Analysis = na
	return res
}
