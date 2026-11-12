package netsec

import (
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
)

// Multibase prefixes, per the multibase specification (multiformats/multibase).
const (
	prefixBase16      = 'f' // lower-case base16
	prefixBase16Upper = 'F' // upper-case base16
	prefixBase32      = 'b' // RFC 4648 lower-case, no padding
	prefixBase32Upper = 'B' // RFC 4648 upper-case, no padding
	prefixBase36      = 'k' // lower-case base36
	prefixBase36Upper = 'K' // upper-case base36
	prefixBase58BTC   = 'z' // base58 bitcoin
	prefixBase64      = 'm' // RFC 4648, padding
	prefixBase64URL   = 'u' // RFC 4648 URL-safe, no padding
)

// ErrUnsupportedMultibase is returned when a string uses a multibase prefix
// the gateway does not know how to decode.
var ErrUnsupportedMultibase = errors.New("netsec: unsupported multibase encoding")

// ErrInvalidMultibase is returned when the payload of a multibase string is
// not valid for its declared encoding.
var ErrInvalidMultibase = errors.New("netsec: invalid multibase payload")

var base32Lower = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
var base32Upper = base32.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZ234567").WithPadding(base32.NoPadding)

var base64NoPad = base64.RawStdEncoding
var base64URLNoPad = base64.URLEncoding.WithPadding(base64.NoPadding)

// decodeMultibase decodes a multibase-encoded string into raw bytes.
func decodeMultibase(s string) ([]byte, error) {
	if s == "" {
		return nil, ErrInvalidMultibase
	}
	prefix := s[0]
	payload := s[1:]
	switch prefix {
	case prefixBase16:
		return decodeHex(strings.ToLower(payload))
	case prefixBase16Upper:
		return decodeHex(strings.ToUpper(payload))
	case prefixBase32:
		return base32Lower.DecodeString(strings.ToLower(payload))
	case prefixBase32Upper:
		return base32Upper.DecodeString(strings.ToUpper(payload))
	case prefixBase36:
		return decodeBase36(strings.ToLower(payload))
	case prefixBase36Upper:
		return decodeBase36(strings.ToUpper(payload))
	case prefixBase58BTC:
		return base58Decode(payload)
	case prefixBase64:
		return base64NoPad.DecodeString(addBase64Padding(payload))
	case prefixBase64URL:
		return base64URLNoPad.DecodeString(payload)
	default:
		return nil, ErrUnsupportedMultibase
	}
}

// encodeMultibase renders raw bytes in the multibase encoding selected by
// prefix.
func encodeMultibase(prefix byte, data []byte) (string, error) {
	switch rune(prefix) {
	case prefixBase16:
		return string(prefix) + strings.ToLower(hex.EncodeToString(data)), nil
	case prefixBase16Upper:
		return string(prefix) + strings.ToUpper(hex.EncodeToString(data)), nil
	case prefixBase32:
		return string(prefix) + strings.ToLower(base32Lower.EncodeToString(data)), nil
	case prefixBase32Upper:
		return string(prefix) + strings.ToUpper(base32Upper.EncodeToString(data)), nil
	case prefixBase36:
		return string(prefix) + strings.ToLower(encodeBase36(data)), nil
	case prefixBase36Upper:
		return string(prefix) + strings.ToUpper(encodeBase36(data)), nil
	case prefixBase58BTC:
		return string(prefix) + base58Encode(data), nil
	case prefixBase64:
		return string(prefix) + addBase64Padding(base64NoPad.EncodeToString(data)), nil
	case prefixBase64URL:
		return string(prefix) + base64URLNoPad.EncodeToString(data), nil
	default:
		return "", ErrUnsupportedMultibase
	}
}

func decodeHex(payload string) ([]byte, error) {
	if len(payload)%2 != 0 {
		return nil, ErrInvalidMultibase
	}
	out, err := hex.DecodeString(payload)
	if err != nil {
		return nil, ErrInvalidMultibase
	}
	return out, nil
}

