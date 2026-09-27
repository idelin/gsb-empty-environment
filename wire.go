// Package wireint defines a canonical wire format for signed, unsigned and
// arbitrary-precision integers, plus a streaming JSON bridge.
//
// Wire format (self-delimiting, one canonical encoding per value):
//
//	0x00..0x7F        inline unsigned value 0..127 (single byte)
//	1 V V V K K K K   tagged field header byte (bit7 set)
//	  VVV = protocol version (1 or 2)
//	  KKKK = kind:
//	    0 uint8   1 uint16  2 uint32  3 uint64   (big-endian magnitude)
//	    4 int8    5 int16   6 int32   7 int64    (big-endian two's complement)
//	    8 bigint  (version 2 only):
//	      uvarint byteLen (1..MaxBigIntBytes, validated BEFORE allocation)
//	      sign byte (0x00 positive, 0x01 negative)
//	      magnitude bytes, big-endian, no leading zero byte
//
// Canonicality rules (all enforced on decode):
//   - values 0..127 must use the inline form;
//   - every other value must use the smallest kind that holds it, so a
//     negative number carried in a wider signed kind (non-canonical sign
//     extension) is rejected;
//   - non-negative values must use unsigned kinds, negatives signed kinds;
//   - bigint is only canonical for values outside int64/uint64 range, its
//     magnitude must have no leading zero byte, and its sign byte must be
//     0x00 or 0x01 (negative zero is impossible).
//
// Decoding never executes host code: it only reads length-prefixed bytes and
// interprets them as numbers. A field is fully self-delimiting, so rejecting
// a non-canonical field never consumes bytes of the next field.
package wireint

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// Protocol versions.
const (
	Version1 = 1 // fixed-width kinds only
	Version2 = 2 // adds arbitrary-precision bigint
)

// MaxVersion is the newest protocol version this implementation speaks.
const MaxVersion = Version2

// MaxBigIntBytes caps the bigint magnitude length. The length prefix is
// validated against this bound BEFORE any allocation happens.
const MaxBigIntBytes = 4096 // 32768 bits

var (
	ErrNonCanonical = errors.New("wireint: non-canonical encoding")
	ErrLength       = errors.New("wireint: invalid or excessive length")
	ErrVersion      = errors.New("wireint: unsupported protocol version")
)

// Kind identifies a wire integer kind.
type Kind int

const (
	KindUint8 Kind = iota
	KindUint16
	KindUint32
	KindUint64
	KindInt8
	KindInt16
	KindInt32
	KindInt64
	KindBig
)

var kindNames = [...]string{
	KindUint8: "u8", KindUint16: "u16", KindUint32: "u32", KindUint64: "u64",
	KindInt8: "i8", KindInt16: "i16", KindInt32: "i32", KindInt64: "i64",
	KindBig: "big",
}

func (k Kind) String() string {
	if k < 0 || int(k) >= len(kindNames) {
		return "unknown"
	}
	return kindNames[k]
}

// ParseKind parses a kind name such as "u8" or "big".
func ParseKind(s string) (Kind, bool) {
	for k := KindUint8; k <= KindBig; k++ {
		if kindNames[k] == s {
			return k, true
		}
	}
	return 0, false
}

// kindWidth returns the fixed byte width and signedness of a kind.
// Big returns (0, false).
func kindWidth(k Kind) (n int, signed bool) {
	switch k {
	case KindUint8:
		return 1, false
	case KindUint16:
		return 2, false
	case KindUint32:
		return 4, false
	case KindUint64:
		return 8, false
	case KindInt8:
		return 1, true
	case KindInt16:
		return 2, true
	case KindInt32:
		return 4, true
	case KindInt64:
		return 8, true
	}
	return 0, false
}

var (
	int8Min  = big.NewInt(-1 << 7)
	int16Min = big.NewInt(-1 << 15)
	int32Min = big.NewInt(-1 << 31)
	int64Min = big.NewInt(-1 << 63)
)

// minimalKind returns the smallest tagged kind that can hold v.
// The inline form (values 0..127) is handled separately by Encode/Decode.
func minimalKind(v *big.Int) Kind {
	if v.Sign() >= 0 {
		switch b := v.BitLen(); {
		case b <= 8:
			return KindUint8
		case b <= 16:
			return KindUint16
		case b <= 32:
			return KindUint32
		case b <= 64:
			return KindUint64
		default:
			return KindBig
		}
	}
	switch {
	case v.Cmp(int8Min) >= 0:
		return KindInt8
	case v.Cmp(int16Min) >= 0:
		return KindInt16
	case v.Cmp(int32Min) >= 0:
		return KindInt32
	case v.Cmp(int64Min) >= 0:
		return KindInt64
	default:
		return KindBig
	}
}

