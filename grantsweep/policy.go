package grantsweep

// Policy selects which weakness a model exhibits. The zero value is the
// reference design; turning a single flag on reproduces one class of flawed
// grant implementation.
type Policy struct {
	// IgnoreSubset allows Use calls that omit required subset keys.
	IgnoreSubset bool
	// IgnoreExtraContext allows extra context keys under equality constraints.
	IgnoreExtraContext bool
	// IgnoreCaller allows a token to be replayed by any principal.
	IgnoreCaller bool
	// IgnoreOperation lets a token authorize operations outside the grant.
	IgnoreOperation bool
	// IgnoreKeyVersion lets a grant extend automatically to a rotated version.
	IgnoreKeyVersion bool
	// GrantIDLookup models cache/state lookup keyed by GrantID alone: the
	// token's generation and digest binding are ignored, so a lookup can be
	// confused across principals/operations/resources after state changes.
	GrantIDLookup bool
	// CrossClientPending lets any caller use a Pending token instead of
	// binding it to the Create requestID and creator.
	CrossClientPending bool
	// NonAtomicRetire splits Retire into status/generation steps so an
	// interleaving Use can observe Retired status with the old generation.
	NonAtomicRetire bool
}

// Name returns a stable identifier for the selected weakness.
func (p Policy) Name() string {
	switch {
	case p.IgnoreSubset:
		return "IgnoreSubset"
	case p.IgnoreExtraContext:
		return "IgnoreExtraContext"
	case p.IgnoreCaller:
		return "IgnoreCaller"
	case p.IgnoreOperation:
		return "IgnoreOperation"
	case p.IgnoreKeyVersion:
		return "IgnoreKeyVersion"
	case p.GrantIDLookup:
		return "GrantIDLookup"
	case p.CrossClientPending:
		return "CrossClientPending"
	case p.NonAtomicRetire:
		return "NonAtomicRetire"
	default:
		return "Reference"
	}
}

// AllPolicies returns one weak Policy per weakness plus the reference.
func AllPolicies() []Policy {
	return []Policy{
		{},
		{IgnoreSubset: true},
		{IgnoreExtraContext: true},
		{IgnoreCaller: true},
		{IgnoreOperation: true},
		{IgnoreKeyVersion: true},
		{GrantIDLookup: true},
		{CrossClientPending: true},
		{NonAtomicRetire: true},
	}
}
