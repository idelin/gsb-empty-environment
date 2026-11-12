package luks2

import (
	"crypto/sha256"
	"strings"
)

// naiveChecksum hashes the tampered region without zeroing the checksum
// field. It models an attacker who ignores the LUKS2 hashing rule; the
// resulting digest must never verify.
func naiveChecksum(raw []byte) [32]byte {
	return sha256.Sum256(raw[:testHeaderSize])
}

func replaceAreaOffset(jsonText, old, replacement string) string {
	return strings.Replace(jsonText, old, replacement, 1)
}
