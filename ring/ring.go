// Package ring implements a consistent hash ring over a configurable
// bit-width position space. A ring of bit width W places virtual nodes
// and keys at positions in [0, 2^W) taken from the top W bits of the
// underlying 64-bit hash. Narrowing W is what exposes hash functions
// whose high output bits lack uniformity.
package ring

import (
	"math"
	"sort"
	"strconv"

	"hashring-analyzer/hashfn"
)

// Ring maps keys to nodes on a consistent hash ring.
type Ring struct {
	hash     hashfn.Hash64
	bitWidth uint
	mask     uint64
	nodes    []string
	vnodes   []vnode // sorted by pos
}

type vnode struct {
	pos  uint64
	node int // index into Ring.nodes
}

// New builds a ring with the given hash function, node names, number of
// virtual nodes (replicas) per node, and ring bit width (1..64).
func New(h hashfn.Hash64, nodes []string, replicas int, bitWidth uint) *Ring {
	if bitWidth == 0 || bitWidth > 64 {
		panic("ring: bitWidth must be in [1, 64]")
	}
	if len(nodes) == 0 {
		panic("ring: at least one node is required")
	}
	if replicas < 1 {
		panic("ring: replicas must be >= 1")
	}
	r := &Ring{
		hash:     h,
		bitWidth: bitWidth,
		mask:     mask(bitWidth),
		nodes:    append([]string(nil), nodes...),
	}
	for i, name := range nodes {
		for v := 0; v < replicas; v++ {
			key := name + "#vnode#" + strconv.Itoa(v)
			r.vnodes = append(r.vnodes, vnode{pos: r.position(r.hash([]byte(key))), node: i})
		}
	}
	sort.Slice(r.vnodes, func(a, b int) bool { return r.vnodes[a].pos < r.vnodes[b].pos })
	return r
}

func mask(bitWidth uint) uint64 {
	if bitWidth >= 64 {
		return math.MaxUint64
	}
	return (uint64(1) << bitWidth) - 1
}

// position extracts the top bitWidth bits of the hash.
func (r *Ring) position(h uint64) uint64 {
	if r.bitWidth >= 64 {
		return h
	}
	return h >> (64 - r.bitWidth)
}

// NumNodes returns the number of physical nodes.
func (r *Ring) NumNodes() int { return len(r.nodes) }

// Nodes returns the node names.
func (r *Ring) Nodes() []string { return append([]string(nil), r.nodes...) }

// BitWidth returns the ring position space width in bits.
func (r *Ring) BitWidth() uint { return r.bitWidth }

// Map returns the index of the node owning the key.
func (r *Ring) Map(key []byte) int {
	pos := r.position(r.hash(key))
	i := sort.Search(len(r.vnodes), func(i int) bool { return r.vnodes[i].pos >= pos })
	if i == len(r.vnodes) {
		i = 0 // wrap around the ring
	}
	return r.vnodes[i].node
}

// ArcFractions returns the fraction of the ring owned by each node,
// i.e. the sum of its virtual nodes' arc lengths divided by 2^bitWidth.
// A perfectly uniform hash maps keys to nodes in exactly these
// proportions, which makes them the right expected values for a
// chi-square goodness-of-fit test of the hash (not of the ring layout).
func (r *Ring) ArcFractions() []float64 {
	arcs := make([]uint64, len(r.nodes))
	n := len(r.vnodes)
	if n == 1 {
		arcs[r.vnodes[0].node] = 1 // whole ring; normalized below
	} else {
		for i, v := range r.vnodes {
			prev := r.vnodes[(i-1+n)%n].pos
			arcs[v.node] += (v.pos - prev) & r.mask
		}
	}
	total := float64(0)
	for _, a := range arcs {
		total += float64(a)
	}
	if total == 0 {
		// Degenerate ring: every virtual node collapsed onto a single
		// position (possible with a weak hash on a narrow ring). Map
		// sends every key to vnodes[0] in this case, so mirror that.
		arcs = make([]uint64, len(r.nodes))
		arcs[r.vnodes[0].node] = 1
		total = 1
	}
	fracs := make([]float64, len(arcs))
	for i, a := range arcs {
		fracs[i] = float64(a) / total
	}
	return fracs
}
