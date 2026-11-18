package netsec

import (
	"fmt"
)

const (
	pbFieldValue        = 1
	pbFieldSignatureV1  = 2
	pbFieldValidity     = 3
	pbFieldValidityType = 4
	pbFieldSequence     = 5
	pbFieldTTL          = 6
	pbFieldSignatureV2  = 7
	pbFieldCBORData     = 8
)

type pbEntry struct {
	value        []byte
	signatureV1  []byte
	validity     []byte
	validityType int64
	sequence     uint64
	ttl          uint64
	signatureV2  []byte
	cborData     []byte
}

// parsePBEntry decodes the protobuf wire format used by IPNS records.
// Only the compact deterministic subset emitted for IPNS is needed.
func parsePBEntry(data []byte) (*pbEntry, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty record", ErrMalformedRecord)
	}
	entry := &pbEntry{}
	seen := make(map[int]bool)

	for len(data) > 0 {
		tag, n, err := readVarint(data)
		if err != nil {
			return nil, err
		}
		data = data[n:]

		field := int(tag >> 3)
		wire := int(tag & 0x7)
		if field == 0 {
			return nil, fmt.Errorf("%w: invalid field number 0", ErrMalformedRecord)
		}
		if seen[field] {
			return nil, fmt.Errorf("%w: repeated protobuf field %d", ErrMalformedRecord, field)
		}

		switch wire {
		case 0:
			v, m, err := readVarint(data)
			if err != nil {
				return nil, err
			}
			data = data[m:]
			if err := entry.assignVarint(field, v); err != nil {
				return nil, err
			}
			seen[field] = true
		case 2:
			length, m, err := readVarint(data)
			if err != nil {
				return nil, err
			}
			data = data[m:]
			if length > uint64(len(data)) {
				return nil, fmt.Errorf("%w: truncated length-delimited field %d", ErrMalformedRecord, field)
			}
			raw := data[:length]
			data = data[length:]
			if err := entry.assignBytes(field, raw); err != nil {
				return nil, err
			}
			seen[field] = true
		case 1, 5:
			width := 8
			if wire == 5 {
				width = 4
			}
			if len(data) < width {
				return nil, fmt.Errorf("%w: truncated fixed-size field %d", ErrMalformedRecord, field)
			}
			data = data[width:]
		default:
			return nil, fmt.Errorf("%w: unsupported wire type %d for field %d", ErrMalformedRecord, wire, field)
		}
	}

	return entry, nil
}

func (e *pbEntry) assignVarint(field int, v uint64) error {
	switch field {
	case pbFieldValidityType:
		e.validityType = int64(v)
	case pbFieldSequence:
		e.sequence = v
	case pbFieldTTL:
		e.ttl = v
	default:
		// Unknown varint fields are skipped deterministically.
	}
	return nil
}

func (e *pbEntry) assignBytes(field int, raw []byte) error {
	dup := append([]byte(nil), raw...)
	switch field {
	case pbFieldValue:
		e.value = dup
	case pbFieldSignatureV1:
		e.signatureV1 = dup
	case pbFieldValidity:
		e.validity = dup
	case pbFieldSignatureV2:
		e.signatureV2 = dup
	case pbFieldCBORData:
		e.cborData = dup
	default:
		// Unknown length-delimited fields are skipped deterministically.
	}
	return nil
}

func readVarint(data []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		if i == 9 && b > 1 {
			return 0, 0, fmt.Errorf("%w: varint overflows uint64", ErrMalformedRecord)
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("%w: truncated varint", ErrMalformedRecord)
}
