package netsec

import (
	"errors"
	"strings"
)

// Well-known multihash codes (multiformats/multicodec, multihash section).
const (
	MultihashIdentity = 0x00
	MultihashSHA256   = 0x12
)

// ErrInvalidMultihash is returned when bytes do not form a self-describing
// multihash: a varint hash code, a varint digest length, and exactly that many
// digest bytes.
var ErrInvalidMultihash = errors.New("netsec: invalid multihash")

// Multihash is the raw, self-describing multihash of a content object. It is
// the canonical key used by the blocklist: CIDv0, CIDv1 and every multibase
// spelling of the same content reduce to an identical Multihash.
type Multihash struct {
	// raw holds the full multihash: code (varint) + length (varint) + digest.
	raw []byte
}

// MultihashFromBytes validates and wraps an already-encoded multihash.
func MultihashFromBytes(b []byte) (Multihash, error) {
	if err := validateMultihash(b); err != nil {
		return Multihash{}, err
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	return Multihash{raw: cp}, nil
}

// NewMultihash builds a multihash from a hash code and digest.
func NewMultihash(code uint64, digest []byte) (Multihash, error) {
	if len(digest) == 0 && code != MultihashIdentity {
		return Multihash{}, ErrInvalidMultihash
	}
	raw := append(appendUvarint(nil, code), appendUvarint(nil, uint64(len(digest)))...)
	raw = append(raw, digest...)
	return Multihash{raw: raw}, nil
}

// Bytes returns a copy of the encoded multihash.
func (m Multihash) Bytes() []byte {
	if m.raw == nil {
		return nil
	}
	out := make([]byte, len(m.raw))
	copy(out, m.raw)
	return out
}

// String renders the multihash as a base58btc multibase string, the canonical
// human-readable form also used inside CIDv0.
func (m Multihash) String() string {
	if m.raw == nil {
		return ""
	}
	s, err := encodeMultibase(prefixBase58BTC, m.raw)
	if err != nil {
		return ""
	}
	return s
}

// Hex renders the multihash as a lower-case base16 multibase string.
func (m Multihash) Hex() string {
	if m.raw == nil {
		return ""
	}
	s, err := encodeMultibase(prefixBase16, m.raw)
	if err != nil {
		return ""
	}
	return s
}

// Code returns the multihash function code.
func (m Multihash) Code() (uint64, error) {
	code, n, err := readUvarint(m.raw)
	if err != nil || n == 0 {
		return 0, ErrInvalidMultihash
	}
	return code, nil
}

// IsZero reports whether the multihash is uninitialized.
func (m Multihash) IsZero() bool {
	return len(m.raw) == 0
}

func validateMultihash(b []byte) error {
	_, n1, err := readUvarint(b)
	if err != nil || n1 == 0 {
		return ErrInvalidMultihash
	}
	length, n2, err := readUvarint(b[n1:])
	if err != nil || n2 == 0 {
		return ErrInvalidMultihash
	}
	if uint64(len(b)-n1-n2) != length {
		return ErrInvalidMultihash
	}
	return nil
}

// ---------------------------------------------------------------------------
// unsigned varint (LEB128) helpers
// ---------------------------------------------------------------------------

func appendUvarint(buf []byte, x uint64) []byte {
	for x >= 0x80 {
		buf = append(buf, byte(x)|0x80)
		x >>= 7
	}
	return append(buf, byte(x))
}

// readUvarint mirrors binary.Uvarint but maps every failure to a boolean. It
// returns the value and the number of bytes consumed.
func readUvarint(b []byte) (uint64, int, error) {
	var x uint64
	var shift uint
	for i, c := range b {
		if i >= 10 {
			return 0, 0, ErrInvalidMultihash
		}
		if c < 0x80 {
			if i == 9 && c > 1 {
				return 0, 0, ErrInvalidMultihash
			}
			return x | uint64(c)<<shift, i + 1, nil
		}
		x |= uint64(c&0x7f) << shift
		shift += 7
	}
	return 0, 0, ErrInvalidMultihash
}

// parseMultihash accepts either raw multihash bytes or a base58btc multibase
// string (as found in blocklists copied from CIDv0 entries).
func parseMultihash(v string) (Multihash, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return Multihash{}, ErrInvalidMultihash
	}
	var raw []byte
	var err error
	if v[0] == prefixBase58BTC {
		raw, err = decodeMultibase(v)
	} else {
		// Tolerate bare base58btc without the leading 'z'.
		raw, err = base58Decode(v)
	}
	if err != nil {
		return Multihash{}, err
	}
	return MultihashFromBytes(raw)
}
