// Package shard implements hierarchical virtual sharding with
// locality-aware routing for very large topologies.
//
// The key space is split into a fixed number of virtual shards (vshards).
// Each vshard is mapped to a small replica set of physical nodes with
// rendezvous (highest-random-weight) hashing, which gives deterministic
// placement and minimal shard movement when membership changes.
//
// Routing is locality-aware: given the client's position in the zone/rack
// hierarchy, the router picks the replica that minimizes cross-node
// traffic (same node > same rack > same zone > remote), breaking ties by
// live load. All hot paths are lock-free or read-locked and safe for
// concurrent use.
package shard

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"sync"
	"sync/atomic"
)

// ErrNoNodes is returned by Route when the cluster has no members.
var ErrNoNodes = errors.New("shard: no nodes in cluster")

// Locality is a position in the zone/rack/node hierarchy.
type Locality struct {
	Zone string
	Rack string
	Node string
}

// Node is a physical member of the cluster.
type Node struct {
	ID       string
	Locality Locality
	Weight   int // relative capacity; <=0 is treated as 1
}

func (n Node) weight() int {
	if n.Weight <= 0 {
		return 1
	}
	return n.Weight
}

// HopClass classifies how far a routed request travelled.
type HopClass int

const (
	HopSameNode HopClass = iota // client and target coincide
	HopSameRack                 // same rack, different node
	HopSameZone                 // same zone, different rack
	HopRemote                   // cross-zone traffic
)

func (h HopClass) String() string {
	switch h {
	case HopSameNode:
		return "same-node"
	case HopSameRack:
		return "same-rack"
	case HopSameZone:
		return "same-zone"
	default:
		return "remote"
	}
}

// localityDistance ranks how close a candidate node is to the client.
func localityDistance(client Locality, n Node) HopClass {
	if client.Node != "" && client.Node == n.ID {
		return HopSameNode
	}
	if client.Zone == n.Locality.Zone {
		if client.Rack != "" && client.Rack == n.Locality.Rack {
			return HopSameRack
		}
		return HopSameZone
	}
	return HopRemote
}

// RouteDecision describes where a key was routed.
type RouteDecision struct {
	Key       string
	Shard     uint32
	Node      Node
	Hops      HopClass
	Replicas  []string // node IDs of the full replica set, best first
}

// Stats is a snapshot of routing counters.
type Stats struct {
	Total    uint64
	SameNode uint64
	SameRack uint64
	SameZone uint64
	Remote   uint64
}

// CrossNode returns routed requests that left the client's node.
func (s Stats) CrossNode() uint64 { return s.SameRack + s.SameZone + s.Remote }

// CrossZone returns routed requests that left the client's zone.
func (s Stats) CrossZone() uint64 { return s.Remote }

// Router maps keys to nodes through virtual shards. The zero value is not
// usable; construct with NewRouter.
type Router struct {
	numShards uint32
	replicas  int

	mu     sync.RWMutex
	nodes  map[string]Node
	owners [][]string // shard -> replica node IDs, ordered by HRW score

	loads  sync.Map // nodeID -> *int64, in-flight-ish load counter
	stats  [4]uint64 // indexed by HopClass
	total  uint64
}

// NewRouter creates a Router with numShards virtual shards and the given
// replica-set size. numShards must be > 0; replicas is clamped to >= 1.
func NewRouter(numShards uint32, replicas int) *Router {
	if numShards == 0 {
		numShards = 1024
	}
	if replicas < 1 {
		replicas = 1
	}
	return &Router{
		numShards: numShards,
		replicas:  replicas,
		nodes:     make(map[string]Node),
	}
}

// NumShards returns the virtual shard count.
func (r *Router) NumShards() uint32 { return r.numShards }

// hashKey hashes a routing key to a shard id.
func (r *Router) hashKey(key string) uint32 {
	h := fnv.New64a()
	h.Write([]byte(key))
	return uint32(h.Sum64() % uint64(r.numShards))
}

// score computes the rendezvous hash of (shard, node).
func score(shard uint32, nodeID string) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	buf[0] = byte(shard)
	buf[1] = byte(shard >> 8)
	buf[2] = byte(shard >> 16)
	buf[3] = byte(shard >> 24)
	h.Write(buf[:])
	h.Write([]byte(nodeID))
	return h.Sum64()
}

// rebuild recomputes the full vshard -> replica-set table. Caller holds mu.
func (r *Router) rebuild() {
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	owners := make([][]string, r.numShards)
	for s := uint32(0); s < r.numShards; s++ {
		type cand struct {
			id    string
			score uint64
		}
		cands := make([]cand, 0, len(ids))
		for _, id := range ids {
			cands = append(cands, cand{id, score(s, id)})
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].score != cands[j].score {
				return cands[i].score > cands[j].score
			}
			return cands[i].id < cands[j].id
		})
		// Hierarchy-aware replica selection: walk candidates best-first but
		// prefer spreading replicas across distinct zones.
		k := r.replicas
		if k > len(cands) {
			k = len(cands)
		}
		picked := make([]string, 0, k)
		usedZone := make(map[string]bool)
		for _, c := range cands {
			if len(picked) == k {
				break
			}
			z := r.nodes[c.id].Locality.Zone
			if usedZone[z] {
				continue
			}
			usedZone[z] = true
			picked = append(picked, c.id)
		}
		for _, c := range cands { // fill remaining slots if zones ran out
			if len(picked) == k {
				break
			}
			dup := false
			for _, p := range picked {
				if p == c.id {
					dup = true
					break
				}
			}
			if !dup {
				picked = append(picked, c.id)
			}
		}
		owners[s] = picked
	}
	r.owners = owners
}