// Encode writes v to w in canonical form at the given protocol version.
// Arbitrary-precision values require Version2.
func Encode(w io.Writer, v *big.Int, version int) error {
	if version < Version1 || version > MaxVersion {
		return fmt.Errorf("%w: %d", ErrVersion, version)
	}
	var hdr [11]byte // tag + uvarint + sign, or tag alone
	if v.Sign() >= 0 && v.BitLen() <= 7 {
		hdr[0] = byte(v.Int64())
		_, err := w.Write(hdr[:1])
		return err
	}
	kind := minimalKind(v)
	if kind == KindBig && version < Version2 {
		return fmt.Errorf("%w: bigint kind requires version 2", ErrVersion)
	}
	hdr[0] = 0x80 | byte(version)<<4 | byte(kind)
	if n, _ := kindWidth(kind); n > 0 {
		buf := make([]byte, 1+n)
		buf[0] = hdr[0]
		encodeFixed(buf[1:], v)
		_, err := w.Write(buf)
		return err
	}
	// bigint: uvarint length, sign byte, magnitude
	mag := new(big.Int).Abs(v).Bytes() // no leading zeros by construction
	if len(mag) > MaxBigIntBytes {
		return fmt.Errorf("%w: magnitude %d bytes > %d", ErrLength, len(mag), MaxBigIntBytes)
	}
	n := binary.PutUvarint(hdr[1:], uint64(len(mag)))
	if v.Sign() < 0 {
		hdr[1+n] = 0x01
	} else {
		hdr[1+n] = 0x00
	}
	if _, err := w.Write(hdr[:2+n]); err != nil {
		return err
	}
	_, err := w.Write(mag)
	return err
}

// encodeFixed writes the low len(dst) bytes of v (two's complement).
func encodeFixed(dst []byte, v *big.Int) {
	x := v.Int64()
	for i := len(dst) - 1; i >= 0; i-- {
		dst[i] = byte(x)
		x >>= 8 // arithmetic shift keeps sign; byte() truncates to low 8 bits
	}
}

// Decode reads one self-delimiting integer from r. maxVersion pins the newest
// protocol version the caller accepts. The returned value is exact.
//
// A field's bytes are read in full before canonicality is validated, so when
// Decode reports ErrNonCanonical the reader is positioned exactly at the
// start of the next field.
func Decode(r io.ByteReader, maxVersion int) (*big.Int, error) {
	tag, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if tag < 0x80 {
		return big.NewInt(int64(tag)), nil
	}
	version := int(tag>>4) & 0x7
	kind := Kind(tag & 0x0F)
	if version < Version1 || version > maxVersion {
		return nil, fmt.Errorf("%w: %d", ErrVersion, version)
	}
	if kind < KindUint8 || kind > KindBig {
		return nil, fmt.Errorf("%w: unknown kind %d", ErrNonCanonical, int(kind))
	}
	if kind == KindBig && version < Version2 {
		return nil, fmt.Errorf("%w: bigint kind requires version 2", ErrVersion)
	}

	var v *big.Int
	if n, signed := kindWidth(kind); n > 0 {
		buf := make([]byte, n)
		if _, err := readFull(r, buf); err != nil {
			return nil, err
		}
		v = new(big.Int).SetBytes(buf)
		if signed && buf[0]&0x80 != 0 {
			v.Sub(v, new(big.Int).Lsh(big.NewInt(1), uint(8*n)))
		}
	} else {
		// bigint: validate length BEFORE allocating the magnitude buffer.
		l, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, err
		}
		if l == 0 || l > MaxBigIntBytes {
			return nil, fmt.Errorf("%w: %d bytes", ErrLength, l)
		}
		sign, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		mag := make([]byte, l)
		if _, err := readFull(r, mag); err != nil {
			return nil, err
		}
		// The field is fully consumed; validate canonicality now.
		if sign > 1 {
			return nil, fmt.Errorf("%w: bad sign byte 0x%02x", ErrNonCanonical, sign)
		}
		if mag[0] == 0 {
			return nil, fmt.Errorf("%w: leading zero in magnitude", ErrNonCanonical)
		}
		v = new(big.Int).SetBytes(mag)
		if sign == 1 {
			v.Neg(v)
		}
	}

	// Canonicality: values 0..127 belong to the inline form, and every other
	// value must use its minimal kind. This rejects non-canonical sign
	// extension (e.g. -1 carried as int64) and oversized kinds.
	if v.Sign() >= 0 && v.BitLen() <= 7 {
		return nil, fmt.Errorf("%w: %s must use inline form", ErrNonCanonical, v)
	}
	if minimalKind(v) != kind {
		return nil, fmt.Errorf("%w: %s carried as %s", ErrNonCanonical, v, kind)
	}
	return v, nil
}

// readFull reads len(p) bytes one at a time from an io.ByteReader, so it
// never consumes bytes beyond the current field (no buffering).
func readFull(r io.ByteReader, p []byte) (int, error) {
	for i := range p {
		b, err := r.ReadByte()
		if err != nil {
			return i, err
		}
		p[i] = b
	}
	return len(p), nil
}
