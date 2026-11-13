package netsec

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
)

// ParseCanonicalInt parses a base-10 integer with exactly one accepted
// spelling. ASCII digits only are accepted. Leading zeroes, a leading plus
// sign, whitespace, underscores, hexadecimal notation, and negative zero are
// rejected. A single leading minus is accepted for a nonzero negative value;
// callers that require nonnegative values reject it separately.
func ParseCanonicalInt(s string) (*big.Int, error) {
	if len(s) == 0 {
		return nil, fmt.Errorf("%w: empty integer", ErrNonCanonical)
	}

	body := s
	negative := false
	if body[0] == '-' {
		negative = true
		body = body[1:]
		if len(body) == 0 {
			return nil, fmt.Errorf("%w: sign without digits", ErrNonCanonical)
		}
	}
	if body[0] == '+' {
		return nil, fmt.Errorf("%w: leading plus sign", ErrNonCanonical)
	}
	if body[0] < '0' || body[0] > '9' {
		return nil, fmt.Errorf("%w: non-ASCII digit", ErrNonCanonical)
	}
	if body[0] == '0' && len(body) != 1 {
		return nil, fmt.Errorf("%w: leading zero", ErrNonCanonical)
	}
	if negative && body == "0" {
		return nil, fmt.Errorf("%w: negative zero", ErrNonCanonical)
	}
	for i := 1; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return nil, fmt.Errorf("%w: non-ASCII digit", ErrNonCanonical)
		}
	}

	z, ok := new(big.Int).SetString(s, 10)
	if !ok || z == nil {
		return nil, fmt.Errorf("%w: invalid integer", ErrNonCanonical)
	}
	// Defensive check: this also catches any future SetString relaxation.
	if z.String() != s {
		return nil, fmt.Errorf("%w: non-canonical integer", ErrNonCanonical)
	}
	return z, nil
}

func parseCanonicalPositive(s, what string) (*big.Int, error) {
	z, err := ParseCanonicalInt(s)
	if err != nil {
		return nil, err
	}
	if z.Sign() <= 0 {
		return nil, fmt.Errorf("%w: %s must be positive", ErrInvalidRange, what)
	}
	return z, nil
}

// ParseCanonicalUint32 parses a canonical base-10 uint32. Values such as
// "03", "+3", " 3", and "-1" are rejected.
func ParseCanonicalUint32(s string) (uint32, error) {
	z, err := ParseCanonicalInt(s)
	if err != nil {
		return 0, err
	}
	if z.Sign() < 0 || z.BitLen() > 32 {
		return 0, fmt.Errorf("%w: uint32", ErrInvalidRange)
	}
	v, err := strconv.ParseUint(z.String(), 10, 32)
	if err != nil {
		return 0, errors.Join(ErrNonCanonical, err)
	}
	return uint32(v), nil
}

func parseRootDigest(s string) ([32]byte, error) {
	var out [32]byte
	if len(s) != 64 {
		return out, fmt.Errorf("%w: root digest must be 64 lowercase hex digits", ErrNonCanonical)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return out, fmt.Errorf("%w: root digest must be lowercase hex", ErrNonCanonical)
		}
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return out, errors.Join(ErrNonCanonical, err)
	}
	copy(out[:], b)
	return out, nil
}
