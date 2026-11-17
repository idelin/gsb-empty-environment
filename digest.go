package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const sha256Prefix = "sha256:"

// SHA256Digest returns the canonical OCI sha256 digest of data.
func SHA256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return sha256Prefix + hex.EncodeToString(sum[:])
}

func parseSHA256Digest(value string) (string, error) {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	hexPart := lower
	if strings.HasPrefix(lower, sha256Prefix) {
		hexPart = lower[len(sha256Prefix):]
	}
	if len(hexPart) != sha256.Size*2 {
		return "", fmt.Errorf("%w: sha256 digest must contain 64 hex characters", ErrInvalidDescriptor)
	}
	if _, err := hex.DecodeString(hexPart); err != nil {
		return "", fmt.Errorf("%w: malformed sha256 digest: %v", ErrInvalidDescriptor, err)
	}
	return sha256Prefix + hexPart, nil
}

func digestHex(digest string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(digest)), sha256Prefix)
}

func sameDigest(a, b string) bool {
	ca, err := parseSHA256Digest(a)
	if err != nil {
		return false
	}
	cb, err := parseSHA256Digest(b)
	if err != nil {
		return false
	}
	return ca == cb
}
