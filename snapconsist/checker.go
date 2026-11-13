package snapconsist

import (
	"context"
)

// Checker is the check-interface entry point. It binds a local read replica to
// the leader and enforces every freshness rule before any allow/deny answer
// is returned.
type Checker struct {
	leader  *Leader
	replica *Replica
	forward bool
}

// CheckerOption customizes check behavior.
type CheckerOption func(*Checker)

// WithForwarding makes a lagging replica forward checks to the leader instead
// of waiting for its own snapshot to catch up. The leader evaluates at the
// required revision, so the answer still never uses an older snapshot.
func WithForwarding() CheckerOption {
	return func(c *Checker) { c.forward = true }
}

// NewChecker builds a Checker served by replica with leader as the freshness
// authority.
func NewChecker(leader *Leader, replica *Replica, opts ...CheckerOption) *Checker {
	c := &Checker{leader: leader, replica: replica}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// CheckRequest is one permission check. AtLeastAsFresh is the optional
// at_least_as_fresh zookie; an empty string means the default freshness rule.
type CheckRequest struct {
	Object         string
	Relation       string
	Subject        string
	AtLeastAsFresh string
}

// CheckResult reports the decision plus the snapshot zookie that produced it.
// Waited is true when the replica had to catch up before answering locally;
// Forwarded is true when the leader answered on behalf of a lagging replica.
type CheckResult struct {
	Allowed    bool
	UsedZookie Zookie
	Waited     bool
	Forwarded  bool
}

// Check resolves one request under the snapshot-consistency rules:
//
//   - A malformed AtLeastAsFresh token is rejected with ErrInvalidZookie.
//   - A token newer than the leader is rejected with ErrFutureZookie.
//   - The effective snapshot revision is at least the resource's latest ACL
//     and content floors, so a default check cannot predate a revocation or a
//     content write.
//   - If the replica has not applied that revision, it waits for replication
//     or forwards to the leader; it never answers from an older snapshot.
func (c *Checker) Check(ctx context.Context, req CheckRequest) (CheckResult, error) {
	tuple := Tuple{Object: req.Object, Relation: req.Relation, Subject: req.Subject}

	var requested uint64
	if req.AtLeastAsFresh != "" {
		zookie, err := ParseZookie(req.AtLeastAsFresh)
		if err != nil {
			return CheckResult{}, err
		}
		requested = zookie.Revision()
	}

	// The leader is the authority for per-resource floors: consulting it here
	// guarantees a default check cannot use a snapshot older than the
	// resource's most recent ACL change (the new-enemy / revocation guard) or
	// its most recent content write.
	floor := c.leader.ResourceFloor(req.Object).Revision()
	required := requested
	if floor > required {
		required = floor
	}

	// A token above the leader's head can never become fresh.
	if required > c.leader.Latest().Revision() {
		return CheckResult{}, ErrFutureZookie
	}

	if c.replica.AppliedRevision() >= required {
		return CheckResult{
			Allowed:    c.replica.checkLocal(tuple),
			UsedZookie: zookieForRevision(c.replica.AppliedRevision()),
		}, nil
	}

	if c.forward {
		allowed, err := c.leader.Check(ctx, required, tuple)
		if err != nil {
			return CheckResult{}, err
		}
		return CheckResult{
			Allowed:    allowed,
			UsedZookie: zookieForRevision(required),
			Forwarded:  true,
		}, nil
	}

	if err := c.replica.waitApplied(ctx, required); err != nil {
		return CheckResult{}, ErrStaleWait
	}
	return CheckResult{
		Allowed:    c.replica.checkLocal(tuple),
		UsedZookie: zookieForRevision(c.replica.AppliedRevision()),
		Waited:     true,
	}, nil
}
