package referrerfilter

import (
	"crypto/sha256"
	"crypto/sha512"
	"encoding"
	"encoding/hex"
	"fmt"
	"hash"
)

// Digest is a validated "algorithm:hex" content digest as defined by the
// OCI image specification.
type Digest struct {
	algorithm string
	encoded   string
}

// supported digest algorithms. The fallback tag scheme only exists for sha256,
// but sha512 descriptors remain addressable through the Referrers API.
const (
	algSHA256 = "sha256"
	algSHA512 = "sha512"
)

// ParseDigest validates and normalizes a digest string.
func ParseDigest(value string) (Digest, error) {
	algorithm, encoded, ok := splitDigest(value)
	if !ok {
		return Digest{}, fmt.Errorf("referrerfilter: malformed digest %q", value)
	}
	return Digest{algorithm: algorithm, encoded: encoded}, nil
}

// String renders the canonical "algorithm:hex" form.
func (d Digest) String() string {
	return d.algorithm + ":" + d.encoded
}

// Algorithm returns the digest algorithm, for example "sha256".
func (d Digest) Algorithm() string {
	return d.algorithm
}

// Hex returns the lowercase hexadecimal checksum.
func (d Digest) Hex() string {
	return d.encoded
}

func splitDigest(value string) (algorithm, encoded string, ok bool) {
	for i := 0; i < len(value); i++ {
		if value[i] != ':' {
			continue
		}
		algorithm = value[:i]
		encoded = value[i+1:]
		break
	}
	if algorithm == "" || encoded == "" {
		return "", "", false
	}
	if !validAlgorithm(algorithm) {
		return "", "", false
	}
	if !validHex(algorithm, encoded) {
		return "", "", false
	}
	return algorithm, encoded, true
}

func validAlgorithm(algorithm string) bool {
	return algorithm == algSHA256 || algorithm == algSHA512
}

func validHex(algorithm, encoded string) bool {
	decoded, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	switch algorithm {
	case algSHA256:
		return len(decoded) == sha256.Size
	case algSHA512:
		return len(decoded) == sha512.Size
	default:
		return false
	}
}

// newHasher builds a hasher for a supported algorithm.
func newHasher(algorithm string) (hash.Hash, error) {
	switch algorithm {
	case algSHA256:
		return sha256.New(), nil
	case algSHA512:
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("referrerfilter: unsupported digest algorithm %q", algorithm)
	}
}

// digestBytes computes the digest of raw manifest bytes.
func digestBytes(algorithm string, content []byte) (Digest, error) {
	hasher, err := newHasher(algorithm)
	if err != nil {
		return Digest{}, err
	}
	if _, err := hasher.Write(content); err != nil {
		return Digest{}, err
	}
	return Digest{
		algorithm: algorithm,
		encoded:   hex.EncodeToString(hasher.Sum(nil)),
	}, nil
}

var (
	_ encoding.TextMarshaler   = Digest{}
	_ encoding.TextUnmarshaler = (*Digest)(nil)
)

// MarshalText emits the canonical digest form.
func (d Digest) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

// UnmarshalText rejects malformed digests so untrusted JSON cannot inject an
// unvalidated digest into the pipeline.
func (d *Digest) UnmarshalText(text []byte) error {
	parsed, err := ParseDigest(string(text))
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}
