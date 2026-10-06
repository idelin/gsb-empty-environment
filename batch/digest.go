package batch

import (
	"crypto/sha256"
	"encoding/hex"
)

// digestOf returns the content digest frozen into manifests and checkpoints.
func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
