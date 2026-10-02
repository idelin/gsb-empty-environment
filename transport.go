package gracefulleave

import (
	"context"
	"errors"
	"sync"
)

// ErrNodeUnreachable is returned immediately (never after a timeout) when a
// call targets a node whose routing entry and endpoint are already gone.
var ErrNodeUnreachable = errors.New("gracefulleave: node unreachable")

// Transport is an in-memory message bus standing in for the RPC layer.
// It delivers LeaveIntent broadcasts and point-to-point calls.
type Transport struct {
	mu    sync.RWMutex
	nodes map[string]*Node
}

// NewTransport creates an empty transport.
func NewTransport() *Transport {
	return &Transport{nodes: make(map[string]*Node)}
}

// Register attaches a node endpoint.
func (t *Transport) Register(n *Node) {
	t.mu.Lock()
	t.nodes[n.ID] = n
	t.mu.Unlock()
}

// Deregister detaches a node endpoint (physical shutdown).
func (t *Transport) Deregister(id string) {
	t.mu.Lock()
	delete(t.nodes, id)
	t.mu.Unlock()
}

// BroadcastLeaveIntent delivers the leave intent to every peer except the
// sender. Delivery is synchronous so the sender knows the intent was
// received before it starts its own drain.
func (t *Transport) BroadcastLeaveIntent(from string) {
	t.mu.RLock()
	peers := make([]*Node, 0, len(t.nodes))
	for id, n := range t.nodes {
		if id != from {
			peers = append(peers, n)
		}
	}
	t.mu.RUnlock()
	for _, p := range peers {
		p.HandleLeaveIntent(from)
	}
}

// Call performs a point-to-point request. A missing endpoint fails fast
// with ErrNodeUnreachable instead of surfacing as an RPC timeout.
func (t *Transport) Call(ctx context.Context, targetID string, req any) (any, error) {
	t.mu.RLock()
	n, ok := t.nodes[targetID]
	t.mu.RUnlock()
	if !ok {
		return nil, ErrNodeUnreachable
	}
	return n.serve(ctx, req)
}
