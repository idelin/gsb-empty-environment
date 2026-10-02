// Package hotring implements a topology-aware active/standby hash ring
// router: an Active ring serves all traffic while a Standby ring is
// rebuilt and warmed up in the background, then swapped in atomically.
package hotring

import (
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
)

// DefaultReplicas is the number of virtual nodes per physical node.
const DefaultReplicas = 160

// ErrEmptyRing is returned when a lookup hits a ring with no nodes.
var ErrEmptyRing = errors.New("hotring: ring has no nodes")

// Ring is an immutable consistent-hash ring. Once built it is never
// modified, which makes it safe to share across goroutines without locks.
type Ring struct {
	replicas int
	points   []uint32          // sorted virtual-node hash points
	owners   map[uint32]string // hash point -> physical node
	members  map[string]struct{}
}

// NewRing builds a ring from the given physical node IDs.
// Duplicate node IDs are collapsed. The replicas argument controls how
// many virtual nodes each physical node contributes; <= 0 uses DefaultReplicas.
func NewRing(nodes []string, replicas int) *Ring {
	if replicas <= 0 {
		replicas = DefaultReplicas
	}
	r := &Ring{
		replicas: replicas,
		owners:   make(map[uint32]string),
		members:  make(map[string]struct{}),
	}
	for _, node := range nodes {
		if _, dup := r.members[node]; dup {
			continue
		}
		r.members[node] = struct{}{}
		for i := 0; i < replicas; i++ {
			h := hashKey(node + "#" + strconv.Itoa(i))
			if _, taken := r.owners[h]; !taken {
				r.owners[h] = node
				r.points = append(r.points, h)
			}
		}
	}
	sort.Slice(r.points, func(i, j int) bool { return r.points[i] < r.points[j] })
	return r
}

func hashKey(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// Get routes key to its owner node on the ring.
func (r *Ring) Get(key string) (string, error) {
	if len(r.points) == 0 {
		return "", ErrEmptyRing
	}
	h := hashKey(key)
	// Binary search for the first point >= h, wrapping around the ring.
	i := sort.Search(len(r.points), func(i int) bool { return r.points[i] >= h })
	if i == len(r.points) {
		i = 0
	}
	return r.owners[r.points[i]], nil
}

// Members returns the sorted list of physical node IDs on the ring.
func (r *Ring) Members() []string {
	out := make([]string, 0, len(r.members))
	for n := range r.members {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Size returns the number of physical nodes on the ring.
func (r *Ring) Size() int { return len(r.members) }

// Replicas returns the virtual-node count per physical node.
func (r *Ring) Replicas() int { return r.replicas }
