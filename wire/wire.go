// Package wire implements a zero-dependency, hand-written Protocol Buffers
// binary wire-format parser. It supports varint (7-bit compressed) encoding,
// ZigZag signed integer transforms, fixed32/fixed64 scalars and nested
// length-delimited messages, with strict bounds checking on every read.
package wire

import "fmt"

// WireType is the 3-bit type tag of a protobuf field key.
type WireType int

const (
	WireVarint          WireType = 0 // int32, int64, uint32, uint64, sint*, bool, enum
	WireFixed64         WireType = 1 // fixed64, sfixed64, double
	WireLengthDelimited WireType = 2 // string, bytes, embedded messages, packed repeated
	WireFixed32         WireType = 5 // fixed32, sfixed32, float
)

func (t WireType) String() string {
	switch t {
	case WireVarint:
		return "varint"
	case WireFixed64:
		return "fixed64"
	case WireLengthDelimited:
		return "length-delimited"
	case WireFixed32:
		return "fixed32"
	default:
		return fmt.Sprintf("unknown(%d)", int(t))
	}
}

// ErrCode classifies decoder failures so callers can aggregate them
// (e.g. into a metrics error-code counter) without string matching.
type ErrCode int

const (
	ErrOK             ErrCode = iota
	ErrTruncated              // buffer ended before the value was complete
	ErrOverflow               // varint longer than 10 bytes / exceeds 64 bits
	ErrInvalidWire            // reserved or unknown wire type
	ErrBadFieldNumber         // field number 0 or out of range
	ErrNegativeLength         // declared length exceeds remaining input
	ErrDepthLimit             // nesting deeper than the configured maximum
	ErrTypeMismatch           // reflection engine: tag kind does not match wire type
	ErrUnsupported            // reflection engine: unsupported Go target type
)

func (c ErrCode) String() string {
	switch c {
	case ErrOK:
		return "ok"
	case ErrTruncated:
		return "truncated"
	case ErrOverflow:
		return "overflow"
	case ErrInvalidWire:
		return "invalid_wire_type"
	case ErrBadFieldNumber:
		return "bad_field_number"
	case ErrNegativeLength:
		return "negative_length"
	case ErrDepthLimit:
		return "depth_limit"
	case ErrTypeMismatch:
		return "type_mismatch"
	case ErrUnsupported:
		return "unsupported"
	default:
		return "unknown"
	}
}

// Error is the single error type returned by the parser. It carries a
// machine-readable code plus the byte offset and field number at which
// the failure occurred.
type Error struct {
	Code   ErrCode
	Offset int
	Field  int
	Msg    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("wire: %s (code=%s offset=%d field=%d)", e.Msg, e.Code, e.Offset, e.Field)
}

func newError(code ErrCode, off, field int, format string, args ...interface{}) *Error {
	return &Error{Code: code, Offset: off, Field: field, Msg: fmt.Sprintf(format, args...)}
}

// CodeOf extracts the ErrCode from an error, returning ErrOK for nil and
// ErrUnsupported for foreign error values.
func CodeOf(err error) ErrCode {
	if err == nil {
		return ErrOK
	}
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return ErrUnsupported
}

// MaxVarintBytes is the longest legal varint: 64 bits of payload need
// ceil(64/7) = 10 groups.
const MaxVarintBytes = 10

// MaxFieldNumber is the largest field number the protobuf spec allows
// (2^29 - 1).
const MaxFieldNumber = 1<<29 - 1

// Reader is a cursor over a byte slice. Every method performs explicit
// bounds checks and never panics on malformed input.
type Reader struct {
	buf []byte
	off int
}

// NewReader returns a Reader over buf. The buffer is not copied.
func NewReader(buf []byte) *Reader { return &Reader{buf: buf} }

// Offset returns the current read position.
func (r *Reader) Offset() int { return r.off }

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int { return len(r.buf) - r.off }

// EOF reports whether the buffer is fully consumed.
func (r *Reader) EOF() bool { return r.off >= len(r.buf) }

