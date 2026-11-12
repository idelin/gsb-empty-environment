package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// AlgorithmSHA256 is the algorithm identifier used by IteratingVerifier.
const AlgorithmSHA256 = "sha256"

// IteratingVerifier is a minimal Verifier that derives a key by iterating
// SHA-256 cost times. It gives hosts a working default; production hosts
// should inject a memory-hard KDF (argon2id, scrypt, PBKDF2) instead.
type IteratingVerifier struct{}

// Derive implements Verifier.
func (IteratingVerifier) Derive(algorithm string, password, salt []byte, cost int) ([]byte, error) {
	if algorithm != AlgorithmSHA256 {
		return nil, fmt.Errorf("netsec: unsupported algorithm %q", algorithm)
	}
	if cost < 1 {
		return nil, fmt.Errorf("netsec: invalid cost %d", cost)
	}
	var costBuf [8]byte
	binary.BigEndian.PutUint64(costBuf[:], uint64(cost))
	h := sha256.New()
	h.Write(password)
	h.Write(salt)
	h.Write(costBuf[:])
	sum := h.Sum(nil)
	for i := 1; i < cost; i++ {
		next := sha256.Sum256(sum)
		Zero(sum)
		sum = next[:]
	}
	out := make([]byte, len(sum))
	copy(out, sum)
	Zero(sum)
	return out, nil
}
