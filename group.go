package netsec

import (
	"errors"
	"math/big"
)

// MaxN is the largest supported vector length. N must be a power of two
// in the range [1, MaxN].
const MaxN = 1024

var (
	// ErrInvalidN is returned when the vector length is not a power of two
	// within [1, MaxN].
	ErrInvalidN = errors.New("netsec: n must be a power of two in [1, 1024]")
	// ErrParams is returned for inconsistent or nil protocol parameters.
	ErrParams = errors.New("netsec: inconsistent parameters")
	// ErrIdentityGenerator is returned when a fixed generator is the
	// identity element; generators must be non-identity.
	ErrIdentityGenerator = errors.New("netsec: generator must not be the identity")
	// ErrWitnessLength is returned when the witness vectors do not match
	// the parameter vector length.
	ErrWitnessLength = errors.New("netsec: witness length mismatch")
	// ErrRoundCount is returned when the number of L/R pairs in a proof
	// does not equal log2(n).
	ErrRoundCount = errors.New("netsec: proof round count does not match log2(n)")
	// ErrEncoding is returned for malformed, truncated, or non-canonical
	// proof encodings.
	ErrEncoding = errors.New("netsec: malformed or non-canonical encoding")
	// ErrVerification is returned when the final single-element commitment
	// equation does not hold.
	ErrVerification = errors.New("netsec: inner product proof verification failed")
)

// Scalar is an element of the prime-order scalar field Z_q. Implementations
// must be immutable: every operation returns a new value.
type Scalar interface {
	Add(Scalar) Scalar
	Sub(Scalar) Scalar
	Mul(Scalar) Scalar
	Neg() Scalar
	// Inv returns the multiplicative inverse. Calling Inv on zero is a
	// programming error and may panic.
	Inv() Scalar
	IsZero() bool
	Equal(Scalar) bool
	// Bytes returns the canonical fixed-length encoding.
	Bytes() []byte
}

// Point is an element of the prime-order group. Implementations must be
// immutable: every operation returns a new value.
type Point interface {
	Add(Point) Point
	Neg() Point
	ScalarMul(Scalar) Point
	IsIdentity() bool
	Equal(Point) bool
	// Bytes returns the canonical fixed-length encoding. The identity
	// element has a canonical encoding and must be representable.
	Bytes() []byte
}

// Group is the trusted adapter contract: it supplies the group order q,
// the group law, and canonical (de)serialization of scalars and points.
// The adapter is responsible for rejecting non-canonical encodings.
type Group interface {
	// Order returns the prime group order q.
	Order() *big.Int
	// ScalarSize is the canonical scalar encoding length in bytes.
	ScalarSize() int
	// PointSize is the canonical point encoding length in bytes.
	PointSize() int
	// Identity returns the group identity element.
	Identity() Point
	// ScalarFromBytes decodes a canonical scalar, rejecting any
	// non-canonical input (wrong length, value >= q).
	ScalarFromBytes([]byte) (Scalar, error)
	// ScalarFromBig reduces an integer modulo q.
	ScalarFromBig(*big.Int) Scalar
	// PointFromBytes decodes a canonical point, rejecting any
	// non-canonical input (wrong length, invalid encoding, off-curve).
	PointFromBytes([]byte) (Point, error)
}

// Params are the host-fixed public parameters. G, H and U are fixed
// non-identity generators; Context binds the proof transcript to the
// host application.
type Params struct {
	G       []Point
	H       []Point
	U       Point
	Context []byte
}

// validate checks structural consistency and the non-identity generator
// requirement.
func (p *Params) validate() error {
	if p == nil || p.U == nil {
		return ErrParams
	}
	n := len(p.G)
	if err := checkN(n); err != nil {
		return err
	}
	if len(p.H) != n {
		return ErrParams
	}
	for i := 0; i < n; i++ {
		if p.G[i] == nil || p.H[i] == nil {
			return ErrParams
		}
		if p.G[i].IsIdentity() || p.H[i].IsIdentity() {
			return ErrIdentityGenerator
		}
	}
	if p.U.IsIdentity() {
		return ErrIdentityGenerator
	}
	return nil
}

func checkN(n int) error {
	if n < 1 || n > MaxN || n&(n-1) != 0 {
		return ErrInvalidN
	}
	return nil
}

// roundsFor returns log2(n) for a valid power-of-two n.
func roundsFor(n int) int {
	r := 0
	for n > 1 {
		n >>= 1
		r++
	}
	return r
}
