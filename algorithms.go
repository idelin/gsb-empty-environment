package netsec

import (
	"fmt"
	"strconv"
	"strings"
)

// HashAlgorithm identifies a hash algorithm permitted by the CCA binding
// policy. The string form is the one used in CCA claims, for example
// "sha-256".
type HashAlgorithm string

const (
	SHA256 HashAlgorithm = "sha-256"
	SHA384 HashAlgorithm = "sha-384"
	SHA512 HashAlgorithm = "sha-512"
)

// DefaultAllowedHashAlgorithms is the default allow-list. SHA-1 and MD5 are
// deliberately excluded.
var DefaultAllowedHashAlgorithms = []HashAlgorithm{SHA256, SHA384, SHA512}

// DigestSize returns the measurement size in bytes for the algorithm.
func (a HashAlgorithm) DigestSize() int {
	switch a {
	case SHA256:
		return 32
	case SHA384:
		return 48
	case SHA512:
		return 64
	default:
		return 0
	}
}

func (a HashAlgorithm) String() string {
	return string(a)
}

// ParseHashAlgorithm accepts the textual CCA claim values ("sha-256",
// "sha-384", "sha-512") as well as equivalent common spellings and COSE/PSA
// algorithm identifiers.
func ParseHashAlgorithm(v any) (HashAlgorithm, error) {
	switch x := v.(type) {
	case HashAlgorithm:
		if x.DigestSize() == 0 {
			return "", fmt.Errorf("%w: %q", ErrAlgorithmNotAllowed, string(x))
		}
		return x, nil
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		s = strings.ReplaceAll(s, "_", "-")
		switch s {
		case "sha-256", "sha256", "sha2-256", "sha-2-256",
			"2.16.840.1.101.3.4.2.1":
			return SHA256, nil
		case "sha-384", "sha384", "sha2-384", "sha-2-384",
			"2.16.840.1.101.3.4.2.2":
			return SHA384, nil
		case "sha-512", "sha512", "sha2-512", "sha-2-512",
			"2.16.840.1.101.3.4.2.3":
			return SHA512, nil
		}
	case int:
		return parseNumericHashAlgorithm(int64(x))
	case int8:
		return parseNumericHashAlgorithm(int64(x))
	case int16:
		return parseNumericHashAlgorithm(int64(x))
	case int32:
		return parseNumericHashAlgorithm(int64(x))
	case int64:
		return parseNumericHashAlgorithm(x)
	case uint:
		return parseNumericHashAlgorithm(int64(x))
	case uint8:
		return parseNumericHashAlgorithm(int64(x))
	case uint16:
		return parseNumericHashAlgorithm(int64(x))
	case uint32:
		return parseNumericHashAlgorithm(int64(x))
	case uint64:
		if x > 1<<63-1 {
			break
		}
		return parseNumericHashAlgorithm(int64(x))
	case float64:
		if x == float64(int64(x)) {
			return parseNumericHashAlgorithm(int64(x))
		}
	}

	return "", fmt.Errorf("%w: %v", ErrAlgorithmNotAllowed, v)
}

func parseNumericHashAlgorithm(v int64) (HashAlgorithm, error) {
	switch v {
	case -16, 0x02000009: // COSE SHA-256; PSA_ALG_SHA_256
		return SHA256, nil
	case -43, 0x0200000A: // COSE SHA-384; PSA_ALG_SHA_384
		return SHA384, nil
	case -44, 0x0200000B: // COSE SHA-512; PSA_ALG_SHA_512
		return SHA512, nil
	default:
		return "", fmt.Errorf("%w: numeric id %d", ErrAlgorithmNotAllowed, v)
	}
}

func parseHashAlgorithmList(values []string) ([]HashAlgorithm, error) {
	out := make([]HashAlgorithm, 0, len(values))
	for _, s := range values {
		a, err := ParseHashAlgorithm(s)
		if err != nil {
			return nil, err
		}
		out = appendUniqueAlgorithm(out, a)
	}
	return out, nil
}

func appendUniqueAlgorithm(dst []HashAlgorithm, a HashAlgorithm) []HashAlgorithm {
	for _, existing := range dst {
		if existing == a {
			return dst
		}
	}
	return append(dst, a)
}

func canonicalAlgorithmName(v any) (string, error) {
	// This helper is used only for diagnostics and option maps.
	switch x := v.(type) {
	case string:
		a, err := ParseHashAlgorithm(x)
		if err != nil {
			return "", err
		}
		return string(a), nil
	case HashAlgorithm:
		return string(x), nil
	default:
		return "", fmt.Errorf("%w: %v", ErrInvalidArgument, v)
	}
}

func numericString(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
