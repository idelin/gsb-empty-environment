package netsec

import "errors"

var (
	// ErrInvalidModulus means N is not a usable odd modulus.
	ErrInvalidModulus = errors.New("netsec: invalid modulus")
	// ErrInvalidGenerator means g is not a canonical unit modulo N.
	ErrInvalidGenerator = errors.New("netsec: invalid generator")
	// ErrInvalidElement means a set element or proof identifier is not a
	// 32-bit prime.
	ErrInvalidElement = errors.New("netsec: invalid element")
	// ErrDuplicateElement means the same prime occurs more than once in a set.
	ErrDuplicateElement = errors.New("netsec: duplicate element")
	// ErrSetTooLarge means a set has more than MaxSetSize members.
	ErrSetTooLarge = errors.New("netsec: set too large")
	// ErrNotMember means an element required to be present is absent. It is
	// also returned when deleting an absent element.
	ErrNotMember = errors.New("netsec: element not in set")
	// ErrMember means an operation requires x to be absent, but x is a member.
	ErrMember = errors.New("netsec: element is in set")

	// ErrIdentityMismatch is the broad identity-binding failure. More specific
	// errors below are wrapped alongside it.
	ErrIdentityMismatch   = errors.New("netsec: proof identity mismatch")
	ErrSetIDMismatch      = errors.New("netsec: set ID mismatch")
	ErrGenerationMismatch = errors.New("netsec: generation mismatch")
	ErrRootDigestMismatch = errors.New("netsec: root digest mismatch")
	ErrGenerationOverflow = errors.New("netsec: generation overflow")
	ErrStaleProof         = errors.New("netsec: stale proof")

	// ErrNonCanonical means an integer or digest was not encoded in its one
	// accepted canonical representation.
	ErrNonCanonical = errors.New("netsec: non-canonical encoding")
	// ErrNotUnit means a group element is not coprime to N (or is outside
	// [1, N-1]).
	ErrNotUnit = errors.New("netsec: element is not a unit")
	// ErrInvalidRange means an integer is structurally valid but outside the
	// range required by the protocol.
	ErrInvalidRange = errors.New("netsec: value out of range")
	// ErrInvalidProof means the proof is malformed or its claimed algebraic
	// equation does not hold.
	ErrInvalidProof = errors.New("netsec: invalid proof")
	// ErrEquationFailed means a well-formed proof failed its accumulator
	// equation.
	ErrEquationFailed = errors.New("netsec: proof equation failed")
)

func invalidProof(err error) error {
	if err == nil {
		err = ErrInvalidProof
	}
	return errors.Join(ErrInvalidProof, err)
}

func identityError(specific error, detail string) error {
	return errors.Join(ErrIdentityMismatch, ErrStaleProof, specific, errors.New(detail))
}
