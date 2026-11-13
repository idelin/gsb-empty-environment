// Package netsec implements the fixed RSA-accumulator algebra used by the
// credential revocation service in this exercise.
//
// An accumulator is identified by a caller-supplied set ID, a generation, and
// a digest of the accumulator's root. Proofs carry that identity, but a
// verifier always takes the trusted modulus, generator, root, set ID, and
// generation from its own configuration. It never derives a root from a proof.
// Identity fields are equality-checked, not signed; callers must obtain the
// expected identity from trusted host state.
//
// The algebra in this package is the fixed construction described by the
// exercise: for a product P of distinct 32-bit primes, A = g^P mod N. A
// membership proof for x is W = g^(P/x), checked with W^x = A. A
// non-membership proof uses a*x + b*P = 1 and checks D^x*A^b = g, with
// 0 <= b < x.
//
// A successful verification is a statement about the supplied proof, the
// trusted parameters, and that exact (set ID, generation, root digest)
// snapshot. It is not a cacheable answer about a later revocation state. A
// caller must verify again after an add or delete advances the generation.
//
// The package deliberately makes no claim about the factoring resistance of
// any particular modulus. In particular, small moduli used by tests are
// reference parameters and are not suitable for deployment.
package netsec
