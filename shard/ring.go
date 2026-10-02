package shard

import (
	"hash/fnv"
	"sort"
	"strconv"
	"sync"
)

// Ring is a consistent-hash ring mapping shard keys to physical nodes.
// Each node owns vnodes virtual points, so removing or adding a node only
// remaps the shards previously owned by it.
type Ring struct {
	mu      sync.RWMutex
	vnodes  int
	points  []vnode // sorted by hash
	members map[NodeID]struct{}
}

type vnode struct {
	hash uint64
	node NodeID
}

// NewRing creates a consistent-hash ring with the given number of virtual
// nodes per physical node.
func NewRing(vnodesPerNode int) *Ring {
	if vnodesPerNode <= 0 {
		vnodesPerNode = 128
	}
	return &Ring{
		vnodes:  vnodesPerNode,
		members: make(map[NodeID]struct{}),
	}
}

func hashBytes(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

// mix64 is the splitmix64 finalizer: a strong bit mixer so that structured
// keys (sequential shard ids, vnode names) spread evenly over the ring.
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func vnodeHash(node NodeID, i int) uint64 {
	h := hashBytes([]byte(string(node) + "#" + strconv.Itoa(i)))
	return mix64(h + uint64(i)*0x9e3779b97f4a7c15)
}

// AddNode adds a physical node to the ring. Adding an existing node is a
// no-op.
func (r *Ring) AddNode(id NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[id]; ok {
		return
	}
	r.members[id] = struct{}{}
	for i := 0; i < r.vnodes; i++ {
		r.points = append(r.points, vnode{hash: vnodeHash(id, i), node: id})
	}
	sort.Slice(r.points, func(a, b int) bool {
		if r.points[a].hash != r.points[b].hash {
			return r.points[a].hash < r.points[b].hash
		}
		return r.points[a].node < r.points[b].node
	})
}

// RemoveNode removes a physical node and all its virtual points.
func (r *Ring) RemoveNode(id NodeID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[id]; !ok {
		return
	}
	delete(r.members, id)
	kept := r.points[:0]
	for _, p := range r.points {
		if p.node != id {
			kept = append(kept, p)
		}
	}
	r.points = kept
}

// Owner returns the node owning the given shard key.
func (r *Ring) Owner(key uint64) (NodeID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.points) == 0 {
		return "", false
	}
	h := mix64(key)
	i := sort.Search(len(r.points), func(i int) bool {
		return r.points[i].hash >= h
	})
	if i == len(r.points) {
		i = 0
	}
	return r.points[i].node, true
}

// Nodes returns the current physical nodes.
func (r *Ring) Nodes() []NodeID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]NodeID, 0, len(r.members))
	for n := range r.members {
		out = append(out, n)
	}
	sort.Slice(out, func(a, b int) bool { return out[a] < out[b] })
	return out
}

// Size returns the number of physical nodes.
func (r *Ring) Size() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.members)
}
