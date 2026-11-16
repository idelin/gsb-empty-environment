package netsec

import (
	"context"
	"fmt"
	"time"
)

// ReplicaOption configures a Replica.
type ReplicaOption func(*Replica)

// WithWaitTimeout sets how long a replica waits for replication to catch up
// before giving up (and forwarding, if a forwarder is configured).
func WithWaitTimeout(d time.Duration) ReplicaOption {
	return func(r *Replica) { r.waitTimeout = d }
}

// WithForwarder installs a function that serves the check on another node,
// typically the leader. A replica forwards only when its own snapshot is too
// stale and waiting does not close the gap.
func WithForwarder(f func(context.Context, CheckRequest) (CheckResult, error)) ReplicaOption {
	return func(r *Replica) { r.forward = f }
}

// Replica is a read-only follower. It may only answer a check once it has
// applied (via the replication log) a snapshot at least as fresh as the
// request requires. It never answers from an older snapshot.
type Replica struct {
	leader      *Store
	secret      []byte
	waitTimeout time.Duration
	forward     func(context.Context, CheckRequest) (CheckResult, error)

	notify chan struct{}

	applied   uint64
	tuples    map[tupleKey]bool
	aclFloor  map[string]uint64
	contentTS map[string]uint64
}

// NewReplica creates a read replica of leader. The replica starts at applied
// timestamp zero and only advances as entries are applied, so it can
// deliberately be kept behind the leader for lag testing.
func NewReplica(leader *Store, opts ...ReplicaOption) *Replica {
	r := &Replica{
		leader:      leader,
		secret:      append([]byte(nil), leader.secret...),
		waitTimeout: 50 * time.Millisecond,
		notify:      make(chan struct{}, 1),
		tuples:      make(map[tupleKey]bool),
		aclFloor:    make(map[string]uint64),
		contentTS:   make(map[string]uint64),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Applied returns the newest zookie timestamp the replica has applied.
func (r *Replica) Applied() uint64 {
	r.leader.mu.Lock()
	defer r.leader.mu.Unlock()
	return r.applied
}

// ApplyThrough pulls and applies leader log entries up to and including
// timestamp through. Repeated or already-applied entries are ignored, so it
// is safe to call as the streaming replication step.
func (r *Replica) ApplyThrough(through uint64) {
	entries := r.leader.EntriesSince(r.applied)

	r.leader.mu.Lock()
	defer r.leader.mu.Unlock()

	for _, entry := range entries {
		if entry.Timestamp > through {
			break
		}
		r.applyLocked(entry)
	}

	select {
	case r.notify <- struct{}{}:
	default:
	}
}

// CatchUp applies every entry the leader currently has.
func (r *Replica) CatchUp() {
	r.leader.mu.Lock()
	target := r.leader.clock
	r.leader.mu.Unlock()
	r.ApplyThrough(target)
}

func (r *Replica) applyLocked(entry LogEntry) {
	if entry.Timestamp <= r.applied {
		return
	}

	switch entry.Kind {
	case entryACL:
		key := tupleKey{
			object:   entry.Object,
			relation: entry.Tuple.Relation,
			subject:  entry.Tuple.Subject,
		}
		if entry.Tuple.Present {
			r.tuples[key] = true
		} else {
			delete(r.tuples, key)
		}
		if entry.Timestamp > r.aclFloor[entry.Object] {
			r.aclFloor[entry.Object] = entry.Timestamp
		}
	case entryContent:
		if entry.Timestamp > r.contentTS[entry.Object] {
			r.contentTS[entry.Object] = entry.Timestamp
		}
	}

	r.applied = entry.Timestamp
}

// localFloorLocked returns the freshness floor known from applied entries.
func (r *Replica) localFloorLocked(object string) uint64 {
	floor := r.aclFloor[object]
	if content := r.contentTS[object]; content > floor {
		floor = content
	}
	return floor
}

// Check serves a read against the replica while enforcing snapshot
// consistency:
//   - A malformed token is rejected.
//   - A token ahead of the leader's latest zookie is rejected as "future".
//   - The required freshness is the maximum of any explicit token and the
//     resource's current floor (latest ACL or content write). The floor is
//     read from the leader because a lagging replica cannot know newer ones.
//   - If the replica has not applied that timestamp, it waits; if waiting
//     fails it forwards to the leader when configured, otherwise returns
//     ErrStaleReplica. It never answers from an older snapshot.
//
// When no token is supplied, the newest applied snapshot is used but never
// earlier than the resource's latest ACL change.
func (r *Replica) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	explicit, err := r.parseBound(req.Token)
	if err != nil {
		return CheckResult{}, err
	}

	// Authoritative per-resource floor, including writes not yet replicated.
	resourceFloor := r.leader.ResourceFloor(req.Object)
	required := explicit
	if resourceFloor > required {
		required = resourceFloor
	}

	if err := r.waitForFreshness(ctx, required); err != nil {
		if r.forward != nil {
			return r.forward(ctx, req)
		}
		return CheckResult{}, err
	}

	r.leader.mu.Lock()
	defer r.leader.mu.Unlock()

	allowed := r.tuples[tupleKey{object: req.Object, relation: req.Relation, subject: req.Subject}]
	return CheckResult{
		Allowed:  allowed,
		ServedAt: mintZookie(r.secret, r.applied),
	}, nil
}

// parseBound validates an at_least_as_fresh token, rejecting malformed tokens
// and timestamps beyond the leader's latest zookie. An empty token means no
// explicit bound and yields zero.
func (r *Replica) parseBound(token string) (uint64, error) {
	if token == "" {
		return 0, nil
	}

	zk, err := ParseZookie(r.secret, token)
	if err != nil {
		return 0, err
	}

	latest := r.leader.Latest().Timestamp()
	if zk.Timestamp() > latest {
		return 0, fmt.Errorf("%w: %d > leader %d",
			ErrFutureZookie, zk.Timestamp(), latest)
	}
	return zk.Timestamp(), nil
}

// waitForFreshness blocks until the replica has applied at least required, or
// the context expires / the wait timeout elapses. It does not pull entries
// itself: replication is delivered through ApplyThrough, which keeps the
// replica's lag explicitly observable and testable.
func (r *Replica) waitForFreshness(ctx context.Context, required uint64) error {
	if r.Applied() >= required {
		return nil
	}

	deadline := time.NewTimer(r.waitTimeout)
	defer deadline.Stop()

	for {
		if r.Applied() >= required {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("%w: applied %d < required %d",
				ErrStaleReplica, r.Applied(), required)
		case <-r.notify:
		}
	}
}
