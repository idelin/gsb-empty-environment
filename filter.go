// Package netsec provides an unknown-field filter for the gateway's
// protobuf decode path.
//
// A gateway forwards messages between older and newer backend services.
// The newer backend may define internal field numbers that the gateway has
// never heard of; an attacker can set those unknown fields and have the
// gateway forward them verbatim. Filter is invoked while decoding an inbound
// message and either keeps or rejects every top-level field according to the
// caller-supplied policy.
package netsec

import (
	"bytes"
	"errors"
	"fmt"
)

// Protobuf wire types.
const (
	wireVarint        = 0
	wireFixed64       = 1
	wireBytes         = 2
	wireStartGroup    = 3
	wireEndGroup      = 4
	wireFixed32       = 5
	maxFieldNumber    = (1 << 29) - 1
	maxVarintBytes    = 10 // ceil(64 / 7)
	fixed64FieldBytes = 8
	fixed32FieldBytes = 4
)

// maxInt is the largest value representable by the platform's int type.
const maxInt = int(^uint(0) >> 1)

// Sentinel errors returned by FilterBytes. Callers can match them with
// errors.Is; wrapped contextual messages preserve the underlying sentinel.
var (
	// ErrInvalidConfig is returned when the filter policy is inconsistent.
	ErrInvalidConfig = errors.New("netsec: invalid filter configuration")
	// ErrMalformed is returned when the input is not parseable protobuf wire
	// data: truncated fields, non-canonical varints, or unsupported wire
	// types.
	ErrMalformed = errors.New("netsec: malformed protobuf message")
	// ErrFieldNumberZero is returned when a field tag carries field number 0,
	// which protobuf reserves and never emits.
	ErrFieldNumberZero = errors.New("netsec: field number 0 is not allowed")
	// ErrReservedField is returned when an unknown field number falls inside a
	// caller-declared internal reserved range.
	ErrReservedField = errors.New("netsec: unknown field uses a reserved field number")
	// ErrGroupField is returned when a group wire type (3 or 4) is
	// encountered. Groups are deprecated and cannot be size-checked safely.
	ErrGroupField = errors.New("netsec: group wire type is not allowed")
	// ErrFieldTooLarge is returned when an unknown field's total encoded
	// size exceeds the caller byte limit.
	ErrFieldTooLarge = errors.New("netsec: unknown field exceeds the byte limit")
)

// Range is an inclusive field-number range used to mark internal reserved
// numbering that must never pass through the gateway as an unknown field.
type Range struct {
	// Min and Max are inclusive protobuf field numbers. Min must not be zero
	// and Min must be less than or equal to Max.
	Min int
	Max int
}

// Filter is the immutable policy applied by the decode path. Construct one
// with NewFilter; the zero value is not usable.
type Filter struct {
	knownFields   map[int]struct{}
	reserved      []Range
	maxFieldBytes int
}

// NewFilter builds a Filter.
//
//   - knownFields lists the field numbers this gateway build understands.
//     Known fields are always forwarded untouched, even if they happen to
//     overlap a reserved range or exceed the byte limit. A nil map means no
//     field is known.
//   - reservedRanges lists the internal field-number ranges the newer
//     backend uses. An unknown field inside any range is rejected.
//   - maxFieldBytes bounds the total encoded size (tag plus payload) of any
//     single unknown field. Zero means unknown fields have no size limit.
//
// The maps and slices passed in are copied; later mutation by the caller
// does not affect the returned Filter.
func NewFilter(knownFields map[int]struct{}, reservedRanges []Range, maxFieldBytes int) (*Filter, error) {
	if maxFieldBytes < 0 {
		return nil, fmt.Errorf("%w: maxFieldBytes must be >= 0, got %d", ErrInvalidConfig, maxFieldBytes)
	}

	known := make(map[int]struct{}, len(knownFields))
	for fieldNum := range knownFields {
		if fieldNum <= 0 || fieldNum > maxFieldNumber {
			return nil, fmt.Errorf("%w: known field number %d out of range [1,%d]", ErrInvalidConfig, fieldNum, maxFieldNumber)
		}
		known[fieldNum] = struct{}{}
	}

	reserved := make([]Range, len(reservedRanges))
	for idx, r := range reservedRanges {
		if r.Min <= 0 || r.Max > maxFieldNumber || r.Min > r.Max {
			return nil, fmt.Errorf("%w: reserved range %d = [%d,%d] is invalid", ErrInvalidConfig, idx, r.Min, r.Max)
		}
		reserved[idx] = r
	}

	return &Filter{
		knownFields:   known,
		reserved:      reserved,
		maxFieldBytes: maxFieldBytes,
	}, nil
}

