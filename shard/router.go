// Package shard implements a hierarchical, locality-aware routing engine.
// Keys map to virtual shards; virtual shards map to replica sets of nodes
// via rendezvous (HRW) hashing; a router picks the replica closest to the
// client's zone to minimize cross-node and cross-zone traffic.
package shard

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
)

// NodeID uniquely identifies a storage node.
type NodeID string

// ZoneID identifies a fault/locality domain (rack, AZ, region).
type ZoneID string

// ShardID identifies a virtual shard.
type ShardID uint32

// ErrNoNodes is returned by Route when the topology is empty.
var ErrNoNodes = errors.New("shard: no nodes in topology")

// Node is a storage node placed in a zone. Weight scales the number of
// virtual points the node contributes during shard assignment; values
// below 1 are normalized to 1.
type Node struct {
	ID     NodeID
	Zone   ZoneID
	Weight int
}

func (n *Node) weight() int {
	if n.Weight < 1 {
		return 1
	}
	return n.Weight
}

// Router maps keys to nodes through virtual shards. It is safe for
// concurrent use and spawns no background goroutines.
type Router struct {
	mu        sync.RWMutex
	numShards uint32
	replicas  int
	nodes     map[NodeID]*Node
	shardMap  map[ShardID][]NodeID
}

// NewRouter builds a router with numShards virtual shards and the given
// replication factor. It panics on invalid configuration.
func NewRouter(numShards uint32, replicas int) *Router {
	if numShards == 0 {
		panic("shard: numShards must be positive")
	}
	if replicas < 1 {
		panic("shard: replicas must be positive")
	}
	return &Router{
		numShards: numShards,
		replicas:  replicas,
		nodes:     make(map[NodeID]*Node),
		shardMap:  make(map[ShardID][]NodeID),
	}
}

// NumShards returns the number of virtual shards.
func (r *Router) NumShards() uint32 { return r.numShards }

// AddNode inserts or replaces a node and rebalances shard assignments.
func (r *Router) AddNode(n *Node) {
	if n == nil || n.ID == "" {
		panic("shard: node must have a non-empty ID")
	}
	cp := *n
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[cp.ID] = &cp
	r.rebuildLocked()
}

// RemoveNode deletes a node (no-op if absent) and rebalances.
func (r *Router) RemoveNode(id NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return
	}
	delete(r.nodes, id)
	r.rebuildLocked()
}

// NumNodes reports the current topology size.
func (r *Router) NumNodes() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// ShardOf returns the virtual shard responsible for key.
func (r *Router) ShardOf(key string) ShardID {
	h := fnv.New32a()
	h.Write([]byte(key))
	return ShardID(h.Sum32() % r.numShards)
}

// Replicas returns the ordered replica set of a shard; the first entry is
// the primary. Returns nil for unknown shards.
func (r *Router) Replicas(s ShardID) []NodeID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	reps, ok := r.shardMap[s]
	if !ok {
		return nil
	}
	return append([]NodeID(nil), reps...)
}

// Route resolves key to the node a client in clientZone should talk to.
// A replica inside clientZone is preferred; otherwise the request falls
// back to a replica chosen deterministically from the full replica set.
func (r *Router) Route(key string, clientZone ZoneID) (NodeID, ShardID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.nodes) == 0 {
		return "", 0, ErrNoNodes
	}
	s := r.ShardOf(key)
	reps := r.shardMap[s]
	if len(reps) == 0 {
		return "", 0, fmt.Errorf("shard: no replicas for shard %d", s)
	}
	var local []NodeID
	for _, id := range reps {
		if r.nodes[id].Zone == clientZone {
			local = append(local, id)
		}
	}
	if len(local) > 0 {
		return local[spread(key)%len(local)], s, nil
	}
	return reps[spread(key)%len(reps)], s, nil
}

// Distribution counts how many shards have their primary on each node.
func (r *Router) Distribution() map[NodeID]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	dist := make(map[NodeID]int, len(r.nodes))
	for _, reps := range r.shardMap {
		if len(reps) > 0 {
			dist[reps[0]]++
		}
	}
	return dist
}

func (r *Router) rebuildLocked() {
	r.shardMap = make(map[ShardID][]NodeID, r.numShards)
	for s := uint32(0); s < r.numShards; s++ {
		r.shardMap[ShardID(s)] = r.replicasForLocked(s)
	}
}

// replicasForLocked picks the top-replicas nodes for a shard using
// rendezvous (HRW) hashing over each node's virtual points.
func (r *Router) replicasForLocked(s uint32) []NodeID {
	type candidate struct {
		id    NodeID
		score uint64
	}
	best := make(map[NodeID]uint64, len(r.nodes))
	for id, n := range r.nodes {
		for i := 0; i < n.weight(); i++ {
			h := hashShardNode(s, id, i)
			if cur, ok := best[id]; !ok || h > cur {
				best[id] = h
			}
		}
	}
	cands := make([]candidate, 0, len(best))
	for id, score := range best {
		cands = append(cands, candidate{id, score})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].id < cands[j].id
	})
	k := r.replicas
	if k > len(cands) {
		k = len(cands)
	}
	out := make([]NodeID, k)
	for i := 0; i < k; i++ {
		out[i] = cands[i].id
	}
	return out
}

func hashShardNode(s uint32, id NodeID, vnode int) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	buf[0] = byte(s)
	buf[1] = byte(s >> 8)
	buf[2] = byte(s >> 16)
	buf[3] = byte(s >> 24)
	buf[4] = byte(vnode)
	buf[5] = byte(vnode >> 8)
	buf[6] = byte(vnode >> 16)
	buf[7] = byte(vnode >> 24)
	h.Write(buf[:])
	h.Write([]byte(id))
	return mix64(h.Sum64())
}

// mix64 is a splitmix64-style finalizer; FNV-1a alone has a weak avalanche
// for keys sharing long prefixes (such as node IDs), which skews shard
// assignment. Mixing restores near-uniform rendezvous scores.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func spread(key string) int {
	h := fnv.New32a()
	h.Write([]byte("route\x00"))
	h.Write([]byte(key))
	return int(h.Sum32() & 0x7fffffff)
}
