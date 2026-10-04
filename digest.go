package dedup

import (
	"crypto/sha256"
	"hash/fnv"
)

// WeakDigest is a cheap hash used only for bucketing candidate blocks.
// Collisions are expected and resolved by the strong digest and content.
type WeakDigest uint64

// StrongDigest is a cryptographic hash used to confirm block identity
// together with the content length and a full byte comparison.
type StrongDigest [32]byte

// DefaultWeakHash is FNV-1a 64, cheap and good enough for bucketing.
func DefaultWeakHash(data []byte) WeakDigest {
	h := fnv.New64a()
	h.Write(data)
	return WeakDigest(h.Sum64())
}

// DefaultStrongHash is SHA-256.
func DefaultStrongHash(data []byte) StrongDigest {
	return StrongDigest(sha256.Sum256(data))
}
