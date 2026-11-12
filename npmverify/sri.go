package npmverify

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"hash"
	"strings"
)

// algorithmStrength ranks SRI hash algorithms from weakest to strongest.
// sha1 is retained only for parsing completeness; a digest with a stronger
// sibling is always preferred. Unknown algorithms have strength 0 and are
// ignored during strongest-selection.
var algorithmStrength = map[string]int{
	"md5":    1,
	"sha1":   2,
	"sha256": 3,
	"sha384": 4,
	"sha512": 5,
}

// Integrity is one parsed SRI token: "algorithm-base64digest".
type Integrity struct {
	Algorithm string
	Digest    string // standard base64, without padding normalization issues
}

// ParseIntegrity parses an SRI string containing one or more whitespace
// separated hash expressions, e.g. "sha512-A...== sha256-B...=". Every
// token must be well-formed and use a recognised algorithm; callers that
// want to ignore unsupported algorithms should filter before calling.
func ParseIntegrity(sri string) ([]Integrity, error) {
	if strings.TrimSpace(sri) == "" {
		return nil, ErrMalformedIntegrity
	}

	var out []Integrity
	for _, token := range strings.Fields(sri) {
		algo, encodedDigest, ok := strings.Cut(token, "-")
		if !ok || algo == "" || encodedDigest == "" {
			return nil, ErrMalformedIntegrity
		}
		if algorithmStrength[algo] == 0 {
			return nil, ErrMalformedIntegrity
		}
		digest, err := base64.StdEncoding.DecodeString(encodedDigest)
		if err != nil {
			return nil, ErrMalformedIntegrity
		}
		if len(digest) == 0 {
			return nil, ErrMalformedIntegrity
		}
		out = append(out, Integrity{Algorithm: algo, Digest: encodedDigest})
	}

	if len(out) == 0 {
		return nil, ErrMalformedIntegrity
	}
	return out, nil
}

// StrongestIntegrity returns the digest with the highest-strength algorithm.
// Ties are resolved by first occurrence, matching the order in the metadata.
func StrongestIntegrity(entries []Integrity) Integrity {
	best := entries[0]
	for _, entry := range entries[1:] {
		if algorithmStrength[entry.Algorithm] > algorithmStrength[best.Algorithm] {
			best = entry
		}
	}
	return best
}

func newHash(algorithm string) (hash.Hash, bool) {
	switch algorithm {
	case "md5":
		return md5.New(), true
	case "sha1":
		return sha1.New(), true
	case "sha256":
		return sha256.New(), true
	case "sha384":
		return sha512.New384(), true
	case "sha512":
		return sha512.New(), true
	default:
		return nil, false
	}
}

// ComputeIntegrity builds a canonical single-algorithm SRI string for data
// using the named algorithm. It is primarily useful for tests and for
// proxies that publish their own integrity metadata.
func ComputeIntegrity(algorithm string, data []byte) (string, error) {
	h, ok := newHash(algorithm)
	if !ok {
		return "", ErrMalformedIntegrity
	}
	h.Write(data)
	return algorithm + "-" + base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}

// verifyIntegrity recomputes the strongest declared digest over the tarball
// and compares it against the metadata in constant time.
func verifyIntegrity(declared string, tarball []byte) (Integrity, error) {
	entries, err := ParseIntegrity(declared)
	if err != nil {
		return Integrity{}, err
	}

	best := StrongestIntegrity(entries)
	hasher, ok := newHash(best.Algorithm)
	if !ok {
		return Integrity{}, ErrMalformedIntegrity
	}
	hasher.Write(tarball)
	actual := base64.StdEncoding.EncodeToString(hasher.Sum(nil))

	if len(actual) != len(best.Digest) ||
		subtle.ConstantTimeCompare([]byte(actual), []byte(best.Digest)) != 1 {
		return Integrity{}, ErrIntegrityMismatch
	}
	return best, nil
}
