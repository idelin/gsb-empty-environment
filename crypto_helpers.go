package taskpkg

import (
	"crypto/sha256"
	"crypto/subtle"
)

func sha256Sum(data []byte) [sha256.Size]byte {
	return sha256.Sum256(data)
}

func constantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
