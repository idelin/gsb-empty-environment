package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// DefaultLeaseTTL is the lifetime of a granted lease.
const DefaultLeaseTTL = 30 * time.Second

// Gateway is the lease-routing gateway. It owns the lease table and routes
// client queries to the node holding the lease for the query's key range.
//
// The gateway lock is only held while consulting the lease table, never
// while a node processes a query, so queries routed to different nodes are
// fully independent.
type Gateway struct {
	mu       sync.RWMutex
	leases   []*Lease // sorted by Range.Start, non-overlapping while valid
	nodes    map[string]*Node
	epoch    uint64
	LeaseTTL time.Duration
}

// NewGateway creates an empty gateway.
func NewGateway() *Gateway {
	return &Gateway{
		nodes:    make(map[string]*Node),
		LeaseTTL: DefaultLeaseTTL,
	}
}

// AddNode registers a storage node.
func (g *Gateway) AddNode(n *Node) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.nodes[n.ID] = n
}

// GrantLease grants a fresh lease for r to nodeID. It fails if r overlaps
// any currently valid lease.
func (g *Gateway) GrantLease(r KeyRange, nodeID string) (*Lease, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.nodes[nodeID]; !ok {
		return nil, fmt.Errorf("unknown node %q", nodeID)
	}
	for _, l := range g.leases {
		if l.valid && overlaps(l.Range, r) {
			return nil, fmt.Errorf("range %s overlaps valid lease %s", r, l.Range)
		}
	}
	g.epoch++
	l := &Lease{
		Range:   r,
		NodeID:  nodeID,
		Epoch:   g.epoch,
		Granted: time.Now(),
		TTL:     g.LeaseTTL,
		valid:   true,
	}
	g.leases = append(g.leases, l)
	g.sortLeases()
	return l, nil
}

// Split atomically splits the valid lease covering splitKey into two child
// ranges [Start, splitKey) and [splitKey, End), granting them to leftNode
// and rightNode.
//
// Lease inheritance: the children inherit the parent's epoch+1 and TTL, and
// the parent lease is revoked inside the same critical section, so routing
// never observes a gap or an overlap in key-space coverage.
func (g *Gateway) Split(splitKey, leftNode, rightNode string) (left, right *Lease, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	var parent *Lease
	parentIdx := -1
	for i, l := range g.leases {
		if l.valid && l.Range.Contains(splitKey) {
			parent, parentIdx = l, i
			break
		}
	}
	if parent == nil {
		return nil, nil, fmt.Errorf("no valid lease covers split key %q", splitKey)
	}
	if splitKey <= parent.Range.Start || splitKey >= parent.Range.End {
		return nil, nil, fmt.Errorf("split key %q not strictly inside %s", splitKey, parent.Range)
	}
	for _, id := range []string{leftNode, rightNode} {
		if _, ok := g.nodes[id]; !ok {
			return nil, nil, fmt.Errorf("unknown node %q", id)
		}
	}

	parent.valid = false // revoke the parent lease

	childEpoch := parent.Epoch + 1
	if g.epoch < childEpoch {
		g.epoch = childEpoch
	}
	now := time.Now()
	left = &Lease{
		Range:   KeyRange{Start: parent.Range.Start, End: splitKey},
		NodeID:  leftNode,
		Epoch:   childEpoch,
		Granted: now,
		TTL:     parent.TTL,
		valid:   true,
	}
	right = &Lease{
		Range:   KeyRange{Start: splitKey, End: parent.Range.End},
		NodeID:  rightNode,
		Epoch:   childEpoch,
		Granted: now,
		TTL:     parent.TTL,
		valid:   true,
	}

	g.leases = append(g.leases[:parentIdx], g.leases[parentIdx+1:]...)
	g.leases = append(g.leases, left, right)
	g.sortLeases()
	return left, right, nil
}

// Route resolves key to its valid lease and the lease-holding node.
func (g *Gateway) Route(key string) (*Lease, *Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, l := range g.leases {
		if l.valid && l.Range.Contains(key) {
			if !l.Valid(time.Now()) {
				return nil, nil, fmt.Errorf("lease %s held by %s has expired", l.Range, l.NodeID)
			}
			return l, g.nodes[l.NodeID], nil
		}
	}
	return nil, nil, fmt.Errorf("no lease covers key %q", key)
}

// Query routes a client query for key to the lease holder and returns the
// node's response.
func (g *Gateway) Query(key string) (string, error) {
	_, node, err := g.Route(key)
	if err != nil {
		return "", err
	}
	return node.HandleQuery(key), nil
}

// Leases returns a snapshot copy of all leases (valid and revoked).
func (g *Gateway) Leases() []Lease {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Lease, len(g.leases))
	for i, l := range g.leases {
		out[i] = *l
	}
	return out
}

func (g *Gateway) sortLeases() {
	sort.Slice(g.leases, func(i, j int) bool {
		return g.leases[i].Range.Start < g.leases[j].Range.Start
	})
}
