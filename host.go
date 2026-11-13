package netsec

import (
	"errors"
	"fmt"
	"math/big"
)

// Group is the small interface a trusted host must provide for one of the
// pairing groups. Points are opaque to this package; the host owns their
// representation, arithmetic and canonical encoding.
type Group[P any] interface {
	Generator() P
	Identity() P
	Add(a, b P) P
	Sub(a, b P) P
	Neg(a P) P
	ScalarMul(a P, scalar *big.Int) P
	Equal(a, b P) bool
	IsIdentity(a P) bool
	Encode(a P) ([]byte, error)
	Decode(encoded []byte) (P, error)
	// Validate checks membership and any host-specific point invariants. It
	// must be called after Decode and before the point is used.
	Validate(a P) error
}

// Pairing exposes a bilinear pairing and equality for its target values. The
// package only ever compares e(a, b) with e(c, d); the host may implement the
// comparison in the most efficient way available to it.
type Pairing[G1, G2, GT any] interface {
	Pair(a G1, b G2) (GT, error)
	Equal(a, b GT) bool
}

// Host supplies all cryptographic primitives and the canonical codecs for
// scalars and group elements. The host is trusted; request data is not.
type Host[G1, G2, GT any] interface {
	// PrimeModulus returns the prime q of the scalar field.
	PrimeModulus() *big.Int
	ScalarEncode(v *big.Int) ([]byte, error)
	ScalarDecode(encoded []byte) (*big.Int, error)
	G1Group() Group[G1]
	G2Group() Group[G2]
	Pairing() Pairing[G1, G2, GT]
}

// SRS is the power-of-tau setup owned by the trusted host. It is deliberately
// separate from EncodedRequest: a verifier request cannot supply or replace it.
// The slices are copied by NewSRS.
type SRS[G1, G2 any] struct {
	identity string
	g1       []G1
	g2       []G2
}

// NewSRS constructs trusted setup data. identity must be a stable, host-chosen
// identifier for the setup (for example a setup-generation transcript digest).
// g1Powers and g2Powers contain tau^0..tau^d for the same secret tau.
func NewSRS[G1, G2 any](identity string, g1Powers []G1, g2Powers []G2) (*SRS[G1, G2], error) {
	if identity == "" {
		return nil, fmt.Errorf("%w: empty SRS identity", ErrInvalidSRS)
	}
	if len(g1Powers) == 0 || len(g2Powers) == 0 {
		return nil, fmt.Errorf("%w: empty power sequence", ErrInvalidSRS)
	}

	g1 := make([]G1, len(g1Powers))
	copy(g1, g1Powers)
	g2 := make([]G2, len(g2Powers))
	copy(g2, g2Powers)

	return &SRS[G1, G2]{identity: identity, g1: g1, g2: g2}, nil
}

// Identity returns the host-supplied SRS identifier.
func (s *SRS[G1, G2]) Identity() string {
	if s == nil {
		return ""
	}
	return s.identity
}

// MaxDegree is the largest polynomial degree supported by both power
// sequences. A setup with d+1 powers in each group supports degree d.
func (s *SRS[G1, G2]) MaxDegree() int {
	if s == nil {
		return -1
	}
	n := len(s.g1)
	if len(s.g2) < n {
		n = len(s.g2)
	}
	return n - 1
}

var (
	// ErrInvalidHost reports an unusable host primitive or generator.
	ErrInvalidHost = errors.New("netsec: invalid host")
	// ErrInvalidSRS reports malformed trusted setup data.
	ErrInvalidSRS = errors.New("netsec: invalid SRS")
	// ErrEmptyRequest reports a request with no evaluations.
	ErrEmptyRequest = errors.New("netsec: empty request")
	// ErrTooManyEvaluations reports more than 32 input evaluations.
	ErrTooManyEvaluations = errors.New("netsec: too many evaluations")
	// ErrScalarOutOfRange reports a scalar outside [0, q-1].
	ErrScalarOutOfRange = errors.New("netsec: scalar out of range")
	// ErrNonCanonicalScalar reports scalar bytes that are not the host's
	// canonical encoding of the decoded value.
	ErrNonCanonicalScalar = errors.New("netsec: non-canonical scalar encoding")
	// ErrInvalidPoint reports undecodable or invalid group data.
	ErrInvalidPoint = errors.New("netsec: invalid group point")
	// ErrNonCanonicalPoint reports point bytes that are not the host's
	// canonical encoding of the decoded point.
	ErrNonCanonicalPoint = errors.New("netsec: non-canonical point encoding")
	// ErrConflictingEvaluation reports the same z with different y values.
	ErrConflictingEvaluation = errors.New("netsec: conflicting evaluation for repeated z")
	// ErrSRSDegree reports a setup whose degree bound is below the request's
	// distinct evaluation count.
	ErrSRSDegree = errors.New("netsec: SRS degree insufficient")
	// ErrDegenerateCommitment reports CommitG2(vanishing polynomial) == identity.
	ErrDegenerateCommitment = errors.New("netsec: degenerate vanishing-polynomial commitment")
	// ErrProofRejected reports a canonical request whose pairing equation fails.
	ErrProofRejected = errors.New("netsec: proof rejected")
	// ErrField reports a failure in finite-field polynomial arithmetic.
	ErrField = errors.New("netsec: finite-field arithmetic failure")
)
