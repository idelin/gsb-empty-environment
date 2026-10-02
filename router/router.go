// Package router implements hierarchical sharding with locality-aware
// routing for very large topologies.
//
// Keys are mapped to a fixed set of virtual shards (hash(key) % N), and
// virtual shards are assigned to physical nodes with weighted rendezvous
// (HRW) hashing. Because each shard is assigned independently and
// deterministically, adding or removing a node only moves the shards
// that land on that node (~1/n of the keyspace), which drastically
// reduces cross-node traffic during rebalancing.
//
// Routing is locality-aware: given the caller's region/zone, candidate
// nodes are ordered same-zone > same-region > remote, so requests stay
// inside the cheapest network boundary whenever capacity allows.
//
// Router is safe for concurrent use and spawns no background goroutines.
package router

import (
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
)

var (
	ErrNoNodes       = errors.New("router: no nodes registered")
	ErrEmptyNodeID   = errors.New("router: node ID must not be empty")
	ErrDuplicateNode = errors.New("router: node already registered")
	ErrNodeNotFound  = errors.New("router: node not found")
	ErrInvalidShards = errors.New("router: shard count must be positive")
	ErrInvalidWeight = errors.New("router: node weight must be positive")
	ErrInvalidFanout = errors.New("router: fanout must be positive")
)

// Locality is a position in the two-level network hierarchy.
type Locality struct {
	Region string
	Zone   string
}

// Node is a physical member of the cluster.
type Node struct {
	ID       string
	Locality Locality
	Weight   int // relative share of virtual shards; 0 is treated as 1
}

func (n Node) weight() int {
	if n.Weight <= 0 {
		return 1
	}
	return n.Weight
}

// Router maps keys to nodes through virtual shards.
type Router struct {
	mu        sync.RWMutex
	numShards int
	nodes     map[string]Node
	owners    []string // owners[shard] = nodeID, "" when no nodes
	version   uint64   // bumped on every membership change
}

// New creates a Router with numShards virtual shards.
func New(numShards int) (*Router, error) {
	if numShards <= 0 {
		return nil, ErrInvalidShards
	}
	return &Router{
		numShards: numShards,
		nodes:     make(map[string]Node),
		owners:    make([]string, numShards),
	}, nil
}

// NumShards returns the virtual shard count.
func (r *Router) NumShards() int { return r.numShards }

// Version returns the membership generation; it changes on every
// AddNode/RemoveNode so callers can cache routes per version.
func (r *Router) Version() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.version
}

// AddNode registers a node and rebalances virtual shards.
func (r *Router) AddNode(n Node) error {
	if n.ID == "" {
		return ErrEmptyNodeID
	}
	if n.Weight < 0 {
		return ErrInvalidWeight
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.nodes[n.ID]; dup {
		return ErrDuplicateNode
	}
	r.nodes[n.ID] = n
	r.rebuildLocked()
	r.version++
	return nil
}

// RemoveNode unregisters a node; its shards are re-assigned to the
// remaining nodes.
func (r *Router) RemoveNode(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[id]; !ok {
		return ErrNodeNotFound
	}
	delete(r.nodes, id)
	r.rebuildLocked()
	r.version++
	return nil
}

// ShardOf maps a key to its virtual shard in [0, NumShards).
func (r *Router) ShardOf(key string) int {
	return int(hash64(key) % uint64(r.numShards))
}

// OwnerOf returns the node currently owning a virtual shard.
func (r *Router) OwnerOf(shard int) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if shard < 0 || shard >= r.numShards {
		return Node{}, false
	}
	id := r.owners[shard]
	if id == "" {
		return Node{}, false
	}
	n, ok := r.nodes[id]
	return n, ok
}

// Route returns up to fanout candidate nodes for key, best first: the
// shard owner is always rank 0, followed by fallbacks ordered by
// locality to the client (same zone > same region > remote) with a
// deterministic per-key tie-break so retries spread across peers.
func (r *Router) Route(key string, client Locality, fanout int) ([]Node, error) {
	if fanout <= 0 {
		return nil, ErrInvalidFanout
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.nodes) == 0 {
		return nil, ErrNoNodes
	}
	owner := r.owners[r.shardOfLocked(key)]
	ranked := make([]Node, 0, len(r.nodes))
	if owner != "" {
		ranked = append(ranked, r.nodes[owner])
	}
	rest := make([]Node, 0, len(r.nodes)-1)
	for _, n := range r.nodes {
		if n.ID != owner {
			rest = append(rest, n)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		si, sj := localityScore(client, rest[i].Locality), localityScore(client, rest[j].Locality)
		if si != sj {
			return si > sj
		}
		return hash64(key, rest[i].ID) < hash64(key, rest[j].ID)
	})
	ranked = append(ranked, rest...)
	if fanout > len(ranked) {
		fanout = len(ranked)
	}
	return ranked[:fanout], nil
}

// Distribution returns how many virtual shards each node owns.
func (r *Router) Distribution() map[string]int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]int, len(r.nodes))
	for _, id := range r.owners {
		if id != "" {
			out[id]++
		}
	}
	return out
}

// Nodes returns a snapshot of registered nodes, sorted by ID.
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

func (r *Router) shardOfLocked(key string) int {
	return int(hash64(key) % uint64(r.numShards))
}

// rebuildLocked re-assigns every virtual shard with weighted rendezvous
// (HRW) hashing: each node gets one rendezvous point per weight unit
// and its score is the best of hash(shard, node, replica). A node with
// weight w therefore wins ~w times as many shards. Shards whose winner
// did not change keep their owner, so membership changes move the
// minimum possible number of shards.
func (r *Router) rebuildLocked() {
	if len(r.nodes) == 0 {
		for i := range r.owners {
			r.owners[i] = ""
		}
		return
	}
	for shard := 0; shard < r.numShards; shard++ {
		s := strconv.Itoa(shard)
		best := ""
		var bestScore uint64
		for id, n := range r.nodes {
			for rep := 0; rep < n.weight(); rep++ {
				score := hash64(s, id, strconv.Itoa(rep))
				if best == "" || score > bestScore || (score == bestScore && id < best) {
					best, bestScore = id, score
				}
			}
		}
		r.owners[shard] = best
	}
}

// localityScore ranks how close a candidate is to the client:
// 2 = same zone, 1 = same region, 0 = remote.
func localityScore(client, cand Locality) int {
	if client.Region != "" && client.Region == cand.Region {
		if client.Zone != "" && client.Zone == cand.Zone {
			return 2
		}
		return 1
	}
	return 0
}

func hash64(parts ...string) uint64 {
	h := fnv.New64a()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	// FNV-1a has a weak avalanche: inputs differing only in the last
	// byte (e.g. "node-8" vs "node-9") produce correlated hashes, which
	// skews rendezvous assignment. Finalize with the murmur3 64-bit
	// mix so every input bit flips each output bit with ~50% chance.
	x := h.Sum64()
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
