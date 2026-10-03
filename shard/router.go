// Package shard implements hierarchical sharding with locality-aware routing.
//
// The key space is split into a fixed number of virtual shards (vshards).
// Each vshard is placed on `replicas` physical nodes using weighted
// rendezvous (HRW) hashing, which keeps shard movement minimal when the
// membership changes. Placement prefers failure-domain diversity (distinct
// zones, then distinct regions) while routing prefers locality: a client
// is served by the replica closest in the Region -> Zone -> Node hierarchy.
package shard

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"sync"
)

var (
	ErrNoNodes   = errors.New("shard: no nodes in router")
	ErrBadVShard = errors.New("shard: numVShards must be > 0")
)

// Location is a node's (or client's) position in the topology hierarchy.
type Location struct {
	Region string
	Zone   string
}

// Node is a physical storage/serving node.
type Node struct {
	ID       string
	Loc      Location
	Capacity int // relative weight; <=0 is treated as 1
}

// Distance levels between a client location and a node.
const (
	DistSameZone    = 1
	DistSameRegion  = 2
	DistCrossRegion = 3
)

// Router owns the membership and the vshard placement table.
type Router struct {
	mu         sync.RWMutex
	numVShards int
	replicas   int
	nodes      map[string]*Node
	placement  [][]string // vshard -> ordered replica node IDs (primary first)
}

// NewRouter builds a router. replicas is capped at the node count.
func NewRouter(numVShards, replicas int) (*Router, error) {
	if numVShards <= 0 {
		return nil, ErrBadVShard
	}
	if replicas <= 0 {
		replicas = 1
	}
	return &Router{
		numVShards: numVShards,
		replicas:   replicas,
		nodes:      make(map[string]*Node),
		placement:  make([][]string, numVShards),
	}, nil
}

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return mix64(h.Sum64())
}

// mix64 is the splitmix64 finalizer; it gives the FNV output full avalanche
// so placement and key mapping are uniform.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// rendezvousKey computes a weighted HRW key: an exponential-race sample
// with rate = weight. Lower wins; P(win) is proportional to weight, and
// adding/removing nodes only affects the shards they win/lose.
func rendezvousKey(vshard, nodeID string, weight int) float64 {
	h := hash64("vshard", vshard, nodeID)
	u := float64(h>>11) / float64(uint64(1)<<53) // uniform in [0,1)
	if u <= 0 {
		u = 1e-12
	}
	return -math.Log(u) / float64(weight)
}

// VShardOf maps a key to its virtual shard.
func (r *Router) VShardOf(key string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return int(hash64("key", key) % uint64(r.numVShards))
}

func (r *Router) rf() int {
	if r.replicas > len(r.nodes) {
		return len(r.nodes)
	}
	return r.replicas
}

// rebuildLocked recomputes the whole placement table (membership changed).
// Returns the number of vshard replica slots that moved.
func (r *Router) rebuildLocked() int {
	moved := 0
	old := r.placement
	r.placement = make([][]string, r.numVShards)
	rf := r.rf()
	nodes := make([]*Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		nodes = append(nodes, n)
	}
	for v := 0; v < r.numVShards; v++ {
		r.placement[v] = placeVShard(v, nodes, rf)
		if !sameSet(old[v], r.placement[v]) {
			moved++
		}
	}
	return moved
}

