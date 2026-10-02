package main

import "bytes"

const (
	oldTombstonesPerNode = 22 // deleted before T=50, GC targets
	liveKeysPerNode      = 8  // live entries, must survive GC
	lateTombstones       = 2  // deleted after T=50, must survive GC
	gcThreshold          = 50
)

// buildCluster creates a 5-node cluster (A..E), drives a workload in
// which each node deletes 22 large objects before T=50, then writes live
// data and a couple of late tombstones past T=50, and finally gossips
// until all clocks and entries fully converge.
func buildCluster() []*Node {
	ids := []string{"A", "B", "C", "D", "E"}
	nodes := make([]*Node, 0, len(ids))
	for _, id := range ids {
		nodes = append(nodes, NewNode(id))
	}

	bigValue := bytes.Repeat([]byte("x"), 256)

	// Phase 1: old objects, inserted and deleted at T <= 44 (< 50).
	for _, n := range nodes {
		for i := 0; i < oldTombstonesPerNode; i++ {
			key := keyOf("old", n.ID, i)
			n.Store.Put(key, bigValue)
			n.Store.Delete(key)
		}
	}

	// Phase 2: live keys plus late tombstones at T > 50.
	for _, n := range nodes {
		for i := 0; i < liveKeysPerNode; i++ {
			n.Store.Put(keyOf("live", n.ID, i), []byte("payload"))
		}
		for i := 0; i < lateTombstones; i++ {
			key := keyOf("late", n.ID, i)
			n.Store.Put(key, []byte("payload"))
			n.Store.Delete(key)
		}
	}

	gossipUntilConverged(nodes)
	return nodes
}

func keyOf(prefix, nodeID string, i int) string {
	return prefix + "-" + nodeID + "-" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

// gossipUntilConverged runs all-pairs gossip rounds until every node has
// identical entries and vector clocks.
func gossipUntilConverged(nodes []*Node) {
	for round := 0; round < 10; round++ {
		for i := 0; i < len(nodes); i++ {
			for j := i + 1; j < len(nodes); j++ {
				nodes[i].Gossip(nodes[j])
			}
		}
		if converged(nodes) {
			return
		}
	}
}

func converged(nodes []*Node) bool {
	ref := nodes[0].Store.Clock()
	refEntries := len(nodes[0].Store.Entries())
	for _, n := range nodes[1:] {
		c := n.Store.Clock()
		if len(c) != len(ref) || len(n.Store.Entries()) != refEntries {
			return false
		}
		for k, v := range ref {
			if c[k] != v {
				return false
			}
		}
	}
	// Every node's knowledge of every peer's clock must also be current,
	// otherwise local frontier computation would use stale partial clocks.
	for _, n := range nodes {
		for _, m := range nodes {
			known, ok := n.known[m.ID]
			if !ok {
				return false
			}
			mc := m.Store.Clock()
			if len(known) != len(mc) {
				return false
			}
			for k, v := range mc {
				if known[k] != v {
					return false
				}
			}
		}
	}
	return true
}
