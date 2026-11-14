package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
)

// gf256 is the field GF(2^8) with reducing polynomial x^8+x^4+x^3+x+1 (0x11d).
// Arithmetic is implemented through logarithm/exp tables for clarity.
type gf256 struct {
	exp [512]byte
	log [256]uint16
}

func newGF256() *gf256 {
	var f gf256
	x := 1
	for i := 0; i < 255; i++ {
		f.exp[i] = byte(x)
		f.log[x] = uint16(i)
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11d
		}
	}
	for i := 255; i < 512; i++ {
		f.exp[i] = f.exp[i-255]
	}
	return &f
}

var field = *newGF256()

func (f *gf256) mul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return f.exp[int(f.log[a])+int(f.log[b])]
}

func (f *gf256) div(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	logA := int(f.log[a])
	logB := int(f.log[b])
	return f.exp[(logA-logB+255)%255]
}

// splitSecret splits secret into n Shamir fragments (indexed 1..n) over
// GF(256), any threshold of which reconstruct the secret. Every byte of the
// secret is treated as an independent shared value.
func splitSecret(secret []byte, threshold, n int, rng io.Reader) ([][]byte, error) {
	if threshold < 1 || threshold > n || n > 255 {
		return nil, ErrInvalidConfig
	}
	fragments := make([][]byte, n)
	for i := range fragments {
		fragments[i] = make([]byte, len(secret))
	}
	coeffs := make([]byte, threshold)
	for pos, value := range secret {
		coeffs[0] = value
		if _, err := io.ReadFull(rng, coeffs[1:]); err != nil {
			return nil, err
		}
		for x := 1; x <= n; x++ {
			xi := byte(x)
			// Horner evaluation of coeffs[0] + coeffs[1] x + ... in GF(256).
			y := coeffs[threshold-1]
			for k := threshold - 2; k >= 0; k-- {
				y = field.mul(y, xi) ^ coeffs[k]
			}
			fragments[x-1][pos] = y
		}
	}
	return fragments, nil
}

// combineSecret reconstructs a secret of secretLen bytes from the given
// (index, fragment) pairs using Lagrange interpolation at x = 0.
func combineSecret(parts []sharePart, secretLen int) []byte {
	secret := make([]byte, secretLen)
	for pos := 0; pos < secretLen; pos++ {
		var value byte
		for i, pi := range parts {
			num := byte(1)
			den := byte(1)
			for j, pj := range parts {
				if i == j {
					continue
				}
				num = field.mul(num, pj.index)
				den = field.mul(den, pi.index^pj.index)
			}
			weight := field.div(num, den)
			value ^= field.mul(weight, pi.fragment[pos])
		}
		secret[pos] = value
	}
	return secret
}

type sharePart struct {
	index    byte
	fragment []byte
}

// hmacSHA256 returns the HMAC-SHA256 of message under key.
func hmacSHA256(key, message []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	return mac.Sum(nil)
}

// validMAC reports whether expected matches actual in constant time.
func validMAC(expected, actual []byte) bool {
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

// writeLengthPrefixed appends a length-prefixed byte slice to dst, making the
// encoding unambiguous for bound context strings.
func writeLengthPrefixed(dst, data []byte) []byte {
	length := uint64(len(data))
	var buf [8]byte
	for i := 7; i >= 0; i-- {
		buf[i] = byte(length)
		length >>= 8
	}
	dst = append(dst, buf[:]...)
	dst = append(dst, data...)
	return dst
}

var errMalformedContext = errors.New("netsec: malformed encoded context")

// readLengthPrefixed reads one length-prefixed byte slice from data, returning
// the remainder.
func readLengthPrefixed(data []byte) (value, rest []byte, err error) {
	if len(data) < 8 {
		return nil, nil, errMalformedContext
	}
	var length uint64
	for _, b := range data[:8] {
		length = (length << 8) | uint64(b)
	}
	data = data[8:]
	if uint64(len(data)) < length {
		return nil, nil, errMalformedContext
	}
	return data[:length], data[length:], nil
}
