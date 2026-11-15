package netsec

import (
	"errors"
	"fmt"
)

// FieldKind describes how a field's wire length is determined.
type FieldKind int

const (
	// Fixed: the field occupies exactly Spec.Max bytes.
	Fixed FieldKind = iota
	// LLVAR: 1-byte length prefix, then up to Spec.Max bytes of data.
	LLVAR
	// LLLVAR: 2-byte big-endian length prefix, then up to Spec.Max bytes of data.
	LLLVAR
)

// FieldSpec describes one ISO 8583 field the caller recognises.
// Max is the exact byte count for Fixed fields and the upper bound
// for LLVAR/LLLVAR payloads.
type FieldSpec struct {
	Kind FieldKind
	Max  int
}

// Spec is the caller-supplied admission policy: which fields are
// defined, their wire shape, and which message types may carry a
// PIN block (field 52).
type Spec struct {
	Fields map[int]FieldSpec
	// PINAllowedMTIs lists the only message type indicators in which
	// field 52 is permitted. Any other MTI carrying bit 52 is rejected.
	PINAllowedMTIs map[string]bool
}

// PINBlock is the parsed field 52 payload. Its log/string forms are
// deliberately blank so PIN material can never leak into logs.
type PINBlock [8]byte

func (PINBlock) String() string   { return "" }
func (PINBlock) GoString() string { return "" }

// Message is the result of a successful admission check.
type Message struct {
	MTI    string
	Fields map[int][]byte
	// PIN is set only when field 52 was present and admitted.
	PIN *PINBlock
}

var (
	ErrUndefinedField = errors.New("netsec: bitmap sets undefined field")
	ErrLengthOverflow = errors.New("netsec: field length exceeds remaining bytes or field limit")
	ErrPINNotAllowed  = errors.New("netsec: PIN block not permitted for this message type")
	ErrPINLength      = errors.New("netsec: PIN block must be exactly 8 bytes")
	ErrTruncated      = errors.New("netsec: message too short")
)

// Admit validates the bitmap and field layout of a raw ISO 8583
// message body (bitmap + field data, MTI supplied separately) against
// the caller's spec. It never echoes field values in errors or logs.
func (s *Spec) Admit(mti string, body []byte) (*Message, error) {
	if len(body) < 8 {
		return nil, ErrTruncated
	}
	bitmap := body[:8]
	rest := body[8:]
	if bitmap[0]&0x80 != 0 {
		// Bit 1 set: a secondary bitmap follows.
		if len(rest) < 8 {
			return nil, ErrTruncated
		}
		bitmap = append(append([]byte{}, bitmap...), rest[:8]...)
		rest = rest[8:]
	}

	msg := &Message{MTI: mti, Fields: make(map[int][]byte)}
	for bit := 2; bit <= len(bitmap)*8; bit++ {
		if bitmap[(bit-1)/8]&(0x80>>uint((bit-1)%8)) == 0 {
			continue
		}
		fs, ok := s.Fields[bit]
		if !ok {
			return nil, fmt.Errorf("%w: bit %d", ErrUndefinedField, bit)
		}
		value, n, err := readField(fs, rest)
		if err != nil {
			return nil, err
		}
		if bit == 52 {
			if !s.PINAllowedMTIs[mti] {
				return nil, fmt.Errorf("%w: mti %q", ErrPINNotAllowed, mti)
			}
			if len(value) != 8 {
				return nil, fmt.Errorf("%w: got %d", ErrPINLength, len(value))
			}
			pin := PINBlock{}
			copy(pin[:], value)
			msg.PIN = &pin
			// Never retain the raw PIN bytes in the generic field map.
			msg.Fields[bit] = nil
		} else {
			msg.Fields[bit] = value
		}
		rest = rest[n:]
	}
	return msg, nil
}

// readField consumes one field from buf per its spec, enforcing both
// the remaining-byte bound and the field's own maximum.
func readField(fs FieldSpec, buf []byte) (value []byte, consumed int, err error) {
	if fs.Max < 0 {
		return nil, 0, fmt.Errorf("%w: negative field limit", ErrLengthOverflow)
	}
	switch fs.Kind {
	case Fixed:
		if fs.Max > len(buf) {
			return nil, 0, fmt.Errorf("%w: fixed %d > remaining %d", ErrLengthOverflow, fs.Max, len(buf))
		}
		return buf[:fs.Max], fs.Max, nil
	case LLVAR, LLLVAR:
		prefix := 1
		if fs.Kind == LLLVAR {
			prefix = 2
		}
		if len(buf) < prefix {
			return nil, 0, ErrTruncated
		}
		declared := int(buf[0])
		if prefix == 2 {
			declared = declared<<8 | int(buf[1])
		}
		if declared > fs.Max {
			return nil, 0, fmt.Errorf("%w: declared %d > limit %d", ErrLengthOverflow, declared, fs.Max)
		}
		if declared > len(buf)-prefix {
			return nil, 0, fmt.Errorf("%w: declared %d > remaining %d", ErrLengthOverflow, declared, len(buf)-prefix)
		}
		return buf[prefix : prefix+declared], prefix + declared, nil
	default:
		return nil, 0, fmt.Errorf("%w: unknown field kind", ErrUndefinedField)
	}
}
