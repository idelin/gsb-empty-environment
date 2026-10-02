// Package shard implements hierarchical virtual sharding with
// locality-aware routing for very large topologies.
//
// The key space is split into a fixed number of virtual shards. Each
// shard is placed on `replicas` nodes using rendezvous (highest-random-
// weight) hashing, which keeps placement deterministic and minimizes
// shard movement when nodes join or leave. Placement is topology-aware:
// replicas are spread across zones first, then racks, then nodes, so a
// single zone or rack failure never takes out a full replica set.
//
// Routing is locality-aware: given a client location (zone/rack/node),
// RouteLocal picks the replica with the smallest network distance,
// which drastically reduces cross-node and cross-zone traffic.
package shard

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
)

// ErrNoNodes is returned when routing is requested on an empty router.
var ErrNoNodes = errors.New("shard: no nodes available")

// NodeInfo describes a node and its position in the hierarchy.
type NodeInfo struct {
	ID   int
	Zone string
	Rack string
}

// Distance returns the network distance between two locations:
// 0 = same node, 1 = same rack, 2 = same zone, 3 = different zone.
func Distance(a, b NodeInfo) int {
	switch {
	case a.ID == b.ID:
		return 0
	case a.Zone == b.Zone && a.Rack == b.Rack:
		return 1
	case a.Zone == b.Zone:
		return 2
	default:
		return 3
	}
}

// Router maps keys to virtual shards and shards to replica sets, and
// answers locality-aware routing queries. Safe for concurrent use.
type Router struct {
	mu        sync.RWMutex
	numShards int
	replicas  int
	nodes     map[int]NodeInfo
	shardMap  [][]int // shard ID -> replica node IDs, primary first
	version   uint64
}

// NewRouter creates a router. numShards and replicas are clamped to >= 1.
func NewRouter(numShards, replicas int) *Router {
	if numShards < 1 {
		numShards = 1
	}
	if replicas < 1 {
		replicas = 1
	}
	return &Router{
		numShards: numShards,
		replicas:  replicas,
		nodes:     make(map[int]NodeInfo),
	}
}

// AddNode adds (or replaces) a node and rebalances the shard map.
func (r *Router) AddNode(n NodeInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
	r.rebuildLocked()
}

// RemoveNode removes a node and rebalances the shard map.
func (r *Router) RemoveNode(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
	r.rebuildLocked()
}

// NumShards returns the number of virtual shards.
func (r *Router) NumShards() int { return r.numShards }

// Version returns the placement generation, bumped on every rebalance.
func (r *Router) Version() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// ShardOf maps a key to its virtual shard ID.
func (r *Router) ShardOf(key string) int {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum64() % uint64(r.numShards))
}

// Replicas returns the replica set of a shard, primary first.
func (r *Router) Replicas(shard int) ([]NodeInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if shard < 0 || shard >= r.numShards {
		return nil, fmt.Errorf("shard: shard id %d out of range", shard)
	}
	if shard >= len(r.shardMap) {
		return nil, ErrNoNodes
	}
	ids := r.shardMap[shard]
	out := make([]NodeInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.nodes[id])
	}
	if len(out) == 0 {
		return nil, ErrNoNodes
	}
	return out, nil
}

// Route returns the shard and full replica set for a key.
func (r *Router) Route(key string) (int, []NodeInfo, error) {
	shard := r.ShardOf(key)
	replicas, err := r.Replicas(shard)
	return shard, replicas, err
}

// RouteLocal picks the replica for key that is closest to the client,
// minimizing cross-node/cross-zone traffic. Ties are broken by replica
// order (primary first), so the result is deterministic.
func (r *Router) RouteLocal(key string, client NodeInfo) (NodeInfo, error) {
	_, replicas, err := r.Route(key)
	if err != nil {
		return NodeInfo{}, err
	}
	best := replicas[0]
	bestDist := Distance(client, best)
	for _, cand := range replicas[1:] {
		if d := Distance(client, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best, nil
}

// Distribution returns how many shard replicas each node holds.
func (r *Router) Distribution() map[int]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[int]int, len(r.nodes))
	for id := range r.nodes {
		out[id] = 0
	}
	for _, ids := range r.shardMap {
		for _, id := range ids {
			out[id]++
		}
	}
	return out
}

// Snapshot returns a copy of the current shard map (node IDs per shard).
func (r *Router) Snapshot() [][]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([][]int, len(r.shardMap))
	for i, ids := range r.shardMap {
		cp := make([]int, len(ids))
		copy(cp, ids)
		out[i] = cp
	}
	return out
}

// rebuildLocked recomputes the full placement. Callers must hold r.mu.
func (r *Router) rebuildLocked() {
	ids := make([]int, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	r.shardMap = make([][]int, r.numShards)
	for s := 0; s < r.numShards; s++ {
		r.shardMap[s] = r.place(s, ids)
	}
	r.version++
}

// place selects up to `replicas` nodes for a shard using rendezvous
// hashing, greedily preferring unused zones, then unused racks.
func (r *Router) place(shard int, ids []int) []int {
	if len(ids) == 0 {
		return nil
	}
	type cand struct {
		id    int
		score uint64
	}
	scored := make([]cand, 0, len(ids))
	for _, id := range ids {
		scored = append(scored, cand{id: id, score: r.score(shard, id)})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].id < scored[j].id
	})

	k := r.replicas
	if k > len(ids) {
		k = len(ids)
	}
	picked := make([]int, 0, k)
	inSet := make(map[int]bool, k)
	zoneCount := make(map[string]int)
	rackUsed := make(map[string]bool)

	// Greedily fill one slot at a time, always preferring the candidate
	// with the fewest already-picked replicas in its zone, then an unused
	// rack, then the highest rendezvous score. This keeps replicas spread
	// across zones and racks while staying deterministic and minimally
	// disruptive under membership changes.
	for len(picked) < k {
		best := -1
		for i := range scored {
			c := scored[i]
			if inSet[c.id] {
				continue
			}
			if best == -1 {
				best = i
				continue
			}
			b := scored[best]
			nc, nb := r.nodes[c.id], r.nodes[b.id]
			zc, zb := zoneCount[nc.Zone], zoneCount[nb.Zone]
			if zc != zb {
				if zc < zb {
					best = i
				}
				continue
			}
			rc := rackUsed[nc.Zone+"/"+nc.Rack]
			rb := rackUsed[nb.Zone+"/"+nb.Rack]
			if rc != rb {
				if !rc {
					best = i
				}
				continue
			}
			// scored is sorted by descending score, so the earlier
			// candidate wins ties; nothing to do.
		}
		chosen := scored[best]
		n := r.nodes[chosen.id]
		picked = append(picked, chosen.id)
		inSet[chosen.id] = true
		zoneCount[n.Zone]++
		rackUsed[n.Zone+"/"+n.Rack] = true
	}
	return picked
}

// score is the rendezvous hash of (shard, node): the pair is mixed with
// the splitmix64 finalizer, which gives an unbiased, deterministic
// distribution (plain FNV-1a over the raw pair is measurably skewed).
func (r *Router) score(shard, nodeID int) uint64 {
	x := uint64(shard)*0x9E3779B97F4A7C15 ^ uint64(uint32(nodeID))*0xC2B2AE3D27D4EB4F
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return x
}
