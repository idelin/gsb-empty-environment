package lease

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

const defaultWriteTimeout = 2 * time.Second

// Node is a service instance that can hold a lease and serve business writes.
type Node struct {
	ID           string
	coord        *Coordinator
	writeTimeout time.Duration

	mu     sync.Mutex
	cred   *Lease
	frozen bool
	state  chan struct{} // closed and replaced on every local state change

	writes int64

	acceptHook func() error // test hook, runs at the start of AcceptHandover
}

func NewNode(id string, coord *Coordinator) *Node {
	return &Node{
		ID:           id,
		coord:        coord,
		writeTimeout: defaultWriteTimeout,
		state:        make(chan struct{}),
	}
}

// SetWriteTimeout bounds how long a write waits out a handover freeze.
func (n *Node) SetWriteTimeout(d time.Duration) { n.writeTimeout = d }

func (n *Node) broadcastLocked() {
	close(n.state)
	n.state = make(chan struct{})
}

// Acquire takes the lease and makes the node leader.
func (n *Node) Acquire(leaseID string, ttl time.Duration) error {
	l, err := n.coord.Acquire(leaseID, n.ID, ttl)
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.cred = l
	n.broadcastLocked()
	n.mu.Unlock()
	return nil
}

// Renew dynamically extends the held lease with a (possibly different) TTL.
func (n *Node) Renew(ttl time.Duration) error {
	n.mu.Lock()
	cred := n.cred
	n.mu.Unlock()
	if cred == nil {
		return ErrNotLeader
	}
	l, err := n.coord.Renew(cred.ID, n.ID, cred.Term, ttl)
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.cred = l
	n.mu.Unlock()
	return nil
}

// PutContext attaches business context to the lease (inherited on handover).
func (n *Node) PutContext(key, value string) error {
	n.mu.Lock()
	cred := n.cred
	n.mu.Unlock()
	if cred == nil {
		return ErrNotLeader
	}
	return n.coord.PutContext(cred.ID, n.ID, cred.Term, key, value)
}

// Write performs a business write. While the node is frozen for handover the
// write waits (up to writeTimeout) instead of failing, so a fast handover is
// invisible to callers.
func (n *Node) Write(ctx context.Context, payload string) error {
	deadline := time.Now().Add(n.writeTimeout)
	for {
		n.mu.Lock()
		frozen := n.frozen
		cred := n.cred
		state := n.state
		n.mu.Unlock()

		if !frozen {
			if cred == nil {
				return ErrNotLeader
			}
			if err := n.coord.Validate(cred.ID, n.ID, cred.Term); err != nil {
				if errors.Is(err, ErrInvalidTerm) || errors.Is(err, ErrLeaseExpired) || errors.Is(err, ErrNotHolder) {
					n.stepDown(cred)
				}
				return err
			}
			atomic.AddInt64(&n.writes, 1)
			return nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return ErrWriteTimeout
		}
		timer := time.NewTimer(remaining)
		select {
		case <-state:
			timer.Stop()
		case <-timer.C:
			return ErrWriteTimeout
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

// InitiateHandover freezes local writes, then transfers the lease credential
// and remaining context to the candidate. It returns the switchover latency:
// the wall-clock time during which this node could not serve writes.
func (n *Node) InitiateHandover(candidate *Node) (time.Duration, error) {
	n.mu.Lock()
	if n.cred == nil {
		n.mu.Unlock()
		return 0, ErrNotLeader
	}
	n.frozen = true // freeze local writes first
	cred := *n.cred
	n.broadcastLocked()
	n.mu.Unlock()

	start := time.Now()
	pkg, err := n.coord.PrepareHandover(cred.ID, n.ID, cred.Term, candidate.ID)
	if err != nil {
		n.unfreeze()
		return 0, err
	}
	if err := candidate.AcceptHandover(pkg); err != nil {
		_ = n.coord.AbortHandover(cred.ID, n.ID, cred.Term)
		n.unfreeze()
		return 0, err
	}

	n.mu.Lock()
	n.cred = nil
	n.frozen = false
	n.broadcastLocked()
	n.mu.Unlock()
	return time.Since(start), nil
}

func (n *Node) unfreeze() {
	n.mu.Lock()
	n.frozen = false
	n.broadcastLocked()
	n.mu.Unlock()
}

// AcceptHandover receives the handover package and instantly takes over the
// leader role with the remaining lease context.
func (n *Node) AcceptHandover(pkg *HandoverPackage) error {
	if n.acceptHook != nil {
		if err := n.acceptHook(); err != nil {
			return err
		}
	}
	l, err := n.coord.CompleteHandover(pkg, n.ID)
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.cred = l
	n.frozen = false
	n.broadcastLocked()
	n.mu.Unlock()
	return nil
}

// Shutdown releases the lease without naming a successor.
func (n *Node) Shutdown() error {
	n.mu.Lock()
	cred := n.cred
	n.mu.Unlock()
	if cred == nil {
		return nil
	}
	err := n.coord.Release(cred.ID, n.ID, cred.Term)
	n.mu.Lock()
	n.cred = nil
	n.broadcastLocked()
	n.mu.Unlock()
	return err
}

func (n *Node) stepDown(cred *Lease) {
	n.mu.Lock()
	if n.cred != nil && n.cred.Term == cred.Term {
		n.cred = nil
		n.broadcastLocked()
	}
	n.mu.Unlock()
}

func (n *Node) IsLeader() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.cred != nil
}

// Credential returns a copy of the node's current lease credential, or nil.
func (n *Node) Credential() *Lease {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cred == nil {
		return nil
	}
	cp := *n.cred
	return &cp
}

// Writes returns the number of successful business writes served.
func (n *Node) Writes() int64 { return atomic.LoadInt64(&n.writes) }

// Router directs business writes to whichever node currently leads.
type Router struct {
	nodes []*Node
}

func NewRouter(nodes ...*Node) *Router { return &Router{nodes: nodes} }

// Write tries the current leader; leadership changes mid-flight are retried
// against the new leader so callers observe no errors during handover.
func (r *Router) Write(ctx context.Context, payload string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		leader := r.leader()
		if leader == nil {
			return ErrNoLeader
		}
		err := leader.Write(ctx, payload)
		switch {
		case err == nil:
			return nil
		case errors.Is(err, ErrNotLeader), errors.Is(err, ErrNotHolder),
			errors.Is(err, ErrLeaseFrozen), errors.Is(err, ErrInvalidTerm):
			continue // leadership moved; find the new leader
		default:
			return err
		}
	}
}

func (r *Router) leader() *Node {
	for _, n := range r.nodes {
		if n.IsLeader() {
			return n
		}
	}
	return nil
}
