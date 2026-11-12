package netsec

import "sort"

func (a *analyzer) buildResult() *Result {
	res := &Result{
		States: map[string]StateView{},
		Labels: a.labels,
	}

	// Export fixpoint in-states.
	for id := range a.nodes {
		in := a.inState(id)
		view := StateView{Versions: map[Target]VersionView{}}
		for t, v := range in.versions {
			view.Versions[t] = VersionView{Label: v.label, Top: v.top}
		}
		for f := range in.facts {
			view.Facts = append(view.Facts, FactView{
				Check:   f.check,
				Target:  f.target,
				Version: VersionView{Label: f.bound.label, Top: f.bound.top},
			})
		}
		sort.Slice(view.Facts, func(i, j int) bool {
			if view.Facts[i].Check != view.Facts[j].Check {
				return view.Facts[i].Check < view.Facts[j].Check
			}
			return view.Facts[i].Target.Field < view.Facts[j].Target.Field
		})
		res.States[id] = view
	}

	ids := make([]string, 0, len(a.nodes))
	for id := range a.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := a.nodes[id]
		if n.Kind != NodeRead {
			continue
		}
		in := a.inState(id)
		for _, t := range a.readTargets[id] {
			if a.hasValidFact(in, t) {
				continue
			}
			path, invalidator, kind := a.findGapPath(id, t)
			res.Gaps = append(res.Gaps, Gap{
				Read:        id,
				Check:       a.factCheck(in, t),
				Target:      t,
				Path:        path,
				Invalidator: invalidator,
				Kind:        kind,
				Message:     a.gapMessage(kind, id, t, invalidator),
			})
		}
	}
	sort.Slice(res.Gaps, func(i, j int) bool {
		if res.Gaps[i].Read != res.Gaps[j].Read {
			return res.Gaps[i].Read < res.Gaps[j].Read
		}
		return res.Gaps[i].Target.Field < res.Gaps[j].Target.Field
	})
	return res
}

func (a *analyzer) hasValidFact(s *flowState, t Target) bool {
	for f := range s.facts {
		if f.target == t && !f.bound.top && s.versions[t] == f.bound {
			return true
		}
	}
	return false
}

func (a *analyzer) factCheck(s *flowState, t Target) string {
	for f := range s.facts {
		if f.target == t {
			return f.check
		}
	}
	return ""
}

// findGapPath reconstructs a concrete entry-to-read node sequence witnessing
// the gap. Backward search from the read:
//
//   - an invalidating edge (write/call that may touch target) witnesses an
//     "invalidated" gap;
//   - an edge whose source still holds a valid fact is a safe branch and is
//     pruned;
//   - a branch reaching the entry without a dominating check witnesses an
//     "unchecked" gap (used as a fallback).
//
// findGapPath builds a concrete entry->read witness for a gap.
//
// For an invalidated gap it returns a path that is guaranteed to contain the
// invalidating write/call: a suffix from the invalidator to the read, prefixed
// by a prefix from the entry to the invalidator. An unchecked gap simply
// returns any entry->read path.
func (a *analyzer) findGapPath(readID string, t Target) (path []string, invalidator, kind string) {
	// Full backward BFS (no pruning): records successor pointers across every
	// reachable predecessor so cyclic graphs stay finite via the visited set.
	parent, anchor, invalidator := a.backwardFull(readID, t)

	if invalidator == "" {
		return a.walkChain(parent, anchor, readID), "", GapUnchecked
	}

	// Suffix: invalidator -> read along the recorded successor chain.
	suffix := a.walkChain(parent, invalidator, readID)
	// Prefix: entry -> invalidator, via an independent backward search.
	prefixParent, prefixAnchor := a.backwardToEntry(invalidator)
	prefix := a.walkChain(prefixParent, prefixAnchor, invalidator)

	out := append([]string{}, prefix...)
	out = append(out, suffix[1:]...) // drop duplicated invalidator
	return out, invalidator, GapInvalidated
}

// backwardFull BFSs every predecessor of the read without pruning. It returns
// the successor-pointer map, an entry anchor (a node with no predecessors), and
// the first invalidating node seen on the search (empty when there is none).
func (a *analyzer) backwardFull(readID string, t Target) (map[string]string, string, string) {
	parent := map[string]string{}
	visited := map[string]bool{readID: true}
	queue := []string{readID}
	anchor := ""
	invalidator := ""
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		preds := a.preds[cur]
		if len(preds) == 0 && anchor == "" {
			anchor = cur
		}
		for _, p := range preds {
			if visited[p] {
				continue
			}
			visited[p] = true
			parent[p] = cur
			if invalidator == "" && a.invalidates(a.nodes[p], t) {
				invalidator = p
			}
			queue = append(queue, p)
		}
	}
	if anchor == "" {
		anchor = readID
	}
	return parent, anchor, invalidator
}

// backwardToEntry BFSs predecessors until nodes with no predecessors, and
// returns the successor-pointer map plus one such entry anchor.
func (a *analyzer) backwardToEntry(terminal string) (map[string]string, string) {
	parent := map[string]string{}
	visited := map[string]bool{terminal: true}
	queue := []string{terminal}
	anchor := ""
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		preds := a.preds[cur]
		if len(preds) == 0 && anchor == "" {
			anchor = cur
		}
		for _, p := range preds {
			if visited[p] {
				continue
			}
			visited[p] = true
			parent[p] = cur
			queue = append(queue, p)
		}
	}
	if anchor == "" {
		anchor = terminal
	}
	return parent, anchor
}

// walkChain follows successor pointers parent[x] from start to terminal.
func (a *analyzer) walkChain(parent map[string]string, start, terminal string) []string {
	out := []string{start}
	cur := start
	for cur != terminal {
		next, ok := parent[cur]
		if !ok {
			out = append(out, terminal)
			break
		}
		out = append(out, next)
		cur = next
	}
	return out
}

// invalidates reports whether node n may write target t through any alias.
func (a *analyzer) invalidates(n Node, t Target) bool {
	var writes []Target
	switch n.Kind {
	case NodeWrite:
		writes = a.writeTargets[n.ID]
	case NodeCall:
		writes = a.callTargets[n.ID]
	default:
		return false
	}
	for _, w := range writes {
		if overlaps(w, t) {
			return true
		}
	}
	return false
}