// addBase64Padding restores the "=" padding stripped by the base64 multibase
// variants so the stdlib decoder accepts the input.
func addBase64Padding(s string) string {
	if m := len(s) % 4; m != 0 {
		return s + strings.Repeat("=", 4-m)
	}
	return s
}

// ---------------------------------------------------------------------------
// base58btc
// ---------------------------------------------------------------------------

const base58Alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

var base58Index = func() [256]int {
	var table [256]int
	for i := range table {
		table[i] = -1
	}
	for i, c := range []byte(base58Alphabet) {
		table[c] = i
	}
	return table
}()

func base58Decode(input string) ([]byte, error) {
	if input == "" {
		return []byte{}, nil
	}
	radix := big.NewInt(58)
	n := new(big.Int)
	for i := 0; i < len(input); i++ {
		digit := base58Index[input[i]]
		if digit < 0 {
			return nil, ErrInvalidMultibase
		}
		n.Mul(n, radix)
		n.Add(n, big.NewInt(int64(digit)))
	}
	bytes := n.Bytes()

	// Leading '1' characters encode leading zero bytes.
	leadingZeros := 0
	for leadingZeros < len(input) && input[leadingZeros] == '1' {
		leadingZeros++
	}
	out := make([]byte, leadingZeros+len(bytes))
	copy(out[leadingZeros:], bytes)
	return out, nil
}

func base58Encode(input []byte) string {
	leadingZeros := 0
	for leadingZeros < len(input) && input[leadingZeros] == 0 {
		leadingZeros++
	}
	n := new(big.Int).SetBytes(input)
	radix := big.NewInt(58)
	mod := new(big.Int)

	var digits []byte
	for n.Sign() > 0 {
		n.QuoRem(n, radix, mod)
		digits = append(digits, base58Alphabet[mod.Int64()])
	}
	for i := 0; i < leadingZeros; i++ {
		digits = append(digits, base58Alphabet[0])
	}

	// digits are least-significant first; reverse them.
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}

// ---------------------------------------------------------------------------
// base36 (used by IPNS names in DNS labels)
// ---------------------------------------------------------------------------

const base36Alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

var base36Index = func() [256]int {
	var table [256]int
	for i := range table {
		table[i] = -1
	}
	for i, c := range []byte(base36Alphabet) {
		table[c] = i
	}
	for i, c := range []byte(strings.ToUpper(base36Alphabet[10:])) {
		table[c] = i + 10
	}
	return table
}()

func decodeBase36(input string) ([]byte, error) {
	if input == "" {
		return []byte{}, nil
	}
	n := new(big.Int)
	radix := big.NewInt(36)
	for i := 0; i < len(input); i++ {
		digit := base36Index[input[i]]
		if digit < 0 {
			return nil, ErrInvalidMultibase
		}
		n.Mul(n, radix)
		n.Add(n, big.NewInt(int64(digit)))
	}
	bytes := n.Bytes()

	leadingZeros := 0
	for leadingZeros < len(input) && input[leadingZeros] == '0' {
		leadingZeros++
	}
	out := make([]byte, leadingZeros+len(bytes))
	copy(out[leadingZeros:], bytes)
	return out, nil
}

func encodeBase36(input []byte) string {
	leadingZeros := 0
	for leadingZeros < len(input) && input[leadingZeros] == 0 {
		leadingZeros++
	}
	n := new(big.Int).SetBytes(input)
	radix := big.NewInt(36)
	mod := new(big.Int)

	var digits []byte
	for n.Sign() > 0 {
		n.QuoRem(n, radix, mod)
		digits = append(digits, base36Alphabet[mod.Int64()])
	}
	for i := 0; i < leadingZeros; i++ {
		digits = append(digits, base36Alphabet[0])
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	if len(digits) == 0 {
		return "0"
	}
	return string(digits)
}