// FilterBytes scans one serialized protobuf message and returns the bytes
// that are safe to forward.
//
// Known fields are copied through unchanged. Unknown fields are rejected when
// they use field number 0, a group wire type, a reserved internal field
// number, or more bytes than the configured limit; every other unknown field
// is kept, in its original position, so benign fields added by newer senders
// keep flowing to older and newer backends.
//
// Length-delimited fields are validated at the framing level only: the
// declared length must fit the remaining buffer. Their payload is treated as
// opaque and forwarded verbatim, which keeps valid nested messages and
// non-message bytes (strings, packed repeated values) working across
// versions. A return value of nil with a nil error denotes the empty message.
func (f *Filter) FilterBytes(msg []byte) ([]byte, error) {
	if f == nil {
		return nil, fmt.Errorf("%w: nil *Filter", ErrInvalidConfig)
	}

	var out bytes.Buffer
	// The filtered message can never be larger than the input; preallocating
	// avoids re-growth in the common case where everything is retained.
	out.Grow(len(msg))

	rest := msg
	for len(rest) > 0 {
		fieldStart := len(msg) - len(rest)

		tag, tagLen, err := consumeVarint(rest)
		if err != nil {
			return nil, err
		}
		fieldNum := int(tag >> 3)
		wireType := int(tag & 0x7)

		if fieldNum == 0 {
			return nil, fmt.Errorf("%w at offset %d", ErrFieldNumberZero, fieldStart)
		}
		if fieldNum > maxFieldNumber {
			return nil, fmt.Errorf("%w: field number %d exceeds %d at offset %d", ErrMalformed, fieldNum, maxFieldNumber, fieldStart)
		}

		fieldLen, err := fieldLength(rest[tagLen:], wireType)
		if err != nil {
			return nil, err
		}

		totalLen := tagLen + fieldLen
		if totalLen > len(rest) {
			return nil, fmt.Errorf("%w: truncated field %d at offset %d", ErrMalformed, fieldNum, fieldStart)
		}

		if _, known := f.knownFields[fieldNum]; !known {
			if err := f.rejectUnknown(fieldNum, wireType, totalLen, fieldStart); err != nil {
				return nil, err
			}
		}

		// bytes.Buffer.Write never fails on these sizes.
		out.Write(rest[:totalLen])
		rest = rest[totalLen:]
	}

	return out.Bytes(), nil
}

// fieldLength returns the payload length (everything after the tag) for the
// next field. Group wire types are rejected outright.
func fieldLength(rest []byte, wireType int) (int, error) {
	switch wireType {
	case wireVarint:
		_, n, err := consumeVarint(rest)
		if err != nil {
			return 0, err
		}
		return n, nil
	case wireFixed64:
		return fixed64FieldBytes, nil
	case wireBytes:
		declaredLen, n, err := consumeVarint(rest)
		if err != nil {
			return 0, err
		}
		length := int(declaredLen)
		// On a 32-bit target the cast can wrap; bound against the platform
		// int range rather than relying on the cast itself.
		if declaredLen > uint64(maxInt) {
			return 0, fmt.Errorf("%w: length-delimited field length %d overflows int", ErrMalformed, declaredLen)
		}
		// Full field size = tag + length varint + declared payload; the
		// truncation check in FilterBytes catches a payload that runs past EOF.
		return n + length, nil
	case wireStartGroup, wireEndGroup:
		return 0, ErrGroupField
	case wireFixed32:
		return fixed32FieldBytes, nil
	default:
		// Wire types 6 and 7 do not exist.
		return 0, fmt.Errorf("%w: unsupported wire type %d", ErrMalformed, wireType)
	}
}

// rejectUnknown applies the policy to one field the gateway does not know.
func (f *Filter) rejectUnknown(fieldNum, wireType, totalLen, offset int) error {
	for _, r := range f.reserved {
		if fieldNum >= r.Min && fieldNum <= r.Max {
			return fmt.Errorf("%w %d at offset %d", ErrReservedField, fieldNum, offset)
		}
	}
	if f.maxFieldBytes > 0 && totalLen > f.maxFieldBytes {
		return fmt.Errorf("%w: field %d is %d bytes, limit is %d", ErrFieldTooLarge, fieldNum, totalLen, f.maxFieldBytes)
	}
	return nil
}

// consumeVarint decodes one base-128 varint at the start of b and returns
// the value together with the number of bytes consumed. It rejects
// unterminated and non-minimal (overlong) encodings rather than tolerating
// ambiguity in the wire data.
func consumeVarint(b []byte) (uint64, int, error) {
	var value uint64
	for i := 0; i < maxVarintBytes && i < len(b); i++ {
		byteVal := b[i]
		if i == maxVarintBytes-1 && byteVal > 1 {
			// The 10th byte may only carry the single remaining bit.
			return 0, 0, fmt.Errorf("%w: varint overflows 64 bits", ErrMalformed)
		}
		value |= uint64(byteVal&0x7f) << (7 * i)
		if byteVal&0x80 == 0 {
			// A terminating zero byte past the first position means the
			// same value had a shorter canonical encoding, including the
			// ten-byte form.
			if i > 0 && byteVal&0x7f == 0 {
				return 0, 0, fmt.Errorf("%w: non-canonical varint", ErrMalformed)
			}
			return value, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("%w: truncated varint", ErrMalformed)
}