// ReadVarint decodes one base-128 varint (7 bits per byte, little-endian
// groups, MSB continuation flag). It rejects encodings longer than
// MaxVarintBytes and a 10th byte carrying bits above bit 63.
func (r *Reader) ReadVarint() (uint64, error) {
	start := r.off
	var v uint64
	for i := 0; i < MaxVarintBytes; i++ {
		if r.off >= len(r.buf) {
			return 0, newError(ErrTruncated, start, 0,
				"varint truncated after %d byte(s)", r.off-start)
		}
		b := r.buf[r.off]
		r.off++
		if i == MaxVarintBytes-1 {
			// 10th byte may only contribute bit 63.
			if b > 1 {
				return 0, newError(ErrOverflow, start, 0,
					"varint overflows 64 bits")
			}
			v |= uint64(b) << 63
			return v, nil
		}
		v |= uint64(b&0x7f) << (uint(i) * 7)
		if b < 0x80 {
			return v, nil
		}
	}
	// Unreachable: loop returns on i == MaxVarintBytes-1.
	return 0, newError(ErrOverflow, start, 0, "varint overflows 64 bits")
}

// ZigZag32 converts a varint payload to the sint32 it encodes.
func ZigZag32(v uint64) int32 { return int32(int64(v>>1) ^ -int64(v&1)) }

// ZigZag64 converts a varint payload to the sint64 it encodes.
func ZigZag64(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// ReadZigZag64 reads a varint and applies the ZigZag transform.
func (r *Reader) ReadZigZag64() (int64, error) {
	v, err := r.ReadVarint()
	if err != nil {
		return 0, err
	}
	return ZigZag64(v), nil
}

// ReadFixed32 reads 4 bytes little-endian.
func (r *Reader) ReadFixed32() (uint32, error) {
	if r.Remaining() < 4 {
		return 0, newError(ErrTruncated, r.off, 0,
			"fixed32 needs 4 bytes, %d remain", r.Remaining())
	}
	b := r.buf[r.off:]
	r.off += 4
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24, nil
}

// ReadFixed64 reads 8 bytes little-endian.
func (r *Reader) ReadFixed64() (uint64, error) {
	if r.Remaining() < 8 {
		return 0, newError(ErrTruncated, r.off, 0,
			"fixed64 needs 8 bytes, %d remain", r.Remaining())
	}
	b := r.buf[r.off:]
	r.off += 8
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56, nil
}

// ReadBytes consumes exactly n bytes, guarding against overruns.
func (r *Reader) ReadBytes(n int) ([]byte, error) {
	if n < 0 || n > r.Remaining() {
		return nil, newError(ErrTruncated, r.off, 0,
			"want %d bytes, %d remain", n, r.Remaining())
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

// ReadLengthDelimited reads a varint length prefix followed by that many
// bytes. A declared length beyond the remaining input is rejected before
// any slice is taken (safe out-of-bounds guard).
func (r *Reader) ReadLengthDelimited() ([]byte, error) {
	start := r.off
	n, err := r.ReadVarint()
	if err != nil {
		return nil, err
	}
	if n > uint64(r.Remaining()) {
		return nil, newError(ErrNegativeLength, start, 0,
			"declared length %d exceeds %d remaining bytes", n, r.Remaining())
	}
	return r.ReadBytes(int(n))
}

// Skip consumes one value of the given wire type without decoding it.
func (r *Reader) Skip(wt WireType) error {
	switch wt {
	case WireVarint:
		_, err := r.ReadVarint()
		return err
	case WireFixed64:
		_, err := r.ReadFixed64()
		return err
	case WireLengthDelimited:
		_, err := r.ReadLengthDelimited()
		return err
	case WireFixed32:
		_, err := r.ReadFixed32()
		return err
	default:
		return newError(ErrInvalidWire, r.off, 0, "cannot skip wire type %d", int(wt))
	}
}

// ReadKey reads a field key (varint) and splits it into field number and
// wire type, validating both.
func (r *Reader) ReadKey() (fieldNum int, wt WireType, err error) {
	start := r.off
	key, err := r.ReadVarint()
	if err != nil {
		return 0, 0, err
	}
	wt = WireType(key & 0x7)
	switch wt {
	case WireVarint, WireFixed64, WireLengthDelimited, WireFixed32:
	default:
		return 0, 0, newError(ErrInvalidWire, start, 0, "invalid wire type %d", int(wt))
	}
	num := key >> 3
	if num == 0 || num > MaxFieldNumber {
		return 0, 0, newError(ErrBadFieldNumber, start, 0, "field number %d out of range", num)
	}
	return int(num), wt, nil
}
