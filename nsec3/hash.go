package nsec3

import (
	"crypto/sha1"
)

// Hash algorithm identifiers. RFC 5155 defines only SHA-1 (value 1).
const (
	HashSHA1 = uint8(1)
	hashLen  = 20
)

// MaxIterations is the largest accepted NSEC3 iterations value. The field
// in the wire format is 16 bits (up to 65535), but operational guidance
// (RFC 9276) mandates rejection of values above 100 to keep verification
// cheap and to prevent amplification attacks.
const MaxIterations = 100

// HashName computes the NSEC3 hash of name (RFC 5155 section 5):
//
//	H(0) = SHA-1(wire_name || salt)
//	H(k) = SHA-1(H(k-1) || salt)   for k = 1..iterations
//
// The wire name is the canonical lowercase uncompressed encoding. Only
// algorithm 1 (SHA-1) is defined; unknown algorithms and iteration counts
// above MaxIterations are rejected.
func HashName(algorithm uint8, iterations uint16, salt []byte, name string) ([]byte, error) {
	wire, err := WireName(name)
	if err != nil {
		return nil, err
	}
	if algorithm != HashSHA1 {
		return nil, ErrUnknownAlgorithm
	}
	if int(iterations) > MaxIterations {
		return nil, ErrIterationsTooLarge
	}
	return iterateHash(wire, iterations, salt), nil
}

// HashNameChain behaves like HashName but additionally returns every
// intermediate value H(0)..H(iterations). It exists mainly for conformance
// testing against the worked vectors in RFC 5155 appendix A.
func HashNameChain(algorithm uint8, iterations uint16, salt []byte, name string) ([][]byte, error) {
	wire, err := WireName(name)
	if err != nil {
		return nil, err
	}
	if algorithm != HashSHA1 {
		return nil, ErrUnknownAlgorithm
	}
	if int(iterations) > MaxIterations {
		return nil, ErrIterationsTooLarge
	}
	chain := make([][]byte, 0, int(iterations)+1)
	h := sha1.Sum(append(append([]byte{}, wire...), salt...))
	chain = append(chain, append([]byte{}, h[:]...))
	for i := uint16(0); i < iterations; i++ {
		h = sha1.Sum(append(append([]byte{}, h[:]...), salt...))
		chain = append(chain, append([]byte{}, h[:]...))
	}
	return chain, nil
}

// iterateHash runs the salted SHA-1 iteration loop starting from the
// canonical wire-form name.
func iterateHash(wire []byte, iterations uint16, salt []byte) []byte {
	buf := make([]byte, 0, hashLen+len(salt))
	buf = append(buf, wire...)
	buf = append(buf, salt...)
	digest := sha1.Sum(buf)
	for i := uint16(0); i < iterations; i++ {
		buf = buf[:0]
		buf = append(buf, digest[:]...)
		buf = append(buf, salt...)
		digest = sha1.Sum(buf)
	}
	return digest[:]
}
