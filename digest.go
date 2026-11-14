package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"
)

// Digest is a canonical content digest in "<algorithm>:<hex>" form.
type Digest string

var digestEncodedSizes = map[string]int{
	"sha256": sha256.Size,
	"sha384": sha512.Size384,
	"sha512": sha512.Size,
}

// ParseDigest validates and canonicalizes an OCI content digest.
func ParseDigest(value string) (Digest, error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty value", ErrInvalidDigest)
	}

	algorithm, encoded, found := strings.Cut(value, ":")
	if !found || algorithm == "" || encoded == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidDigest, value)
	}
	if algorithm != strings.ToLower(algorithm) {
		return "", fmt.Errorf("%w: algorithm must be lowercase: %q", ErrInvalidDigest, value)
	}

	encodedSize, supported := digestEncodedSizes[algorithm]
	if !supported {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedDigestAlgorithm, algorithm)
	}

	decoded, decodeErr := hex.DecodeString(encoded)
	if decodeErr != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrInvalidDigest, value, decodeErr)
	}
	if len(decoded) != encodedSize {
		return "", fmt.Errorf(
			"%w: algorithm %s requires %d bytes, got %d",
			ErrInvalidDigest,
			algorithm,
			encodedSize,
			len(decoded),
		)
	}

	return Digest(algorithm + ":" + strings.ToLower(encoded)), nil
}

// Algorithm returns the digest algorithm. It is empty for an invalid digest.
func (digest Digest) Algorithm() string {
	algorithm, _, found := strings.Cut(string(digest), ":")
	if !found {
		return ""
	}
	return algorithm
}

// String returns the canonical string form.
func (digest Digest) String() string {
	return string(digest)
}

// ComputeDigest hashes content with a supported algorithm.
func ComputeDigest(algorithm string, content []byte) (Digest, error) {
	if algorithm != strings.ToLower(algorithm) {
		return "", fmt.Errorf("%w: algorithm must be lowercase: %q", ErrInvalidDigest, algorithm)
	}
	if _, supported := digestEncodedSizes[algorithm]; !supported {
		return "", fmt.Errorf("%w: %q", ErrUnsupportedDigestAlgorithm, algorithm)
	}

	hasher := hashForAlgorithm(algorithm)
	if _, writeErr := hasher.Write(content); writeErr != nil {
		return "", fmt.Errorf("hash content: %w", writeErr)
	}
	return Digest(algorithm + ":" + hex.EncodeToString(hasher.Sum(nil))), nil
}

func verifyContentDigest(expected Digest, content []byte) error {
	parsed, parseErr := ParseDigest(expected.String())
	if parseErr != nil {
		return parseErr
	}

	actual, computeErr := ComputeDigest(parsed.Algorithm(), content)
	if computeErr != nil {
		return computeErr
	}
	if actual != parsed {
		return fmt.Errorf("%w: expected %s, calculated %s", ErrDigestMismatch, parsed, actual)
	}
	return nil
}

func verifyAlgorithm(expected Digest, chainAlgorithm string) error {
	parsed, parseErr := ParseDigest(expected.String())
	if parseErr != nil {
		return parseErr
	}
	if chainAlgorithm != "" && parsed.Algorithm() != chainAlgorithm {
		return fmt.Errorf(
			"%w: chain uses %s, descriptor uses %s",
			ErrDigestAlgorithmMismatch,
			chainAlgorithm,
			parsed.Algorithm(),
		)
	}
	return nil
}

func hashForAlgorithm(algorithm string) hash.Hash {
	switch algorithm {
	case "sha256":
		return sha256.New()
	case "sha384":
		return sha512.New384()
	case "sha512":
		return sha512.New()
	default:
		panic("hashForAlgorithm called with an unsupported algorithm")
	}
}
