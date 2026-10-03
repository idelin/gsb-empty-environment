// Package shard implements hierarchical virtual sharding and
// locality-aware routing on top of the hexgrid cell space.
//
// Cells are mapped to a fixed number of virtual shards through their
// ancestor at a configurable "shard resolution", so topologically close
// cells (e.g. a k-ring disk) land on the same or very few shards. Virtual
// shards are assigned to physical nodes with rendezvous (HRW) hashing,
// which keeps remapping minimal when nodes join or leave. The Router holds
// no background goroutines and is safe for concurrent use.
package shard

import (
	"encoding/binary"
	"hash/fnv"
	"sort"
	"sync"

	"hexgsb/hexgrid"
)

// Router maps cells to virtual shards and shards to nodes.
type Router struct {
	mu       sync.RWMutex
	shards   int // number of virtual shards, a power of two
	shardRes int // ancestor resolution used as the locality key
	nodes    []string
}

// NewRouter creates a Router with numShards virtual shards (must be a
// positive power of two) and the given initial nodes. shardRes is the
// ancestor resolution used for locality-preserving shard keys.
func NewRouter(numShards, shardRes int, nodes []string) *Router {
	if numShards <= 0 || numShards&(numShards-1) != 0 {
		panic("shard: numShards must be a positive power of two")
	}
	if shardRes < 0 || shardRes > hexgrid.MaxResolution {
		panic("shard: invalid shard resolution")
	}
	return &Router{
		shards:   numShards,
		shardRes: shardRes,
		nodes:    normalizeNodes(nodes),
	}
}

func normalizeNodes(nodes []string) []string {
	set := make(map[string]struct{}, len(nodes))
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n == "" {
			continue
		}
		if _, ok := set[n]; ok {
			continue
		}
		set[n] = struct{}{}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// NumShards returns the number of virtual shards.
func (r *Router) NumShards() int { return r.shards }

// ShardResolution returns the ancestor resolution used for shard keys.
func (r *Router) ShardResolution() int { return r.shardRes }

// Nodes returns a sorted snapshot of the current nodes.
func (r *Router) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.nodes))
	copy(out, r.nodes)
	return out
}

// AddNode registers a node. It reports whether the node was new.
func (r *Router) AddNode(id string) bool {
	if id == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, n := range r.nodes {
		if n == id {
			return false
		}
	}
	r.nodes = append(r.nodes, id)
	sort.Strings(r.nodes)
	return true
}

// RemoveNode unregisters a node. It reports whether the node existed.
func (r *Router) RemoveNode(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, n := range r.nodes {
		if n == id {
			r.nodes = append(r.nodes[:i], r.nodes[i+1:]...)
			return true
		}
	}
	return false
}

// ShardOf maps a cell to a virtual shard. The shard key is the cell's
// ancestor at the shard resolution, so neighbors almost always collide on
// the same shard, which is what keeps cross-node traffic low.
func (r *Router) ShardOf(c hexgrid.Cell) uint32 {
	a := c
	if c.Resolution() > r.shardRes {
		a, _ = c.Parent(r.shardRes)
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(a))
	h := fnv.New64a()
	h.Write(buf[:])
	return uint32(h.Sum64() & uint64(r.shards-1))
}

func hashShardNode(shard uint32, node string) uint64 {
	h := fnv.New64a()
	var buf [4]byte
	binary.LittleEndian.PutUint32(buf[:], shard)
	h.Write(buf[:])
	h.Write([]byte(node))
	return mix64(h.Sum64())
}

// mix64 is the splitmix64 finalizer; FNV-1a alone has weak avalanche in
// the high bits, which would skew rendezvous-hashing comparisons.
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// NodeForShard resolves a virtual shard to a node using rendezvous (HRW)
// hashing: the node with the highest hash weight wins. Adding or removing
// one of N nodes remaps only ~1/N of the shards.
func (r *Router) NodeForShard(shard uint32) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.nodes) == 0 {
		return "", false
	}
	best := r.nodes[0]
	bestH := hashShardNode(shard, best)
	for _, n := range r.nodes[1:] {
		if h := hashShardNode(shard, n); h > bestH {
			bestH = h
			best = n
		}
	}
	return best, true
}

// NodeForCell resolves a cell to its owning node.
func (r *Router) NodeForCell(c hexgrid.Cell) (string, bool) {
	return r.NodeForShard(r.ShardOf(c))
}

// Plan is the result of routing a batch of cells.
type Plan struct {
	// Assignments groups cells by the node they were routed to.
	Assignments map[string][]hexgrid.Cell
	// Cells is the number of input cells.
	Cells int
	// Shards is the number of distinct virtual shards touched.
	Shards int
}

// Route groups cells by their owning node so each node receives one
// contiguous, topology-local batch.
func (r *Router) Route(cells []hexgrid.Cell) *Plan {
	plan := &Plan{
		Assignments: make(map[string][]hexgrid.Cell),
		Cells:       len(cells),
	}
	shardSet := make(map[uint32]struct{})
	for _, c := range cells {
		s := r.ShardOf(c)
		shardSet[s] = struct{}{}
		n, ok := r.NodeForShard(s)
		if !ok {
			continue
		}
		plan.Assignments[n] = append(plan.Assignments[n], c)
	}
	plan.Shards = len(shardSet)
	return plan
}

// LocalityScore walks every adjacent pair inside cells and counts how many
// pairs are split across different nodes. A low cross/total ratio means
// the routing preserves topology and minimizes inter-node communication.
func (r *Router) LocalityScore(cells []hexgrid.Cell) (total, cross int) {
	nodeOf := make(map[hexgrid.Cell]string, len(cells))
	for _, c := range cells {
		if n, ok := r.NodeForCell(c); ok {
			nodeOf[c] = n
		}
	}
	for c, n1 := range nodeOf {
		for _, nb := range c.Neighbors() {
			n2, ok := nodeOf[nb]
			if !ok || nb < c {
				continue
			}
			total++
			if n1 != n2 {
				cross++
			}
		}
	}
	return total, cross
}
