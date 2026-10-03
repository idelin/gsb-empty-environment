// Package router implements hierarchical sharding with virtual shards and
// locality-aware routing. Keys map to a fixed number of virtual shards;
// each shard is assigned to a replica set of physical nodes via rendezvous
// (highest-random-weight) hashing, which keeps shard movement minimal when
// nodes join or leave. Routing picks the replica with the best locality
// match (same zone, then same region) and breaks ties by live load, which
// keeps cross-node and cross-zone traffic low.
package router

import (
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

// Locality is a node's or client's position in the network hierarchy.
type Locality struct {
	Region string
	Zone   string
}

// NodeID identifies a physical node.
type NodeID string

// Node is a physical shard server.
type Node struct {
	ID       NodeID
	Locality Locality
	load     int64
}

// Load returns the number of in-flight routed requests on the node.
func (n *Node) Load() int64 { return atomic.LoadInt64(&n.load) }

var (
	ErrNoNodes        = errors.New("router: no nodes available")
	ErrNodeExists     = errors.New("router: node already exists")
	ErrNodeNotFound   = errors.New("router: node not found")
	ErrBadShardCount  = errors.New("router: shard count must be positive")
	ErrBadReplication = errors.New("router: replication factor must be positive")
	ErrShardRange     = errors.New("router: shard id out of range")
)

// Router maps keys to virtual shards and shards to node replica sets.
// It is safe for concurrent use.
type Router struct {
	mu          sync.RWMutex
	shardCount  int
	replication int
	nodes       map[NodeID]*Node
	shards      [][]NodeID // shard -> ranked replica set
}

// NewRouter creates a router with the given number of virtual shards and
// replica-set size per shard.
func NewRouter(shardCount, replication int) (*Router, error) {
	if shardCount <= 0 {
		return nil, ErrBadShardCount
	}
	if replication <= 0 {
		return nil, ErrBadReplication
	}
	return &Router{
		shardCount:  shardCount,
		replication: replication,
		nodes:       make(map[NodeID]*Node),
		shards:      make([][]NodeID, shardCount),
	}, nil
}

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		_, _ = h.Write([]byte(p))
		_, _ = h.Write([]byte{0})
	}
	return fmix64(h.Sum64())
}

// fmix64 is the murmur3 finalizer. FNV-1a has a weak avalanche for inputs
// that differ only in their last bytes (e.g. "node-08" vs "node-12"),
// which would badly bias rendezvous ranking; the finalizer restores a
// uniform distribution.
func fmix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}

// ShardCount returns the number of virtual shards.
func (r *Router) ShardCount() int { return r.shardCount }

// NodeCount returns the number of live nodes.
func (r *Router) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}

// ShardOf deterministically maps a key to a virtual shard.
func (r *Router) ShardOf(key string) int {
	return int(hash64("shard", key) % uint64(r.shardCount))
}

// AddNode registers a node and recomputes shard assignments.
func (r *Router) AddNode(id NodeID, loc Locality) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; ok {
		return ErrNodeExists
	}
	r.nodes[id] = &Node{ID: id, Locality: loc}
	r.recomputeLocked()
	return nil
}

// RemoveNode deregisters a node and recomputes shard assignments.
func (r *Router) RemoveNode(id NodeID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return ErrNodeNotFound
	}
	delete(r.nodes, id)
	r.recomputeLocked()
	return nil
}

// recomputeLocked re-ranks every shard's replica set with rendezvous
// hashing. Only shards affected by a membership change move.
func (r *Router) recomputeLocked() {
	ids := make([]NodeID, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for s := 0; s < r.shardCount; s++ {
		shard := strconv.Itoa(s)
		ranked := append([]NodeID(nil), ids...)
		sort.Slice(ranked, func(i, j int) bool {
			hi := hash64("assign", shard, string(ranked[i]))
			hj := hash64("assign", shard, string(ranked[j]))
			if hi != hj {
				return hi > hj
			}
			return ranked[i] < ranked[j]
		})
		k := r.replication
		if k > len(ranked) {
			k = len(ranked)
		}
		r.shards[s] = append([]NodeID(nil), ranked[:k]...)
	}
}

// Replicas returns the ranked replica set of a shard.
func (r *Router) Replicas(shard int) ([]NodeID, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if shard < 0 || shard >= r.shardCount {
		return nil, ErrShardRange
	}
	if len(r.shards[shard]) == 0 {
		return nil, ErrNoNodes
	}
	return append([]NodeID(nil), r.shards[shard]...), nil
}

func localityScore(node, from Locality) int {
	if from.Region == "" && from.Zone == "" {
		return 0
	}
	if node.Region == from.Region {
		if from.Zone != "" && node.Zone == from.Zone {
			return 2
		}
		return 1
	}
	return 0
}

// Route picks the best node for a key: the shard's replica with the best
// locality match to `from`, breaking ties by lowest live load and then by
// node id for determinism. The chosen node's load is incremented; callers
// should call Done when the request completes.
func (r *Router) Route(key string, from Locality) (NodeID, int, error) {
	shard := r.ShardOf(key)

	r.mu.RLock()
	defer r.mu.RUnlock()
	reps := r.shards[shard]
	if len(reps) == 0 {
		return "", shard, ErrNoNodes
	}

	best := NodeID("")
	bestScore := -1
	var bestLoad int64
	for _, id := range reps {
		n := r.nodes[id]
		score := localityScore(n.Locality, from)
		load := n.Load()
		if best == "" ||
			score > bestScore ||
			(score == bestScore && load < bestLoad) ||
			(score == bestScore && load == bestLoad && id < best) {
			best, bestScore, bestLoad = id, score, load
		}
	}
	atomic.AddInt64(&r.nodes[best].load, 1)
	return best, shard, nil
}

// Done marks a routed request on the node as completed.
func (r *Router) Done(id NodeID) {
	r.mu.RLock()
	n, ok := r.nodes[id]
	r.mu.RUnlock()
	if ok {
		atomic.AddInt64(&n.load, -1)
	}
}

// Load returns the live load of a node.
func (r *Router) Load(id NodeID) (int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	if !ok {
		return 0, ErrNodeNotFound
	}
	return n.Load(), nil
}

// LocalityOf returns the locality of a live node.
func (r *Router) LocalityOf(id NodeID) (Locality, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.nodes[id]
	if !ok {
		return Locality{}, ErrNodeNotFound
	}
	return n.Locality, nil
}

// Nodes returns a sorted snapshot of live node ids.
func (r *Router) Nodes() []NodeID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]NodeID, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
