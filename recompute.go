package netsec

import "sort"

// BatchSnapshot is the result of an independent from-scratch
// recomputation. Its algorithm deliberately differs from the incremental
// worklist engine: it seeds facts and then repeats full earliest-time
// sweeps over every edge until a global fixed point is reached.
type BatchSnapshot struct {
	Earliest []int
	BestEdge []int
	Edges    []Edge
	Facts    map[TermID]int
	Target   []TermID
}

// Recompute rebuilds the whole closure from the observed facts and the
// current derivation universe without consulting any incremental state.
func (e *Engine) Recompute() BatchSnapshot {
	n := len(e.terms)
	earliest := make([]int, n)
	for id := range earliest {
		earliest[id] = NoEvent
	}
	for id, at := range e.factAt {
		earliest[id] = at
	}

	edges := append([]edgeRec(nil), e.edges...)
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].head != edges[j].head {
			return edges[i].head < edges[j].head
		}
		// Same ordering the incremental evaluator uses: for equal
		// firing times the lowest rule rank always wins, so both
		// algorithms select the same shared derivation edge.
		return edges[i].rank < edges[j].rank
	})

	changed := true
	for changed {
		changed = false
		for idx := range edges {
			edge := &edges[idx]
			candidate, ok := batchEdgeTime(edge, earliest)
			if !ok || candidate >= earliest[edge.head] {
				continue
			}
			earliest[edge.head] = candidate
			changed = true
		}
	}

	bestEdge := make([]int, n)
	for id := range bestEdge {
		bestEdge[id] = -1
	}
	for id := 0; id < n; id++ {
		if earliest[id] == NoEvent {
			continue
		}
		for idx := range edges {
			edge := &edges[idx]
			if edge.head != TermID(id) {
				continue
			}
			candidate, ok := batchEdgeTime(edge, earliest)
			if !ok || candidate != earliest[edge.head] {
				continue
			}
			if bestEdge[edge.head] == -1 || edge.rank < edges[bestEdge[edge.head]].rank {
				bestEdge[edge.head] = idx
			}
		}
	}

	publicEdges := make([]Edge, len(edges))
	for idx := range edges {
		publicEdges[idx] = Edge{
			Head: edges[idx].head,
			Rule: edges[idx].rule,
			Deps: append([]TermID(nil), edges[idx].deps...),
		}
	}

	facts := make(map[TermID]int, len(e.factAt))
	for id, at := range e.factAt {
		facts[id] = at
	}

	return BatchSnapshot{
		Earliest: earliest,
		BestEdge: bestEdge,
		Edges:    publicEdges,
		Facts:    facts,
		Target:   append([]TermID(nil), e.targets...),
	}
}

func batchEdgeTime(edge *edgeRec, earliest []int) (int, bool) {
	if edge.rule == RuleFact {
		at := earliest[edge.head]
		if at == NoEvent {
			return 0, false
		}
		return at, true
	}
	latest := 0
	for _, dep := range edge.deps {
		at := earliest[dep]
		if at == NoEvent {
			return 0, false
		}
		if at > latest {
			latest = at
		}
	}
	return latest, true
}
