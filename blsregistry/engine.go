// Package blsregistry implements BLS-based validator registration with
// proofs of possession and aggregate-signature verification for a consensus
// committee.
//
// The package is scheme-agnostic: pairing evaluation and hash-to-curve are
// not implemented here. The consensus node supplies them by injecting an
// Engine, typically backed by a BLS12-381 library using the
// minimal-pubkey-size scheme (public keys in G1, signatures in G2).
//
// Security properties enforced by this package:
//   - A public key is admitted only together with a valid proof of
//     possession, which prevents rogue-key attacks in which an attacker
//     derives a key from other validators' public keys and forges
//     aggregate signatures on their behalf.
//   - Proofs of possession and ordinary message signatures use distinct
//     domain separation tags, so they can never be interchanged.
//   - The point at infinity is never accepted as a public key.
//   - Re-registering the same public key is a no-op.
//   - Aggregate public keys are summed only from registered keys selected
//     by a bitmap whose length equals the validator set size, and the
//     selected-signer count must reach the quorum threshold.
//   - Distinct-message aggregation accepts pairwise distinct messages only.
package blsregistry

// Domain separation tags. They are intentionally different: a proof of
// possession is a signature over a public-key encoding in its own hash-to-
// curve domain and is never interchangeable with an ordinary message
// signature.
const (
	// DomainSignature is the DST for ordinary consensus message signatures.
	DomainSignature = "Q0817-BLS-SIG-v1"
	// DomainProofOfPossession is the DST for key-proof signatures. It must
	// never equal DomainSignature.
	DomainProofOfPossession = "Q0817-BLS-POP-v1"
)

// G1Point is an element of the G1 group, the public-key group in the
// minimal-pubkey-size BLS scheme.
//
// All arithmetic methods are functional: they return a new point and must
// not mutate their receivers or arguments. Implementations must be safe for
// concurrent use.
type G1Point interface {
	// IsIdentity reports whether the point is the point at infinity.
	IsIdentity() bool
	// Add returns the sum of the two points.
	Add(other G1Point) G1Point
	// Neg returns the additive inverse of the point.
	Neg() G1Point
	// Mul returns the scalar multiplication point * scalar.
	Mul(scalar Scalar) G1Point
	// Encode returns the canonical byte encoding of the point.
	Encode() []byte
	// Equal reports whether the two points are the same group element.
	Equal(other G1Point) bool
}

// G2Point is an element of the G2 group, the signature group.
//
// All arithmetic methods are functional and safe for concurrent use.
type G2Point interface {
	// IsIdentity reports whether the point is the point at infinity.
	IsIdentity() bool
	// Add returns the sum of the two points.
	Add(other G2Point) G2Point
	// Neg returns the additive inverse of the point.
	Neg() G2Point
	// Mul returns the scalar multiplication point * scalar.
	Mul(scalar Scalar) G2Point
	// Equal reports whether the two points are the same group element.
	Equal(other G2Point) bool
}

// Scalar is an element of the scalar field shared by G1 and G2.
type Scalar interface {
	// IsZero reports whether the scalar is zero.
	IsZero() bool
}

// Engine abstracts the cryptographic primitives injected by the caller. A
// production implementation is backed by a real pairing library; the mock
// implementation in the tests only simulates the algebraic relationships and
// must never be used outside tests.
type Engine interface {
	// G1Generator returns the fixed generator of G1.
	G1Generator() G1Point
	// G2Generator returns the fixed generator of G2.
	G2Generator() G2Point
	// HashToG2 maps message to a G2 point under the given domain separation
	// tag. Distinct dst values must produce independent hash-to-curve
	// instances.
	HashToG2(dst string, message []byte) G2Point
	// CheckPairing verifies the multi-pairing equation
	//
	//	e(g1[0], g2[0]) * ... * e(g1[n-1], g2[n-1]) == 1_GT.
	//
	// It returns false when the two slices have different lengths.
	CheckPairing(g1 []G1Point, g2 []G2Point) bool
}
