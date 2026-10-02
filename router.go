package leaserouter

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// DefaultLeaseTTL is used when a router is created with a non-positive TTL.
const DefaultLeaseTTL = 30 * time.Second

// Router maintains an ordered, non-overlapping set of range leases and
// routes keys to the lease-holding node. Route only takes a read lock,
// so concurrent lookups never block each other; only topology changes
// (Grant/Split) take the write lock.
type Router struct {
	mu     sync.RWMutex
	leases []*Lease // sorted by Range.Start, non-overlapping
	nodes  map[string]*Node
	ttl    time.Duration
	nextID int64
}

// NewRouter creates a router that issues leases with the given TTL.
func NewRouter(ttl time.Duration) *Router {
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	return &Router{nodes: make(map[string]*Node), ttl: ttl}
}

// AddNode registers a storage node so leases can be granted to it.
func (r *Router) AddNode(n *Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nodes[n.ID] = n
}

// Grant issues a fresh root lease for rng to nodeID.
// It fails if rng overlaps any existing lease.
func (r *Router) Grant(rng KeyRange, nodeID string) (*Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.nodes[nodeID]; !ok {
		return nil, fmt.Errorf("unknown node %q", nodeID)
	}
	for _, l := range r.leases {
		if overlaps(l.Range, rng) {
			return nil, fmt.Errorf("range %s overlaps existing lease %s", rng, l.Range)
		}
	}
	r.nextID++
	lease := &Lease{
		ID:       r.nextID,
		NodeID:   nodeID,
		Range:    rng,
		Epoch:    1,
		IssuedAt: time.Now(),
		TTL:      r.ttl,
	}
	idx := sort.Search(len(r.leases), func(i int) bool {
		return r.leases[i].Range.Start > rng.Start
	})
	r.leases = append(r.leases, nil)
	copy(r.leases[idx+1:], r.leases[idx:])
	r.leases[idx] = lease
	return lease, nil
}

// Route returns the lease and the node responsible for key.
func (r *Router) Route(key string) (*Lease, *Node, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, l := range r.leases {
		if l.Range.Contains(key) {
			return l, r.nodes[l.NodeID], nil
		}
	}
	return nil, nil, fmt.Errorf("no lease covers key %q", key)
}

// Leases returns a snapshot of the current lease table.
func (r *Router) Leases() []*Lease {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Lease, len(r.leases))
	copy(out, r.leases)
	return out
}

// Split divides the range containing boundary into [Start, boundary) and
// [boundary, End). The left child lease is inherited by the current
// holder; the right child is granted to rightNodeID. Both children record
// the parent lease ID and inherit a bumped fencing epoch, so any stale
// parent lease is immediately fenced off.
func (r *Router) Split(boundary, rightNodeID string) (left, right *Lease, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	idx := -1
	for i, l := range r.leases {
		if l.Range.Contains(boundary) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, nil, fmt.Errorf("no range contains split boundary %q", boundary)
	}
	parent := r.leases[idx]
	if boundary == parent.Range.Start {
		return nil, nil, fmt.Errorf("split boundary %q equals range start %q", boundary, parent.Range.Start)
	}
	if _, ok := r.nodes[rightNodeID]; !ok {
		return nil, nil, fmt.Errorf("unknown node %q", rightNodeID)
	}

	now := time.Now()
	r.nextID++
	left = &Lease{
		ID:       r.nextID,
		Parent:   parent.ID,
		NodeID:   parent.NodeID, // lease inheritance: holder keeps the left half
		Range:    KeyRange{Start: parent.Range.Start, End: boundary},
		Epoch:    parent.Epoch + 1,
		IssuedAt: now,
		TTL:      r.ttl,
	}
	r.nextID++
	right = &Lease{
		ID:       r.nextID,
		Parent:   parent.ID,
		NodeID:   rightNodeID,
		Range:    KeyRange{Start: boundary, End: parent.Range.End},
		Epoch:    parent.Epoch + 1,
		IssuedAt: now,
		TTL:      r.ttl,
	}

	next := make([]*Lease, 0, len(r.leases)+1)
	next = append(next, r.leases[:idx]...)
	next = append(next, left, right)
	next = append(next, r.leases[idx+1:]...)
	r.leases = next
	return left, right, nil
}
