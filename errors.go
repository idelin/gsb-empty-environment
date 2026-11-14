package netsec

import "errors"

var (
	// ErrNoPolicy means that no policy is configured for the tenant view.
	ErrNoPolicy = errors.New("negcache: no policy for view")
	// ErrPolicyMismatch covers a changed policy generation or policy binding.
	ErrPolicyMismatch = errors.New("negcache: policy binding mismatch")
	// ErrKeyGeneration means that a proof was produced by another key generation.
	ErrKeyGeneration = errors.New("negcache: key generation mismatch or revoked key")
	// ErrViewMismatch means that the proof belongs to a different tenant view.
	ErrViewMismatch = errors.New("negcache: tenant view mismatch")
	// ErrProofExpired means that the proof or one of its signatures is outside
	// its validity interval, or that the proof's TTL elapsed.
	ErrProofExpired = errors.New("negcache: proof expired")
	// ErrClockRollback means that a clock rollback invalidated cached state.
	ErrClockRollback = errors.New("negcache: clock rollback")
	// ErrMalformed means that a name, bitmap, NSEC3 hash, or record is invalid.
	ErrMalformed = errors.New("negcache: malformed denial proof")
	// ErrUnproven means that the supplied denial RRsets do not prove either
	// NXDOMAIN or NODATA for the question.
	ErrUnproven = errors.New("negcache: negative answer not proven")
	// ErrOptOut means that opt-out coverage is insufficient for the requested
	// conclusion. In particular, opt-out must not turn a possible unsigned
	// delegation into NXDOMAIN.
	ErrOptOut = errors.New("negcache: nsec3 opt-out does not prove the answer")
	// ErrDelegation means that the question is at or below a delegation and the
	// parent zone cannot author a negative answer for it.
	ErrDelegation = errors.New("negcache: question is at or below a delegation")
	// ErrWildcard means that a wildcard exists, so NXDOMAIN is not justified.
	ErrWildcard = errors.New("negcache: wildcard owner exists")
	// ErrPositive means that the proof shows the requested RRset can exist.
	ErrPositive = errors.New("negcache: proof does not establish absence")
	// ErrNoUpstream means that the service has no upstream for a tenant view.
	ErrNoUpstream = errors.New("negcache: no upstream for view")
	// ErrNoUpstreamResponse means that the per-view upstream returned no
	// negative proof.
	ErrNoUpstreamResponse = errors.New("negcache: upstream returned no negative proof")
	// ErrQuestionMismatch means that the upstream proof answers another question.
	ErrQuestionMismatch = errors.New("negcache: proof question mismatch")
	// ErrInvalidPolicy means that a policy is internally inconsistent.
	ErrInvalidPolicy = errors.New("negcache: invalid policy")
	// ErrAmbiguous means that more than one independently valid cache entry
	// could answer the question with different parameter sets.
	ErrAmbiguous = errors.New("negcache: ambiguous cache entry")
)
