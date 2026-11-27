package dnssec

import (
	"crypto/sha1"
	"errors"
	"strings"
)

var (
	ErrNSEC3Alg       = errors.New("dnssec: unsupported NSEC3 hash algorithm")
	ErrInvalidHashLen = errors.New("dnssec: invalid NSEC3 hash length")
	ErrBadBase32Hex   = errors.New("dnssec: invalid base32hex encoding")
)

// base32hexAlphabet is the Extended Hex Alphabet of RFC 4648 section 7 used
// by NSEC3 (0-9 then a-v), without padding.
const base32hexAlphabet = "0123456789abcdefghijklmnopqrstuv"

// NSEC3Hash computes the iterated owner hash of name per RFC 5155
// section 3.1.4. The initial value H(0) is SHA1 over the wire name and salt;
// the iteration then runs exactly iterations additional rounds:
//
//	H(0)            = SHA1(wire(lowercase name) || salt)
//	H(k), 1<=k<=iter = SHA1(H(k-1) || salt)
//
// Only SHA-1 (algorithm 1) is defined for NSEC3.
func NSEC3Hash(name Name, salt []byte, iterations uint16, algorithm uint8) ([]byte, error) {
	if algorithm != NSEC3SHA1 {
		return nil, ErrNSEC3Alg
	}
	h := sha1.New()
	h.Write(name.LowerWire())
	h.Write(salt)
	digest := h.Sum(nil)

	for i := uint16(0); i < iterations; i++ {
		iter := sha1.New()
		iter.Write(digest)
		iter.Write(salt)
		digest = iter.Sum(nil)
	}
	if len(digest) != 20 {
		return nil, ErrInvalidHashLen
	}
	return digest, nil
}

// Base32HexEncode encodes data as unpadded lowercase base32hex.
func Base32HexEncode(data []byte) string {
	var b strings.Builder
	bits := uint(0)
	acc := uint(0)
	for _, c := range data {
		acc = (acc << 8) | uint(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			idx := (acc >> bits) & 0x1F
			b.WriteByte(base32hexAlphabet[idx])
		}
	}
	if bits > 0 {
		idx := (acc << (5 - bits)) & 0x1F
		b.WriteByte(base32hexAlphabet[idx])
	}
	return b.String()
}

// Base32HexDecode decodes an unpadded, case-insensitive base32hex string.
func Base32HexDecode(s string) ([]byte, error) {
	var out []byte
	bits := uint(0)
	acc := uint(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		var idx uint
		switch {
		case c >= '0' && c <= '9':
			idx = uint(c - '0')
		case c >= 'a' && c <= 'v':
			idx = uint(c-'a') + 10
		case c >= 'A' && c <= 'V':
			idx = uint(c-'A') + 10
		default:
			return nil, ErrBadBase32Hex
		}
		acc = (acc << 5) | idx
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte((acc>>bits)&0xFF))
		}
	}
	if bits >= 5 {
		return nil, ErrBadBase32Hex
	}
	return out, nil
}

// HashedOwnerName extracts and decodes the hashed owner name of an NSEC3
// record. The first label must be base32hex and the remaining labels must
// equal zone.
func HashedOwnerName(nsec3RR RR, zone Name) ([]byte, error) {
	if nsec3RR.Type != TypeNSEC3 || nsec3RR.Name.LabelCount() != zone.LabelCount()+1 {
		return nil, ErrBogus("NSEC3 owner name is not a single hash label under the zone")
	}
	suffix, err := nsec3RR.Name.TrimToLabels(zone.LabelCount())
	if err != nil || !suffix.Equal(zone) {
		return nil, ErrBogus("NSEC3 owner name is outside the zone")
	}
	hash, err := Base32HexDecode(string(nsec3RR.Name.Labels()[0]))
	if err != nil {
		return nil, ErrBogus("NSEC3 owner hash label is not base32hex")
	}
	return hash, nil
}

// NSEC3Covers reports whether hash lies strictly inside the open interval
// (owner, nextHashed) on the circular hash name space, following RFC 5155
// section 3.3. Two cases exist:
//
//   - owner < nextHashed: a normal interval, covered when owner < h < next
//   - owner > nextHashed: a wraparound interval covering the zero point
//
// Equality with either endpoint is not coverage (that is an exact match).
func NSEC3Covers(ownerHash, nextHashed, target []byte) bool {
	c := compareBytes(ownerHash, nextHashed)
	if c == 0 {
		return false
	}
	afterOwner := compareBytes(target, ownerHash) > 0
	beforeNext := compareBytes(target, nextHashed) < 0
	if c < 0 {
		return afterOwner && beforeNext
	}
	// Wraparound interval.
	return afterOwner || beforeNext
}

// NSEC3Matches reports exact equality of the owner hash and target.
func NSEC3Matches(ownerHash, target []byte) bool {
	return compareBytes(ownerHash, target) == 0
}

// NSECCovers implements the analogous (non-circular in well-formed zones)
// canonical-name interval test for NSEC records: owner < target < next.
func NSECCovers(owner, next, target Name) bool {
	return CanonicalCompare(owner, target) < 0 && CanonicalCompare(target, next) < 0
}

// NSECMatches reports exact name equality for NSEC.
func NSECMatches(owner, target Name) bool { return owner.Equal(target) }

func compareBytes(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
