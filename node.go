package configlease

import (
	"fmt"
	"sync"
)

// Node is a worker node. It only serves traffic while it holds an unexpired
// lease; once the lease lapses (e.g. the node is cut off from the center)
// it fences itself off instead of acting on stale config.
type Node struct {
	name   string
	center *Center
	clock  Clock

	mu    sync.Mutex
	cfg   Config
	lease Lease
	bound bool
}

// NewNode creates a node and binds it to the center with an initial lease.
func NewNode(name string, center *Center, clock Clock) *Node {
	n := &Node{name: name, center: center, clock: clock}
	n.Renew()
	return n
}

// Renew refreshes the node's lease against the center. If the old lease was
// revoked (config changed) or expired, the center returns the latest config
// together with a fresh lease, and the node swaps to it atomically.
func (n *Node) Renew() {
	id := ""
	n.mu.Lock()
	if n.bound {
		id = n.lease.ID
	}
	n.mu.Unlock()

	cfg, lease := n.center.Renew(id)

	n.mu.Lock()
	n.cfg = cfg
	n.lease = lease
	n.bound = true
	n.mu.Unlock()
}

// Serve handles one request, but only while the node holds a valid lease.
// The fencing check is purely local: even a node that cannot reach the
// center stops serving the moment its lease expires.
func (n *Node) Serve(query string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.bound || !n.clock.Now().Before(n.lease.ExpiresAt) {
		return "", ErrLeaseExpired
	}
	return fmt.Sprintf("node=%s version=%d dbpassword=%s query=%q",
		n.name, n.cfg.Version, n.cfg.DBPassword, query), nil
}

// Config returns the config the node is currently bound to.
func (n *Node) Config() Config {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cfg
}

// Lease returns the node's current lease credential.
func (n *Node) Lease() Lease {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lease
}
