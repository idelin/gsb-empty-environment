package intwire

import (
	"errors"
	"io"
	"math/big"
)

// MaxBigBytes bounds the byte length of a serialized arbitrary-precision
// integer. The decoder validates the declared length against this limit and
// against the bytes actually remaining BEFORE allocating anything.
const MaxBigBytes = 1 << 16

// Version selects the wire protocol revision. The first byte of every
// encoded value is the version, so a decoder can serve multiple revisions.
type Version byte

const (
	V1 Version = 1 // tag = kind<<6 | widthCode; big = minimal two's complement
	V2 Version = 2 // kind and width in separate bytes; big = sign + magnitude
)

var (
	ErrVersion      = errors.New("intwire: unsupported protocol version")
	ErrNonCanonical = errors.New("intwire: non-canonical encoding")
	ErrOverflow     = errors.New("intwire: varint overflows 64 bits")
	ErrLength       = errors.New("intwire: declared length exceeds limit or input")
	ErrTruncated    = errors.New("intwire: truncated input")
)

// Append encodes v using the given protocol version onto buf.
func Append(buf []byte, ver Version, v Value) ([]byte, error) {
	if err := v.validate(); err != nil {
		return nil, err
	}
	switch ver {
	case V1:
		return appendV1(buf, v), nil
	case V2:
		return appendV2(buf, v), nil
	}
	return nil, ErrVersion
}

// Encode writes one value to w.
func Encode(w io.Writer, ver Version, v Value) error {
	buf, err := Append(nil, ver, v)
	if err != nil {
		return err
	}
	_, err = w.Write(buf)
	return err
}

// Decoder reads consecutive values from a byte slice. On any decode error
// the decoder position is left unchanged, so a rejected non-canonical
// encoding never consumes the next field.
type Decoder struct {
	buf []byte
	pos int
}

func NewDecoder(buf []byte) *Decoder { return &Decoder{buf: buf} }

// Offset reports the current read position.
func (d *Decoder) Offset() int { return d.pos }

// Done reports whether all input has been consumed.
func (d *Decoder) Done() bool { return d.pos >= len(d.buf) }

// Rest returns the not-yet-consumed input bytes.
func (d *Decoder) Rest() []byte { return d.buf[d.pos:] }

// Decode reads the next value. On error the decoder position is NOT
// advanced: malformed bytes are never silently swallowed.
func (d *Decoder) Decode() (Value, error) {
	v, npos, err := decodeValue(d.buf, d.pos)
	if err != nil {
		return Value{}, err
	}
	d.pos = npos
	return v, nil
}

func decodeValue(buf []byte, pos int) (Value, int, error) {
	if pos >= len(buf) {
		return Value{}, pos, ErrTruncated
	}
	switch Version(buf[pos]) {
	case V1:
		return decodeV1(buf, pos+1)
	case V2:
		return decodeV2(buf, pos+1)
	}
	return Value{}, pos, ErrVersion
}

// ---- canonical varint primitives ----

func appendUvarint(dst []byte, x uint64) []byte {
	for x >= 0x80 {
		dst = append(dst, byte(x)|0x80)
		x >>= 7
	}
	return append(dst, byte(x))
}

