package hashring

import (
	"sort"
	"strconv"
)

// Ring is a consistent-hash ring over a 32-bit hash space.
type Ring struct {
	nodes  []string
	points []uint32 // sorted virtual-node positions
	owners []int    // owners[i] is the node index owning points[i]
}

// NewRing builds a ring placing vnodes virtual points per node with hash h.
func NewRing(nodes []string, vnodes int, h HashFunc) *Ring {
	r := &Ring{nodes: append([]string(nil), nodes...)}
	type vp struct {
		pos   uint32
		owner int
	}
	all := make([]vp, 0, len(nodes)*vnodes)
	for i, n := range nodes {
		for v := 0; v < vnodes; v++ {
			label := n + "#" + strconv.Itoa(v)
			all = append(all, vp{pos: h([]byte(label)), owner: i})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].pos < all[j].pos })
	r.points = make([]uint32, len(all))
	r.owners = make([]int, len(all))
	for i, p := range all {
		r.points[i] = p.pos
		r.owners[i] = p.owner
	}
	return r
}

// NumNodes returns the number of physical nodes on the ring.
func (r *Ring) NumNodes() int { return len(r.nodes) }

// Locate returns the index of the node owning ring position hv.
func (r *Ring) Locate(hv uint32) int {
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i] >= hv })
	if i == len(r.points) {
		i = 0 // wrap around the ring
	}
	return r.owners[i]
}

// Shares returns the fraction of the ring's arc length owned by each node.
// This is the expected load share under a perfectly uniform hash, and it
// isolates hash-quality measurement from vnode placement randomness.
func (r *Ring) Shares() []float64 {
	const space = 1 << 32
	shares := make([]float64, len(r.nodes))
	for i, p := range r.points {
		var prev uint32
		if i == 0 {
			prev = r.points[len(r.points)-1]
		} else {
			prev = r.points[i-1]
		}
		arc := p - prev // uint32 wraparound handles the ring seam
		shares[r.owners[i]] += float64(arc) / space
	}
	return shares
}
