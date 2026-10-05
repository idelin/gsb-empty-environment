// Package allocator implements weighted shard placement with exact integer
// per-node quotas and minimum-migration reassignment.
//
// The allocator works in two phases:
//
//  1. Quotas: each node's ideal share shards*weight/totalWeight is rounded
//     down, and the leftover shards are handed out by largest fractional
//     remainder (Hamilton / largest-remainder method). Ties on the remainder
//     are broken by smaller node ID, so quotas are exact (they sum to the
//     shard count) and fully deterministic.
//
//  2. Placement: every node keeps as many of its current shards as its
//     quota allows (lowest shard IDs first), surplus shards are released,
//     and released shards (lowest shard ID first) fill deficit nodes in
//     ascending node ID order.
//
// The number of migrations is provably minimal: node i ends with quota[i]
// shards and at most oldCount[i] of them can be shards it already held, so
// at least max(0, quota[i]-oldCount[i]) shards must newly arrive at i, and
// every arriving shard is an owner change. Summed over all nodes this gives
// the lower bound, and the placement above attains it exactly. Plan.Proof
// carries this evidence for every run.
package allocator

import (
	"fmt"
	"sort"
	"strings"
)

// Unassigned marks a shard with no current owner in the old assignment.
const Unassigned = -1

// Node is a placement target. Weight is its relative share of the shards;
// a zero weight means the node must hold no shards.
type Node struct {
	ID     int
	Weight int64
}

// Plan is the outcome of a Reassign call.
type Plan struct {
	// Assignment[s] is the ID of the node owning shard s.
	Assignment []int
	// Quotas[id] is the exact number of shards node id must hold.
	Quotas map[int]int
	// Migrations is the number of shards whose owner changed relative to
	// the old assignment (a shard going from Unassigned to a node counts).
	Migrations int
	// LowerBound is the provable minimum number of migrations for these
	// quotas; Migrations == LowerBound always holds.
	LowerBound int
	// Proof is the human-readable lower-bound evidence.
	Proof string
}

// Quotas computes exact integer quotas proportional to node weights via the
// largest-remainder method, breaking remainder ties by smaller node ID.
// The quotas always sum to shards. If every weight is zero, shards are
// spread as evenly as possible in ascending node ID order.
func Quotas(nodes []Node, shards int) map[int]int {
	ids := sortedIDs(nodes)
	weights := make(map[int]int64, len(ids))
	var total int64
	for _, n := range nodes {
		w := n.Weight
		if w < 0 {
			w = 0
		}
		weights[n.ID] = w
	}
	for _, id := range ids {
		total += weights[id]
	}
	if total == 0 {
		for _, id := range ids {
			weights[id] = 1
		}
		total = int64(len(ids))
	}

	quota := make(map[int]int, len(ids))
	type remainder struct {
		id  int
		rem int64
	}
	rems := make([]remainder, 0, len(ids))
	assigned := 0
	for _, id := range ids {
		exact := int64(shards) * weights[id]
		q := exact / total
		quota[id] = int(q)
		assigned += int(q)
		rems = append(rems, remainder{id: id, rem: exact % total})
	}
	// Largest remainder first; equal remainders go to the smaller node ID.
	sort.Slice(rems, func(i, j int) bool {
		if rems[i].rem != rems[j].rem {
			return rems[i].rem > rems[j].rem
		}
		return rems[i].id < rems[j].id
	})
	for i := 0; i < shards-assigned; i++ {
		quota[rems[i].id]++
	}
	return quota
}

// Reassign places shards onto nodes so that every node holds exactly its
// weight-proportional quota and the number of owner changes relative to old
// is minimal. old[s] is the current owner of shard s, or Unassigned.
// Shards owned by nodes that are no longer present are released and
// reallocated. The result is deterministic: all ties are resolved by
// ascending node ID and ascending shard ID.
func Reassign(shards int, nodes []Node, old []int) Plan {
	if len(old) != shards {
		panic(fmt.Sprintf("allocator: old assignment has %d entries, want %d", len(old), shards))
	}
	quota := Quotas(nodes, shards)
	ids := sortedIDs(nodes)
	present := make(map[int]bool, len(ids))
	for _, id := range ids {
		present[id] = true
	}

	// Shards currently held by each live node.
	oldCount := make(map[int]int, len(ids))
	for s := 0; s < shards; s++ {
		if present[old[s]] {
			oldCount[old[s]]++
		}
	}

	// Keep as many current shards per node as the quota allows, preferring
	// the lowest shard IDs; release everything else.
	assignment := make([]int, shards)
	kept := make(map[int]int, len(ids))
	released := make([]int, 0, shards)
	for s := 0; s < shards; s++ {
		owner := old[s]
		if present[owner] && kept[owner] < quota[owner] {
			assignment[s] = owner
			kept[owner]++
		} else {
			assignment[s] = Unassigned
			released = append(released, s)
		}
	}

	// Fill deficit nodes in ascending node ID order with released shards in
	// ascending shard ID order.
	cursor := 0
	for _, id := range ids {
		for need := quota[id] - kept[id]; need > 0; need-- {
			assignment[released[cursor]] = id
			cursor++
		}
	}

	migrations := 0
	for s := 0; s < shards; s++ {
		if assignment[s] != old[s] {
			migrations++
		}
	}

	// Lower-bound evidence: node i must receive at least
	// max(0, quota[i]-oldCount[i]) shards it does not currently hold, and
	// each such shard is an owner change. The plan attains the sum exactly.
	lowerBound := 0
	var lines []string
	for _, id := range ids {
		if deficit := quota[id] - oldCount[id]; deficit > 0 {
			lowerBound += deficit
			lines = append(lines, fmt.Sprintf(
				"node %d: quota %d, holds %d -> at least %d shard(s) must move in",
				id, quota[id], oldCount[id], deficit))
		}
	}
	proof := fmt.Sprintf(
		"each shard moving into a node changes owner, so migrations >= sum of per-node deficits = %d; this plan migrates exactly %d shard(s), matching the bound",
		lowerBound, migrations)
	if len(lines) > 0 {
		proof = strings.Join(lines, "\n") + "\n" + proof
	}

	return Plan{
		Assignment: assignment,
		Quotas:     quota,
		Migrations: migrations,
		LowerBound: lowerBound,
		Proof:      proof,
	}
}

// sortedIDs returns the deduplicated node IDs in ascending order.
func sortedIDs(nodes []Node) []int {
	seen := make(map[int]bool, len(nodes))
	ids := make([]int, 0, len(nodes))
	for _, n := range nodes {
		if !seen[n.ID] {
			seen[n.ID] = true
			ids = append(ids, n.ID)
		}
	}
	sort.Ints(ids)
	return ids
}
