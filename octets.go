package netsec

import (
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"reflect"
	"strings"
)

// octetCandidates returns all faithful interpretations of a claim value that
// may represent binary data. Raw CBOR tokens use byte strings; JSON-based
// fixtures commonly use base64 or hexadecimal strings, so both forms are
// supported without guessing a single lossy representation.
func octetCandidates(v any) ([][]byte, error) {
	switch x := v.(type) {
	case []byte:
		return [][]byte{append([]byte(nil), x...)}, nil
	case string:
		return stringOctetCandidates(x), nil
	case fmt.Stringer:
		return stringOctetCandidates(x.String()), nil
	}

	rv := reflect.ValueOf(v)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil, fmt.Errorf("%w: nil byte value", ErrInvalidClaim)
		}
		rv = rv.Elem()
	}
	if rv.IsValid() {
		if rv.CanInterface() {
			if b, ok := rv.Interface().(interface{ Bytes() []byte }); ok {
				return [][]byte{append([]byte(nil), b.Bytes()...)}, nil
			}
		}
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return [][]byte{append([]byte(nil), rv.Bytes()...)}, nil
		}
		if rv.Kind() == reflect.Array && rv.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, rv.Len())
			for i := 0; i < rv.Len(); i++ {
				b[i] = byte(rv.Index(i).Uint())
			}
			return [][]byte{b}, nil
		}
	}
	return nil, fmt.Errorf("%w: expected byte string, got %T", ErrInvalidClaim, v)
}

func stringOctetCandidates(s string) [][]byte {
	var out [][]byte
	add := func(b []byte) {
		if b == nil {
			return
		}
		for _, existing := range out {
			if subtle.ConstantTimeCompare(existing, b) == 1 {
				return
			}
		}
		out = append(out, append([]byte(nil), b...))
	}

	lower := strings.ToLower(s)

	// Raw text is always a candidate; this is what a CBOR text-string claim
	// literally contains.
	add([]byte(s))
	if strings.Contains(s, "-----BEGIN") {
		if block, _ := pem.Decode([]byte(s)); block != nil {
			add(block.Bytes)
		}
	}

	switch {
	case strings.HasPrefix(lower, "base64:"):
		if b, ok := decodeBase64(strings.TrimSpace(s[len("base64:"):])); ok {
			add(b)
		}
	case strings.HasPrefix(lower, "b64:"):
		if b, ok := decodeBase64(strings.TrimSpace(s[len("b64:"):])); ok {
			add(b)
		}
	case strings.HasPrefix(lower, "hex:"):
		if b, err := hex.DecodeString(strings.TrimSpace(s[len("hex:"):])); err == nil {
			add(b)
		}
	case strings.HasPrefix(lower, "0x"):
		if b, err := hex.DecodeString(strings.TrimSpace(s[2:])); err == nil {
			add(b)
		}
	default:
		if b, ok := decodeBase64(s); ok {
			add(b)
		}
		if isHexString(s) {
			if b, err := hex.DecodeString(s); err == nil {
				add(b)
			}
		}
	}

	return out
}

func decodeBase64(s string) ([]byte, bool) {
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, enc := range encodings {
		decoded, err := enc.DecodeString(s)
		if err != nil {
			continue
		}
		reencoded := enc.EncodeToString(decoded)
		if reencoded == s || strings.TrimRight(reencoded, "=") == strings.TrimRight(s, "=") {
			return decoded, true
		}
	}
	return nil, false
}

func isHexString(s string) bool {
	if len(s) < 8 || len(s)%2 != 0 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func singleOctets(v any) ([]byte, error) {
	switch x := v.(type) {
	case []byte:
		return append([]byte(nil), x...), nil
	case string:
		lower := strings.ToLower(x)
		switch {
		case strings.HasPrefix(lower, "base64:"):
			if b, ok := decodeBase64(strings.TrimSpace(x[len("base64:"):])); ok {
				return b, nil
			}
			return nil, fmt.Errorf("%w: invalid base64 value", ErrInvalidClaim)
		case strings.HasPrefix(lower, "b64:"):
			if b, ok := decodeBase64(strings.TrimSpace(x[len("b64:"):])); ok {
				return b, nil
			}
			return nil, fmt.Errorf("%w: invalid base64 value", ErrInvalidClaim)
		case strings.HasPrefix(lower, "hex:"):
			b, err := hex.DecodeString(strings.TrimSpace(x[len("hex:"):]))
			if err != nil {
				return nil, fmt.Errorf("%w: invalid hex value", ErrInvalidClaim)
			}
			return b, nil
		case strings.HasPrefix(lower, "0x"):
			b, err := hex.DecodeString(strings.TrimSpace(x[2:]))
			if err != nil {
				return nil, fmt.Errorf("%w: invalid hex value", ErrInvalidClaim)
			}
			return b, nil
		default:
			if b, ok := decodeBase64(x); ok {
				return b, nil
			}
			if isHexString(x) {
				if b, err := hex.DecodeString(x); err == nil {
					return b, nil
				}
			}
			return []byte(x), nil
		}
	}

	candidates, err := octetCandidates(v)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: empty byte value", ErrInvalidClaim)
	}
	return candidates[0], nil
}

func hashBytes(alg HashAlgorithm, data []byte) []byte {
	switch alg {
	case SHA256:
		sum := sha256.Sum256(data)
		return sum[:]
	case SHA384:
		sum := sha512.Sum384(data)
		return sum[:]
	case SHA512:
		sum := sha512.Sum512(data)
		return sum[:]
	default:
		return nil
	}
}

func anyOctetsEqual(actual [][]byte, expected ...[]byte) bool {
	for _, a := range actual {
		for _, e := range expected {
			if len(a) == len(e) && subtle.ConstantTimeCompare(a, e) == 1 {
				return true
			}
		}
	}
	return false
}

func measurementValues(v any) ([]any, error) {
	switch x := v.(type) {
	case []byte:
		return []any{x}, nil
	case [][]byte:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out, nil
	case []any:
		return x, nil
	case []string:
		out := make([]any, len(x))
		for i := range x {
			out[i] = x[i]
		}
		return out, nil
	}

	rv := reflect.ValueOf(v)
	for rv.IsValid() && (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface) {
		if rv.IsNil() {
			return nil, fmt.Errorf("%w: nil measurement list", ErrInvalidClaim)
		}
		rv = rv.Elem()
	}
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return []any{append([]byte(nil), rv.Bytes()...)}, nil
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, rv.Index(i).Interface())
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: expected measurement array, got %T", ErrInvalidClaim, v)
}
