package gracefulleave

import (
	"context"
	"sync"
	"time"
)

// Handler processes an application-level request.
type Handler func(ctx context.Context, req any) (any, error)

// Node is a cluster member. It owns a membership view (its routing table),
// a router for outbound traffic, and an endpoint serving inbound RPCs.
type Node struct {
	ID   string
	Addr string

	transport  *Transport
	membership *Membership
	router     *Router
	handler    Handler
	quiescent  time.Duration

	mu       sync.Mutex
	serving  bool // false once the endpoint is physically shut down
	inflight sync.WaitGroup
	leave    sync.Once
	shutdown sync.Once
}

// NewNode creates and registers a node. The handler may be nil, in which
// case requests are echoed back.
func NewNode(id, addr string, t *Transport, quiescent time.Duration, h Handler) *Node {
	if h == nil {
		h = func(ctx context.Context, req any) (any, error) { return req, nil }
	}
	n := &Node{
		ID:         id,
		Addr:       addr,
		transport:  t,
		membership: NewMembership(quiescent),
		handler:    h,
		quiescent:  quiescent,
		serving:    true,
	}
	n.router = NewRouter(n.membership, t)
	t.Register(n)
	return n
}

// Membership returns the node's routing-table view.
func (n *Node) Membership() *Membership { return n.membership }

// Router returns the node's outbound router.
func (n *Node) Router() *Router { return n.router }

// Leave initiates the graceful-leave protocol:
//  1. broadcast a LeaveIntent to all peers (they mark this node Leaving and
//     stop routing new traffic to it),
//  2. keep serving in-flight requests during the quiescent period,
//  3. shut the endpoint down once the quiescent timer expires.
//
// Leave is idempotent.
func (n *Node) Leave() {
	n.leave.Do(func() {
		n.transport.BroadcastLeaveIntent(n.ID)
		time.AfterFunc(n.quiescent, n.Shutdown)
	})
}

// HandleLeaveIntent processes a leave intent received from a peer: the peer
// is moved to StateLeaving (out of the ready pool) and its routing entry is
// scheduled for physical removal after the quiescent period.
func (n *Node) HandleLeaveIntent(from string) {
	n.membership.MarkLeaving(from)
}

// serve handles an inbound RPC. Once the endpoint is shut down it fails
// fast with ErrNodeUnreachable rather than hanging into a timeout.
func (n *Node) serve(ctx context.Context, req any) (any, error) {
	n.mu.Lock()
	if !n.serving {
		n.mu.Unlock()
		return nil, ErrNodeUnreachable
	}
	n.inflight.Add(1)
	n.mu.Unlock()
	defer n.inflight.Done()
	return n.handler(ctx, req)
}

// Shutdown physically destroys the endpoint: it stops accepting new
// requests, waits for in-flight requests to drain, and deregisters from
// the transport. It is idempotent and safe to call concurrently.
func (n *Node) Shutdown() {
	n.shutdown.Do(func() {
		n.mu.Lock()
		n.serving = false
		n.mu.Unlock()
		n.inflight.Wait()
		n.transport.Deregister(n.ID)
	})
}
