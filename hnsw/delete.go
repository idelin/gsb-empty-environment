package hnsw

import (
	"fmt"
	"sort"
)

// DeleteReport summarizes a Delete batch.
type DeleteReport struct {
	// Deleted are the IDs that transitioned from alive to tombstoned.
	Deleted []int
	// Skipped are requested IDs that already carried a tombstone.
	Skipped []int
	// Repaired are alive nodes whose neighborhoods were reconstructed on at
	// least one layer.
	Repaired []int
}

// layerSnapshot captures the alive adjacency of every node on one layer
// before the batch mutates the graph, including links to nodes that the
// batch is about to tombstone.
type layerSnapshot map[int][]int

// Delete tombstones the requested nodes in a batch and reconstructs the
// affected neighborhoods.
//
// For every layer, each deleted node is first detached from its alive
// neighbors (both directions). Every surviving node that lost a neighbor is
// then repaired with HNSW Algorithm 4 over the candidate pool formed by the
// original deleted neighbors, their alive one-hop neighbors (captured before
// mutation) and the node's remaining alive neighbors. New edges are inserted
// symmetrically, and any endpoint exceeding the layer degree bound (M on
// upper layers, M0=2M on layer 0) is shrunk with the same heuristic.
//
// Finally the entry point is migrated, when needed, to the surviving node on
// the highest layer (smallest ID breaking ties). Deleting an unknown ID is an
// error and leaves the graph untouched; re-deleting an already tombstoned ID
// is idempotent and reported as skipped.
func (g *Graph[T]) Delete(ids []int) (DeleteReport, error) {
	report := DeleteReport{}
	if len(ids) == 0 {
		return report, nil
	}

	toDelete := make(map[int]struct{})
	for _, id := range ids {
		ns, ok := g.nodes[id]
		if !ok {
			return report, fmt.Errorf("hnsw: unknown node %d", id)
		}
		if ns.deleted {
			report.Skipped = append(report.Skipped, id)
			continue
		}
		toDelete[id] = struct{}{}
	}
	if len(toDelete) == 0 {
		sort.Ints(report.Skipped)
		return report, nil
	}

	maxLayer := 0
	for id := range toDelete {
		if l := g.nodes[id].level; l > maxLayer {
			maxLayer = l
		}
	}

	// Phase 1: snapshot all layer links before mutation so the candidate pool
	// still sees one-hop neighbors of nodes that get tombstoned.
	snapshots := make([]layerSnapshot, maxLayer+1)
	for layer := 0; layer <= maxLayer; layer++ {
		snapshots[layer] = layerSnapshot{}
		for id, ns := range g.nodes {
			if ns.deleted || layer > ns.level {
				continue
			}
			snapshots[layer][id] = append([]int(nil), ns.conns[layer]...)
		}
	}

	affectedSet := map[int]struct{}{}

	// Phase 2: detach deleted nodes from every surviving neighbor on every
	// layer they span, recording the survivors for repair.
	for layer := 0; layer <= maxLayer; layer++ {
		snap := snapshots[layer]
		for id := range toDelete {
			for _, neighborID := range snap[id] {
				if _, gone := toDelete[neighborID]; gone {
					continue
				}
				g.removeLink(layer, id, neighborID)
				affectedSet[neighborID] = struct{}{}
			}
		}
	}

	// Phase 3: tombstone the nodes and release their adjacency storage.
	for id := range toDelete {
		ns := g.nodes[id]
		ns.deleted = true
		ns.conns = nil
		report.Deleted = append(report.Deleted, id)
	}

	// Phase 4: rebuild the neighborhood of each affected survivor, layer by
	// layer. Candidate pools and processing orders are fully deterministic.
	affected := sortedKeys(affectedSet)
	repairedSet := map[int]struct{}{}
	for layer := 0; layer <= maxLayer; layer++ {
		snap := snapshots[layer]
		shrinkQueue := []int{}
		queued := map[int]struct{}{}

		for _, target := range affected {
			ns := g.nodes[target]
			if layer > ns.level {
				continue
			}

			candidates := map[int]struct{}{}
			// Original deleted neighbors (the task's anchor candidates) plus
			// the surviving one-hop neighborhood of those anchors, as seen
			// before the batch mutated links.
			for _, anchorID := range snap[target] {
				candidates[anchorID] = struct{}{}
				if anchorNeighbors, ok := snap[anchorID]; ok {
					for _, hopID := range anchorNeighbors {
						candidates[hopID] = struct{}{}
					}
				}
			}
			// Current surviving neighbors ensure existing valid edges are
			// part of the heuristic input instead of being dropped silently.
			for _, neighborID := range ns.conns[layer] {
				candidates[neighborID] = struct{}{}
				// Two-hop expansion through surviving neighbors: when an
				// entire neighborhood is tombstoned in one batch, the
				// anchor-based pool contains only tombstones and would
				// strand the target.
				for _, hopID := range g.nodes[neighborID].conns[layer] {
					candidates[hopID] = struct{}{}
				}
			}

			wanted := g.selectNeighbors(target, candidates, layer, g.MaxDegree(layer))
			if len(wanted) == 0 {
				// Fallback for a node whose whole local pool was
				// tombstoned: reconnect to the globally closest alive
				// nodes so the graph cannot split into islands.
				wanted = g.nearestAlive(target, layer, g.MaxDegree(layer))
			}
			wantedSet := intSet(wanted)

			for _, neighborID := range append([]int(nil), ns.conns[layer]...) {
				if _, keep := wantedSet[neighborID]; !keep {
					g.removeLink(layer, target, neighborID)
				}
			}
			for _, neighborID := range wanted {
				if !containsID(ns.conns[layer], neighborID) {
					g.addLink(layer, target, neighborID, &shrinkQueue, queued)
				}
			}
			repairedSet[target] = struct{}{}
		}

		if len(shrinkQueue) > 0 {
			g.repairLayerDegree(layer, shrinkQueue)
		}
	}

	// Phase 5: keep the entry point on the highest surviving layer before any
	// traversal depends on it.
	g.migrateEntry(toDelete)

	// Phase 6: guarantee layer-0 connectivity. Shrinking a reverse edge can
	// leave a low-degree node detached even though it never directly lost a
	// tombstoned neighbor; such nodes are not in the affected set and would
	// otherwise become silent islands.
	g.reconnectLayer0(repairedSet)

	report.Repaired = sortedKeys(repairedSet)
	sort.Ints(report.Deleted)
	sort.Ints(report.Skipped)
	return report, nil
}

// migrateEntry selects the alive node with the maximum level; the smallest ID
// wins ties. When no alive node remains the graph has no entry point.
func (g *Graph[T]) migrateEntry(deleted map[int]struct{}) {
	if g.entry != -1 {
		if ns, ok := g.nodes[g.entry]; ok && !ns.deleted {
			return
		}
	}
	best := -1
	bestLevel := -1
	ids := make([]int, 0, len(g.nodes))
	for id, ns := range g.nodes {
		if ns.deleted {
			continue
		}
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if l := g.nodes[id].level; l > bestLevel {
			best = id
			bestLevel = l
		}
	}
	g.entry = best
}
