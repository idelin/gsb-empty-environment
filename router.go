package gracefulleave

import (
	"context"
	"errors"
	"sync/atomic"
)

// ErrNoReadyMembers is returned when the ready routing pool is empty.
var ErrNoReadyMembers = errors.New("gracefulleave: no ready members")

// Router routes outbound calls to the ready pool (alive members only)
// using round-robin. Members in StateLeaving never receive new calls,
// while calls already in flight are allowed to finish.
type Router struct {
	membership *Membership
	transport  *Transport
	rr         atomic.Uint64
}

// NewRouter creates a router over the given membership view and transport.
func NewRouter(ms *Membership, t *Transport) *Router {
	return &Router{membership: ms, transport: t}
}

// Call routes one request to a ready member.
func (r *Router) Call(ctx context.Context, req any) (any, error) {
	ready := r.membership.Ready()
	if len(ready) == 0 {
		return nil, ErrNoReadyMembers
	}
	i := r.rr.Add(1) - 1
	m := ready[i%uint64(len(ready))]

	m.beginCall()
	defer m.endCall()
	return r.transport.Call(ctx, m.ID, req)
}
