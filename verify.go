package main

import (
	"fmt"
	"sort"
)

// verifyByRebuild cross-checks every answer against a graph rebuilt from
// scratch at the query's own moment: the event stream is replayed in
// order with reference counting, and at each query event the current
// edge multiset is materialized and used to validate the answer.
//   - connected answers: the reported path must be a real u-v path in
//     the rebuilt snapshot (endpoints + consecutive adjacency);
//   - disconnected answers: the reported component must equal the
//     rebuilt component of u, the complement must match, and no active
//     edge may cross the cut (the partition evidence).
func verifyByRebuild(events []event, answers []answer) error {
	verts, _ := vertexIndex(events)
	count := make(map[edgeKey]int)
	qi := 0
	for i, ev := range events {
		switch ev.op {
		case opAdd:
			count[makeEdge(ev.u, ev.v)]++
		case opDel:
			k := makeEdge(ev.u, ev.v)
			if count[k] == 0 {
				return fmt.Errorf("event #%d (tick %d): delete of non-existent edge %d-%d", i, ev.tick, ev.u, ev.v)
			}
			count[k]--
		case opQuery:
			if qi >= len(answers) {
				return fmt.Errorf("event #%d (tick %d): missing answer for query %d-%d", i, ev.tick, ev.u, ev.v)
			}
			a := answers[qi]
			qi++
			if a.tick != ev.tick || a.u != ev.u || a.v != ev.v {
				return fmt.Errorf("event #%d: answer metadata mismatch: got tick %d query %d-%d", i, a.tick, a.u, a.v)
			}
			var active []edgeKey
			for k, c := range count {
				if c > 0 {
					active = append(active, k)
				}
			}
			if err := checkAnswer(active, verts, a); err != nil {
				return fmt.Errorf("event #%d (tick %d, query %d-%d): %w", i, ev.tick, ev.u, ev.v, err)
			}
		}
	}
	if qi != len(answers) {
		return fmt.Errorf("%d answers produced but only %d queries seen", len(answers), qi)
	}
	return nil
}

func checkAnswer(active []edgeKey, verts []int, a answer) error {
	adj := bfsAdj(active)
	if a.connected {
		if len(a.path) == 0 {
			return fmt.Errorf("claimed connected but path is empty")
		}
		if a.path[0] != a.u || a.path[len(a.path)-1] != a.v {
			return fmt.Errorf("path %v does not connect %d and %d", a.path, a.u, a.v)
		}
		for j := 0; j+1 < len(a.path); j++ {
			if !hasNeighbor(adj, a.path[j], a.path[j+1]) {
				return fmt.Errorf("path %v uses non-active edge %d-%d at this tick", a.path, a.path[j], a.path[j+1])
			}
		}
		if bfsPath(active, a.u, a.v) == nil {
			return fmt.Errorf("claimed connected but rebuilt graph says disconnected")
		}
		return nil
	}
	// Disconnected: component must match the rebuilt one exactly.
	want := bfsComponent(active, a.u)
	if len(a.comp) != len(want) {
		return fmt.Errorf("component %v does not match rebuilt component (size %d)", a.comp, len(want))
	}
	for _, v := range a.comp {
		if !want[v] {
			return fmt.Errorf("component %v does not match rebuilt component", a.comp)
		}
	}
	if want[a.v] {
		return fmt.Errorf("claimed disconnected but %d is reachable from %d", a.v, a.u)
	}
	// Partition evidence: comp + rest must partition all vertices, and
	// zero active edges may cross the cut.
	inComp := make(map[int]bool, len(a.comp))
	for _, v := range a.comp {
		inComp[v] = true
	}
	rest := a.rest
	sort.Ints(rest)
	wantRest := make([]int, 0, len(verts)-len(a.comp))
	for _, v := range verts {
		if !inComp[v] {
			wantRest = append(wantRest, v)
		}
	}
	if len(rest) != len(wantRest) {
		return fmt.Errorf("cut complement %v does not match vertices %v", rest, wantRest)
	}
	for j := range rest {
		if rest[j] != wantRest[j] {
			return fmt.Errorf("cut complement %v does not match vertices %v", rest, wantRest)
		}
	}
	for _, e := range active {
		if inComp[e.u] != inComp[e.v] {
			return fmt.Errorf("active edge %d-%d crosses the claimed cut", e.u, e.v)
		}
	}
	return nil
}

func hasNeighbor(adj map[int][]int, x, y int) bool {
	for _, n := range adj[x] {
		if n == y {
			return true
		}
	}
	return false
}