// AddNode registers or updates a node and rebuilds the shard table.
func (r *Router) AddNode(n Node) error {
	if n.ID == "" {
		return errors.New("shard: node ID must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
	r.loads.LoadOrStore(n.ID, new(int64))
	r.rebuild()
	return nil
}

// RemoveNode deletes a node and rebuilds the shard table. It returns false
// if the node was not a member.
func (r *Router) RemoveNode(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return false
	}
	delete(r.nodes, id)
	r.loads.Delete(id)
	r.rebuild()
	return true
}

// Nodes returns a snapshot of current members, sorted by ID.
func (r *Router) Nodes() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Replicas returns the replica node IDs for a shard, best candidate first.
func (r *Router) Replicas(shard uint32) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if int(shard) >= len(r.owners) {
		return nil
	}
	out := make([]string, len(r.owners[shard]))
	copy(out, r.owners[shard])
	return out
}

// Route resolves key to the best replica for the given client locality,
// minimizing cross-node hops and breaking ties by live load. Route is
// side-effect free with respect to load: callers that want load tracking
// must pair Acquire with Done around the request's lifetime.
func (r *Router) Route(key string, client Locality) (RouteDecision, error) {
	shardID := r.hashKey(key)

	r.mu.RLock()
	if len(r.nodes) == 0 {
		r.mu.RUnlock()
		return RouteDecision{}, ErrNoNodes
	}
	var reps []string
	if int(shardID) < len(r.owners) {
		reps = r.owners[shardID]
	}
	best := ""
	bestHop := HopRemote + 1
	for _, id := range reps {
		n := r.nodes[id]
		h := localityDistance(client, n)
		if h < bestHop {
			best, bestHop = id, h
			continue
		}
		if h == bestHop && best != "" && r.loadOf(id) < r.loadOf(best) {
			best = id
		}
	}
	if best == "" && len(reps) > 0 {
		best, bestHop = reps[0], localityDistance(client, r.nodes[reps[0]])
	}
	node := r.nodes[best]
	replicaCopy := make([]string, len(reps))
	copy(replicaCopy, reps)
	r.mu.RUnlock()

	atomic.AddUint64(&r.total, 1)
	atomic.AddUint64(&r.stats[int(bestHop)], 1)
	return RouteDecision{
		Key:      key,
		Shard:    shardID,
		Node:     node,
		Hops:     bestHop,
		Replicas: replicaCopy,
	}, nil
}

// Acquire increments the load counter of a node, marking an in-flight
// request. Every Acquire must be paired with exactly one Done.
func (r *Router) Acquire(nodeID string) {
	if lp, ok := r.loads.Load(nodeID); ok {
		atomic.AddInt64(lp.(*int64), 1)
	}
}

// Done decrements the load counter previously incremented by Acquire.
func (r *Router) Done(nodeID string) {
	if lp, ok := r.loads.Load(nodeID); ok {
		atomic.AddInt64(lp.(*int64), -1)
	}
}

func (r *Router) loadOf(id string) int64 {
	if lp, ok := r.loads.Load(id); ok {
		return atomic.LoadInt64(lp.(*int64))
	}
	return 0
}

// Load returns the current load counter of a node.
func (r *Router) Load(nodeID string) int64 { return r.loadOf(nodeID) }

// Stats returns a consistent snapshot of routing counters.
func (r *Router) Stats() Stats {
	return Stats{
		Total:    atomic.LoadUint64(&r.total),
		SameNode: atomic.LoadUint64(&r.stats[int(HopSameNode)]),
		SameRack: atomic.LoadUint64(&r.stats[int(HopSameRack)]),
		SameZone: atomic.LoadUint64(&r.stats[int(HopSameZone)]),
		Remote:   atomic.LoadUint64(&r.stats[int(HopRemote)]),
	}
}

// ShardOf returns the virtual shard id for a key.
func (r *Router) ShardOf(key string) uint32 { return r.hashKey(key) }

// Distribution returns how many vshards each node primarily owns (first
// replica), useful for balance assertions.
func (r *Router) Distribution() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int, len(r.nodes))
	for _, reps := range r.owners {
		if len(reps) > 0 {
			out[reps[0]]++
		}
	}
	return out
}

// Movement reports how many vshards changed primary owner between this
// router's current table and other's.
func (r *Router) Movement(other *Router) (moved int, total int, err error) {
	if r.numShards != other.numShards {
		return 0, 0, fmt.Errorf("shard: shard count mismatch %d vs %d", r.numShards, other.numShards)
	}
	a := r.snapshotOwners()
	b := other.snapshotOwners()
	for i := range a {
		ai, bi := "", ""
		if len(a[i]) > 0 {
			ai = a[i][0]
		}
		if len(b[i]) > 0 {
			bi = b[i][0]
		}
		if ai != bi {
			moved++
		}
	}
	return moved, len(a), nil
}

func (r *Router) snapshotOwners() [][]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([][]string, len(r.owners))
	for i, reps := range r.owners {
		cp := make([]string, len(reps))
		copy(cp, reps)
		out[i] = cp
	}
	return out
}
