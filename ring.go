// Package hashring implements a topology-aware consistent hash ring and a
// dual-ring (Active/Standby) hot-standby routing controller.
package hashring

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
)

// Node describes a physical node together with its network topology labels.
// Zone and Rack are failure domains used for topology-aware replica spread.
type Node struct {
	ID     string
	Zone   string
	Rack   string
	Weight int // virtual-node multiplier; values <= 0 are treated as 1
}

func (n Node) weight() int {
	if n.Weight <= 0 {
		return 1
	}
	return n.Weight
}

// vnode is one point on the ring owned by a node.
type vnode struct {
	hash uint64
	node string
}

// Ring is an immutable consistent-hash ring. Once built it is never mutated,
// so it is safe for concurrent reads without locks and can be hot-swapped
// with a single atomic pointer store.
type Ring struct {
	nodes   map[string]Node
	points  []vnode // sorted by hash
	version uint64
}

// Version identifies the ring generation carried by the controller.
func (r *Ring) Version() uint64 { return r.version }

// NumNodes returns the number of physical nodes on the ring.
func (r *Ring) NumNodes() int { return len(r.nodes) }

// Has reports whether a node ID is present on the ring.
func (r *Ring) Has(id string) bool {
	_, ok := r.nodes[id]
	return ok
}

// Node returns the metadata for a node ID.
func (r *Ring) Node(id string) (Node, bool) {
	n, ok := r.nodes[id]
	return n, ok
}

// Nodes returns a copy of the node set.
func (r *Ring) Nodes() map[string]Node {
	out := make(map[string]Node, len(r.nodes))
	for k, v := range r.nodes {
		out[k] = v
	}
	return out
}

func hashKey(parts ...string) uint64 {
	h := fnv.New64a()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	// FNV-1a alone clusters sequential inputs (vnode IDs, sequential keys);
	// a final avalanche mix spreads points uniformly over the hash space.
	return fmix64(h.Sum64())
}

// fmix64 is the 64-bit MurmurHash3 finalizer: a bijective bit mixer.
func fmix64(k uint64) uint64 {
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

// ownerIdx returns the index of the vnode owning hash h (first point >= h,
// wrapping around to 0).
func (r *Ring) ownerIdx(h uint64) int {
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i].hash >= h })
	if i == len(r.points) {
		return 0
	}
	return i
}

// Lookup returns the primary owner of key.
func (r *Ring) Lookup(key string) (Node, bool) {
	if len(r.points) == 0 {
		return Node{}, false
	}
	vn := r.points[r.ownerIdx(hashKey(key))]
	return r.nodes[vn.node], true
}

// LookupN returns up to n distinct nodes for key, walking the ring in order.
// Placement is topology-aware: replicas are spread across distinct Zones
// first, then across distinct Racks, before any domain is reused.
func (r *Ring) LookupN(key string, n int) []Node {
	if n <= 0 || len(r.points) == 0 {
		return nil
	}
	if n > len(r.nodes) {
		n = len(r.nodes)
	}
	start := r.ownerIdx(hashKey(key))
	seen := make(map[string]struct{}, len(r.nodes))
	picked := make([]Node, 0, n)
	usedZone := make(map[string]bool)
	usedRack := make(map[string]bool)

	// Single walk over distinct nodes in ring order, two selection passes:
	// pass 0 prefers unused zones, pass 1 unused racks, pass 2 anything.
	type candidate struct {
		node Node
		ord  int
	}
	var cands []candidate
	for i, ord := start, 0; ord < len(r.points) && len(seen) < len(r.nodes); ord++ {
		vn := r.points[i%len(r.points)]
		i++
		if _, dup := seen[vn.node]; dup {
			continue
		}
		seen[vn.node] = struct{}{}
		cands = append(cands, candidate{node: r.nodes[vn.node], ord: len(cands)})
	}
	for pass := 0; pass < 3 && len(picked) < n; pass++ {
		for _, c := range cands {
			if len(picked) >= n {
				break
			}
			if c.ord < 0 {
				continue
			}
			switch pass {
			case 0:
				if usedZone[c.node.Zone] {
					continue
				}
			case 1:
				if usedRack[c.node.Zone+"/"+c.node.Rack] {
					continue
				}
			}
			cands[c.ord].ord = -1
			picked = append(picked, c.node)
			usedZone[c.node.Zone] = true
			usedRack[c.node.Zone+"/"+c.node.Rack] = true
		}
	}
	return picked
}

// Builder accumulates topology changes and produces immutable Rings.
// A Builder is NOT safe for concurrent use.
type Builder struct {
	nodes    map[string]Node
	replicas int // base vnode count per unit of weight
}

// NewBuilder creates a Builder with the given base virtual-node count
// (per unit of node weight). Values <= 0 default to 128.
func NewBuilder(replicas int) *Builder {
	if replicas <= 0 {
		replicas = 128
	}
	return &Builder{nodes: make(map[string]Node), replicas: replicas}
}

// FromRing creates a Builder pre-populated with the node set of r.
// This is how the standby ring inherits the active topology.
func FromRing(r *Ring, replicas int) *Builder {
	b := NewBuilder(replicas)
	for _, n := range r.nodes {
		b.nodes[n.ID] = n
	}
	return b
}

// Add inserts or replaces a node.
func (b *Builder) Add(n Node) error {
	if n.ID == "" {
		return fmt.Errorf("hashring: node ID must not be empty")
	}
	b.nodes[n.ID] = n
	return nil
}

// Remove deletes a node. It reports whether the node existed.
func (b *Builder) Remove(id string) bool {
	if _, ok := b.nodes[id]; !ok {
		return false
	}
	delete(b.nodes, id)
	return true
}

// Has reports whether the builder contains a node ID.
func (b *Builder) Has(id string) bool {
	_, ok := b.nodes[id]
	return ok
}

// Len returns the number of nodes staged in the builder.
func (b *Builder) Len() int { return len(b.nodes) }

// Build freezes the staged topology into an immutable Ring with the given
// version. The builder remains usable afterwards.
func (b *Builder) Build(version uint64) *Ring {
	nodes := make(map[string]Node, len(b.nodes))
	var points []vnode
	for _, n := range b.nodes {
		nodes[n.ID] = n
		vcount := b.replicas * n.weight()
		for i := 0; i < vcount; i++ {
			points = append(points, vnode{
				hash: hashKey(n.ID, strconv.Itoa(i)),
				node: n.ID,
			})
		}
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].hash == points[j].hash {
			return points[i].node < points[j].node
		}
		return points[i].hash < points[j].hash
	})
	return &Ring{nodes: nodes, points: points, version: version}
}
