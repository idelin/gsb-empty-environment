package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidDigest indicates that a value is not a supported canonical OCI
	// content digest.
	ErrInvalidDigest = errors.New("netsec: invalid OCI digest")

	// ErrUnsupportedDigestAlgorithm indicates that a digest uses an algorithm
	// the package cannot verify.
	ErrUnsupportedDigestAlgorithm = errors.New("netsec: unsupported digest algorithm")
)

type digestValue struct {
	algorithm string
	encoded   string
}

func (d digestValue) String() string {
	return d.algorithm + ":" + d.encoded
}

func parseDigest(value string) (digestValue, error) {
	algorithm, encoded, ok := strings.Cut(value, ":")
	if !ok || algorithm == "" || encoded == "" {
		return digestValue{}, fmt.Errorf("%w: %q", ErrInvalidDigest, value)
	}
	if !validAlgorithm(algorithm) {
		return digestValue{}, fmt.Errorf("%w: %q", ErrInvalidDigest, value)
	}

	var expectedLen int
	switch algorithm {
	case "sha256":
		expectedLen = sha256.Size * 2
	case "sha512":
		expectedLen = sha512.Size * 2
	default:
		return digestValue{}, fmt.Errorf("%w: %q", ErrUnsupportedDigestAlgorithm, algorithm)
	}
	if len(encoded) != expectedLen || !isLowerHex(encoded) {
		return digestValue{}, fmt.Errorf("%w: %q", ErrInvalidDigest, value)
	}
	return digestValue{algorithm: algorithm, encoded: encoded}, nil
}

func validAlgorithm(algorithm string) bool {
	if algorithm == "" {
		return false
	}
	for i, r := range algorithm {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case i > 0 && (r == '+' || r == '.' || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

func isLowerHex(value string) bool {
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func canonicalDigest(value string) (string, error) {
	parsed, err := parseDigest(value)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// ComputeDigest returns the canonical OCI digest of content for algorithm.
// SHA-256 and SHA-512 are supported.
func ComputeDigest(algorithm string, content []byte) (string, error) {
	switch algorithm {
	case "sha256":
		sum := sha256.Sum256(content)
		return "sha256:" + hex.EncodeToString(sum[:]), nil
	case "sha512":
		sum := sha512.Sum512(content)
		return "sha512:" + hex.EncodeToString(sum[:]), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedDigestAlgorithm, algorithm)
	}
}

// VerifyDigest checks content against an expected canonical OCI digest.
func VerifyDigest(expected string, content []byte) error {
	parsed, err := parseDigest(expected)
	if err != nil {
		return err
	}
	actual, err := ComputeDigest(parsed.algorithm, content)
	if err != nil {
		return err
	}
	if actual != parsed.String() {
		return fmt.Errorf("%w: expected %s, got %s", ErrDigestMismatch, expected, actual)
	}
	return nil
}

// ErrDigestMismatch indicates that fetched content does not hash to the
// descriptor digest.
var ErrDigestMismatch = errors.New("netsec: content digest mismatch")
