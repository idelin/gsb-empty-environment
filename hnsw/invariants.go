package hnsw

import (
	"fmt"
	"sort"
	"strings"
)

// InvariantError aggregates every graph invariant violation detected by
// CheckInvariants.
type InvariantError struct {
	Violations []string
}

func (e *InvariantError) Error() string {
	return "hnsw: graph invariant violations:\n  - " + strings.Join(e.Violations, "\n  - ")
}

// CheckInvariants validates the structural contract of the graph:
//
//   - no edge points at a tombstoned node (no dangling edges);
//   - every edge is stored symmetrically in both adjacency lists;
//   - adjacency lists are sorted, duplicate-free and within the per-layer
//     degree bound (M0=2M on layer 0, M elsewhere);
//   - the entry point, when any alive node exists, is alive and sits on the
//     highest surviving layer (smallest ID on ties);
//   - every adjacency entry references an existing node spanning the layer.
//
// A nil result means the graph is valid.
func (g *Graph[T]) CheckInvariants() error {
	var violations []string
	aliveCount := 0
	maxAliveLevel := -1
	for _, ns := range g.nodes {
		if !ns.deleted {
			aliveCount++
			if ns.level > maxAliveLevel {
				maxAliveLevel = ns.level
			}
		}
	}

	if aliveCount == 0 {
		if g.entry != -1 {
			violations = append(violations, fmt.Sprintf("no alive nodes but entry point is %d", g.entry))
		}
	} else {
		entryNS, ok := g.nodes[g.entry]
		if !ok {
			violations = append(violations, fmt.Sprintf("entry point %d does not exist", g.entry))
		} else if entryNS.deleted {
			violations = append(violations, fmt.Sprintf("entry point %d is deleted", g.entry))
		} else if entryNS.level != maxAliveLevel {
			violations = append(violations, fmt.Sprintf(
				"entry point %d lives on layer %d but highest surviving layer is %d",
				g.entry, entryNS.level, maxAliveLevel))
		} else {
			expected := -1
			for id, ns := range g.nodes {
				if ns.deleted || ns.level != maxAliveLevel {
					continue
				}
				if expected == -1 || id < expected {
					expected = id
				}
			}
			if expected != -1 && g.entry != expected {
				violations = append(violations, fmt.Sprintf(
					"entry point %d should be the smallest top-layer id %d", g.entry, expected))
			}
		}
	}

	ids := make([]int, 0, len(g.nodes))
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	for _, id := range ids {
		ns := g.nodes[id]
		if ns.deleted {
			if len(ns.conns) != 0 {
				violations = append(violations, fmt.Sprintf("deleted node %d still stores %d adjacency layers", id, len(ns.conns)))
			}
			continue
		}
		if len(ns.conns) != ns.level+1 {
			violations = append(violations, fmt.Sprintf(
				"node %d has level %d but %d adjacency layers", id, ns.level, len(ns.conns)))
			continue
		}
		for layer := 0; layer <= ns.level; layer++ {
			conns := ns.conns[layer]
			bound := g.MaxDegree(layer)
			if len(conns) > bound {
				violations = append(violations, fmt.Sprintf(
					"node %d layer %d degree %d exceeds bound %d", id, layer, len(conns), bound))
			}
			for pos, neighborID := range conns {
				if pos > 0 && conns[pos-1] >= neighborID {
					violations = append(violations, fmt.Sprintf(
						"node %d layer %d adjacency list is not strictly sorted", id, layer))
					break
				}
				neighbor, ok := g.nodes[neighborID]
				if !ok {
					violations = append(violations, fmt.Sprintf(
						"edge %d->%d on layer %d targets a missing node", id, neighborID, layer))
					continue
				}
				if neighbor.deleted {
					violations = append(violations, fmt.Sprintf(
						"dangling edge %d->%d on layer %d targets a deleted node", id, neighborID, layer))
					continue
				}
				if neighbor.level < layer {
					violations = append(violations, fmt.Sprintf(
						"edge %d->%d on layer %d exceeds neighbor level %d", id, neighborID, layer, neighbor.level))
					continue
				}
				if !containsID(neighbor.conns[layer], id) {
					violations = append(violations, fmt.Sprintf(
						"asymmetric edge: %d lists %d on layer %d but not vice versa", id, neighborID, layer))
				}
			}
		}
	}

	if len(violations) == 0 {
		return nil
	}
	return &InvariantError{Violations: violations}
}
