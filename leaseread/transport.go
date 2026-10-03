package leaseread

import (
	"context"
	"sync/atomic"
	"time"
)

// Transport is the network layer between the leader and its followers.
// Every Heartbeat call is exactly one network round trip.
type Transport interface {
	// Heartbeat sends a leader heartbeat (term + commitIndex) to a follower
	// and waits for its ack. An error means the follower is unreachable or
	// rejected the term.
	Heartbeat(ctx context.Context, followerID string, term, commitIndex uint64) error
}

// CountingTransport wraps a Transport and counts every network round trip,
// which lets tests and the demo prove the "zero network overhead" property.
type CountingTransport struct {
	inner  Transport
	rounds int64
}

func NewCountingTransport(inner Transport) *CountingTransport {
	return &CountingTransport{inner: inner}
}

func (t *CountingTransport) Heartbeat(ctx context.Context, followerID string, term, commitIndex uint64) error {
	atomic.AddInt64(&t.rounds, 1)
	return t.inner.Heartbeat(ctx, followerID, term, commitIndex)
}

// Rounds returns the total number of network round trips performed.
func (t *CountingTransport) Rounds() int64 {
	return atomic.LoadInt64(&t.rounds)
}

// Follower simulates a replica that acks heartbeats unless it is down or the
// leader's term is stale.
type Follower struct {
	ID          string
	currentTerm uint64
	down        int32 // atomic: 0 = up, 1 = down
	latency     time.Duration
}

func NewFollower(id string, latency time.Duration) *Follower {
	return &Follower{ID: id, latency: latency}
}

func (f *Follower) SetDown(down bool) {
	if down {
		atomic.StoreInt32(&f.down, 1)
	} else {
		atomic.StoreInt32(&f.down, 0)
	}
}

// InMemoryTransport delivers heartbeats directly to in-process followers.
// It stands in for RPC in tests and the demo while preserving real
// semantics: per-call latency, failures, and term checks.
type InMemoryTransport struct {
	followers map[string]*Follower
}

func NewInMemoryTransport(followers ...*Follower) *InMemoryTransport {
	m := make(map[string]*Follower, len(followers))
	for _, f := range followers {
		m[f.ID] = f
	}
	return &InMemoryTransport{followers: m}
}

func (t *InMemoryTransport) Heartbeat(ctx context.Context, followerID string, term, commitIndex uint64) error {
	f, ok := t.followers[followerID]
	if !ok {
		return errUnknownFollower
	}
	if atomic.LoadInt32(&f.down) == 1 {
		return errFollowerDown
	}
	if f.latency > 0 {
		select {
		case <-time.After(f.latency):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if term < f.currentTerm {
		return errStaleTerm
	}
	f.currentTerm = term
	return nil
}
