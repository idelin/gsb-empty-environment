package leaseread

import "errors"

var (
	errUnknownFollower = errors.New("leaseread: unknown follower")
	errFollowerDown    = errors.New("leaseread: follower unreachable")
	errStaleTerm       = errors.New("leaseread: stale term")

	// ErrNoQuorum is returned when the leader cannot confirm a quorum of
	// followers, so a strongly consistent read cannot be authorized.
	ErrNoQuorum = errors.New("leaseread: cannot confirm quorum")
)