// readUvarint reads a base-128 varint and rejects non-minimal forms
// (a zero final group) and 64-bit overflow.
func readUvarint(buf []byte, pos int) (uint64, int, error) {
	var x uint64
	var s uint
	for i := 0; ; i++ {
		if pos >= len(buf) {
			return 0, pos, ErrTruncated
		}
		b := buf[pos]
		pos++
		if i == 9 && b > 1 { // 10th byte may only contribute bit 63
			return 0, pos, ErrOverflow
		}
		if b < 0x80 {
			if i > 0 && b == 0 {
				return 0, pos, ErrNonCanonical // e.g. 0x80 0x00 for zero
			}
			x |= uint64(b) << s
			return x, pos, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
}

func zigzagEncode(i int64) uint64 { return uint64(i<<1) ^ uint64(i>>63) }

func zigzagDecode(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

// ---- version 1 ----

// tag: bits 7..6 = kind (0 u, 1 s, 2 big), bits 5..0 = width code
// (width/8 for fixed kinds, 0 for big).
func appendV1(dst []byte, v Value) []byte {
	dst = append(dst, byte(V1))
	switch v.Kind {
	case KindUnsigned:
		dst = append(dst, byte(0<<6|v.Width/8))
		return appendUvarint(dst, v.U)
	case KindSigned:
		dst = append(dst, byte(1<<6|v.Width/8))
		return appendUvarint(dst, zigzagEncode(v.I))
	case KindBig:
		dst = append(dst, byte(2<<6))
		tw := twosComplement(v.B)
		dst = appendUvarint(dst, uint64(len(tw)))
		return append(dst, tw...)
	}
	return dst
}

// twosComplement returns the minimal-length big-endian two's complement
// encoding of b. Zero is a single 0x00 byte.
func twosComplement(b *big.Int) []byte {
	if b.Sign() == 0 {
		return []byte{0x00}
	}
	n := (b.BitLen() + 8) / 8 // one extra sign bit headroom
	out := make([]byte, n)
	mod := new(big.Int).Lsh(big.NewInt(1), uint(8*n))
	v := new(big.Int).Mod(b, mod) // wraps negatives into [0, 2^(8n))
	v.FillBytes(out)
	// shrink redundant sign-extension bytes (should not happen with the
	// sizing above, but keep the encoder provably minimal)
	for len(out) > 1 {
		if out[0] == 0x00 && out[1]&0x80 == 0 {
			out = out[1:]
		} else if out[0] == 0xff && out[1]&0x80 != 0 {
			out = out[1:]
		} else {
			break
		}
	}
	return out
}

func decodeV1(buf []byte, pos int) (Value, int, error) {
	if pos >= len(buf) {
		return Value{}, pos, ErrTruncated
	}
	tag := buf[pos]
	pos++
	kind := tag >> 6
	width := int(tag&0x3f) * 8
	switch kind {
	case 0, 1:
		if !validWidth(width) {
			return Value{}, pos, ErrWidth
		}
		u, npos, err := readUvarint(buf, pos)
		if err != nil {
			return Value{}, pos, err
		}
		pos = npos
		if kind == 0 {
			v, err := NewUnsigned(width, u)
			if err != nil {
				return Value{}, pos, err
			}
			return v, pos, nil
		}
		v, err := NewSigned(width, zigzagDecode(u))
		if err != nil {
			return Value{}, pos, err
		}
		return v, pos, nil
	case 2:
		n64, npos, err := readUvarint(buf, pos)
		if err != nil {
			return Value{}, pos, err
		}
		pos = npos
		// Validate length BEFORE allocating: limit, then availability.
		if n64 == 0 || n64 > MaxBigBytes {
			return Value{}, pos, ErrLength
		}
		if uint64(len(buf)-pos) < n64 {
			return Value{}, pos, ErrTruncated
		}
		n := int(n64)
		raw := buf[pos : pos+n]
		if !canonicalTwosComplement(raw) {
			return Value{}, pos, ErrNonCanonical
		}
		pos += n
		mag := new(big.Int).SetBytes(raw)
		if raw[0]&0x80 != 0 {
			mod := new(big.Int).Lsh(big.NewInt(1), uint(8*n))
			mag.Sub(mag, mod)
		}
		return NewBig(mag), pos, nil
	}
	return Value{}, pos, ErrBadKind
}

// canonicalTwosComplement rejects redundant sign extension: a leading 0x00
// whose removal keeps the value positive, or a leading 0xFF whose removal
// keeps it negative.
func canonicalTwosComplement(b []byte) bool {
	if len(b) <= 1 {
		return true
	}
	if b[0] == 0x00 && b[1]&0x80 == 0 {
		return false
	}
	if b[0] == 0xff && b[1]&0x80 != 0 {
		return false
	}
	return true
}

// ---- version 2 ----

// layout: version, kind byte, width byte, payload.
// big payload: sign byte (0/1), uvarint magnitude length, magnitude bytes
// (big-endian, no leading zeros; zero = sign 0 + length 0).
func appendV2(dst []byte, v Value) []byte {
	dst = append(dst, byte(V2), byte(v.Kind), byte(v.Width))
	switch v.Kind {
	case KindUnsigned:
		return appendUvarint(dst, v.U)
	case KindSigned:
		return appendUvarint(dst, zigzagEncode(v.I))
	case KindBig:
		if v.B.Sign() == 0 {
			return appendUvarint(append(dst, 0x00), 0)
		}
		sign := byte(0x00)
		if v.B.Sign() < 0 {
			sign = 0x01
		}
		mag := new(big.Int).Abs(v.B).Bytes()
		dst = append(dst, sign)
		dst = appendUvarint(dst, uint64(len(mag)))
		return append(dst, mag...)
	}
	return dst
}

func decodeV2(buf []byte, pos int) (Value, int, error) {
	if pos+2 > len(buf) {
		return Value{}, pos, ErrTruncated
	}
	kind := Kind(buf[pos])
	width := int(buf[pos+1])
	pos += 2
	switch kind {
	case KindUnsigned, KindSigned:
		if !validWidth(width) {
			return Value{}, pos, ErrWidth
		}
		u, npos, err := readUvarint(buf, pos)
		if err != nil {
			return Value{}, pos, err
		}
		pos = npos
		if kind == KindUnsigned {
			v, err := NewUnsigned(width, u)
			if err != nil {
				return Value{}, pos, err
			}
			return v, pos, nil
		}
		v, err := NewSigned(width, zigzagDecode(u))
		if err != nil {
			return Value{}, pos, err
		}
		return v, pos, nil
	case KindBig:
		if width != 0 {
			return Value{}, pos, ErrWidth
		}
		if pos >= len(buf) {
			return Value{}, pos, ErrTruncated
		}
		sign := buf[pos]
		pos++
		if sign > 1 {
			return Value{}, pos, ErrNonCanonical
		}
		n64, npos, err := readUvarint(buf, pos)
		if err != nil {
			return Value{}, pos, err
		}
		pos = npos
		if n64 > MaxBigBytes {
			return Value{}, pos, ErrLength
		}
		if uint64(len(buf)-pos) < n64 {
			return Value{}, pos, ErrTruncated
		}
		n := int(n64)
		if n == 0 {
			if sign != 0 {
				return Value{}, pos, ErrNonCanonical // negative zero
			}
			return NewBig(big.NewInt(0)), pos, nil
		}
		raw := buf[pos : pos+n]
		if raw[0] == 0x00 {
			return Value{}, pos, ErrNonCanonical // leading zero magnitude
		}
		pos += n
		mag := new(big.Int).SetBytes(raw)
		if sign == 1 {
			mag.Neg(mag)
		}
		return NewBig(mag), pos, nil
	}
	return Value{}, pos, ErrBadKind
}