// placeVShard picks rf nodes for one vshard: weighted rendezvous hashing
// (exponential race) for stability, with a preference for zone/region
// diversity.
func placeVShard(vshard int, nodes []*Node, rf int) []string {
	if rf == 0 {
		return nil
	}
	type scored struct {
		n     *Node
		score float64
	}
	vs := fmt.Sprintf("%d", vshard)
	ranked := make([]scored, 0, len(nodes))
	for _, n := range nodes {
		w := n.Capacity
		if w <= 0 {
			w = 1
		}
		ranked = append(ranked, scored{n, rendezvousKey(vs, n.ID, w)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score < ranked[j].score
		}
		return ranked[i].n.ID < ranked[j].n.ID
	})
	// Greedy diversity pass: first take the best-scoring node of each new
	// zone, then each new region, then fill by raw score.
	picked := make([]scored, 0, rf)
	used := make(map[string]bool)
	take := func(ok func(*Node) bool) {
		for _, s := range ranked {
			if len(picked) == rf {
				return
			}
			if used[s.n.ID] || !ok(s.n) {
				continue
			}
			used[s.n.ID] = true
			picked = append(picked, s)
		}
	}
	seenZone := make(map[string]bool)
	take(func(n *Node) bool {
		key := n.Loc.Region + "/" + n.Loc.Zone
		if seenZone[key] {
			return false
		}
		seenZone[key] = true
		return true
	})
	seenRegion := make(map[string]bool)
	for _, s := range picked {
		seenRegion[s.n.Loc.Region] = true
	}
	take(func(n *Node) bool {
		if seenRegion[n.Loc.Region] {
			return false
		}
		seenRegion[n.Loc.Region] = true
		return true
	})
	take(func(n *Node) bool { return true })

	out := make([]string, len(picked))
	for i, s := range picked {
		out[i] = s.n.ID
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
		if m[x] < 0 {
			return false
		}
	}
	return true
}

// AddNode registers a node and rebalances. Returns moved vshard count.
func (r *Router) AddNode(n *Node) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
	return r.rebuildLocked()
}

// RemoveNode deletes a node and rebalances. Returns moved vshard count.
func (r *Router) RemoveNode(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.nodes, id)
	return r.rebuildLocked()
}

// Replicas returns the ordered replica set (primary first) for a key.
func (r *Router) Replicas(key string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.nodes) == 0 {
		return nil, ErrNoNodes
	}
	v := int(hash64("key", key) % uint64(r.numVShards))
	out := make([]string, len(r.placement[v]))
	copy(out, r.placement[v])
	return out, nil
}

// Route returns the primary node for a key.
func (r *Router) Route(key string) (string, error) {
	reps, err := r.Replicas(key)
	if err != nil {
		return "", err
	}
	return reps[0], nil
}

// Distance measures topological distance between a client location and a node.
func Distance(from Location, n *Node) int {
	if from.Region == n.Loc.Region {
		if from.Zone == n.Loc.Zone {
			return DistSameZone
		}
		return DistSameRegion
	}
	return DistCrossRegion
}

// RouteLocal returns the replica topologically closest to the client.
// Ties break deterministically by node ID.
func (r *Router) RouteLocal(key string, from Location) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.nodes) == 0 {
		return "", ErrNoNodes
	}
	v := int(hash64("key", key) % uint64(r.numVShards))
	reps := r.placement[v]
	best := ""
	bestDist := int(^uint(0) >> 1)
	for _, id := range reps {
		d := Distance(from, r.nodes[id])
		if d < bestDist || (d == bestDist && id < best) {
			best, bestDist = id, d
		}
	}
	return best, nil
}

// Stats reports per-node primary share and average routing distance from a
// set of client locations; used to validate locality and balance.
type Stats struct {
	PrimaryCount map[string]int
	AvgDistance  float64
}

func (r *Router) Stats(keys []string, clients []Location) Stats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	st := Stats{PrimaryCount: make(map[string]int)}
	if len(r.nodes) == 0 || len(keys) == 0 {
		return st
	}
	var total float64
	var samples int
	for _, k := range keys {
		v := int(hash64("key", k) % uint64(r.numVShards))
		reps := r.placement[v]
		if len(reps) == 0 {
			continue
		}
		st.PrimaryCount[reps[0]]++
		for _, c := range clients {
			best := int(^uint(0) >> 1)
			for _, id := range reps {
				if d := Distance(c, r.nodes[id]); d < best {
					best = d
				}
			}
			total += float64(best)
			samples++
		}
	}
	if samples > 0 {
		st.AvgDistance = total / float64(samples)
	}
	return st
}

// NumVShards returns the configured virtual shard count.
func (r *Router) NumVShards() int { return r.numVShards }

// NodeCount returns the current membership size.
func (r *Router) NodeCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.nodes)
}
