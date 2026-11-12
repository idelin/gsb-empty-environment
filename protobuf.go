package netsec

import (
	"encoding/binary"
	"fmt"
	"math"
)

const (
	fieldValue        = 1
	fieldSignature    = 2
	fieldValidityType = 3
	fieldValidity     = 4
	fieldSequence     = 5
	fieldTTL          = 6
	fieldPubKey       = 7
	fieldSignatureV2  = 8
	fieldData         = 9
)

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
)

func parseRecord(raw []byte) (*Record, error) {
	r := &Record{}
	seen := make(map[int]struct{})
	for off := 0; off < len(raw); {
		key, n := binary.Uvarint(raw[off:])
		if n <= 0 {
			return nil, fmt.Errorf("%w: invalid field key", ErrMalformedRecord)
		}
		off += n
		number := int(key >> 3)
		wire := byte(key & 7)
		if number == 0 {
			return nil, fmt.Errorf("%w: field number 0", ErrMalformedRecord)
		}
		if key>>3 > math.MaxInt32 {
			return nil, fmt.Errorf("%w: field number out of range", ErrMalformedRecord)
		}
		if _, ok := seen[number]; ok && number >= fieldValue && number <= fieldData {
			return nil, fmt.Errorf("%w: duplicate field %d", ErrMalformedRecord, number)
		}
		if want, known := fieldWireType(number); known && wire != want {
			return nil, fmt.Errorf("%w: field %d has wire type %d, want %d", ErrMalformedRecord, number, wire, want)
		}

		switch wire {
		case wireVarint:
			v, n := binary.Uvarint(raw[off:])
			if n <= 0 {
				return nil, fmt.Errorf("%w: invalid varint in field %d", ErrMalformedRecord, number)
			}
			off += n
			switch number {
			case fieldValidityType:
				vt, err := validateValidityType(v)
				if err != nil {
					return nil, err
				}
				r.ValidityType = vt
			case fieldSequence:
				r.Sequence = v
			case fieldTTL:
				r.TTL = v
			}
		case wireBytes:
			length, n := binary.Uvarint(raw[off:])
			if n <= 0 {
				return nil, fmt.Errorf("%w: invalid length in field %d", ErrMalformedRecord, number)
			}
			off += n
			if length > uint64(len(raw)-off) {
				return nil, fmt.Errorf("%w: field %d overruns record", ErrMalformedRecord, number)
			}
			value := raw[off : off+int(length)]
			off += int(length)
			switch number {
			case fieldValue:
				r.Value = value
			case fieldSignature:
				r.Signature = value
			case fieldValidity:
				r.Validity = value
			case fieldPubKey:
				r.PubKey = value
			case fieldSignatureV2:
				r.SignatureV2 = value
			case fieldData:
				r.Data = value
			}
		case wireFixed64:
			if len(raw)-off < 8 {
				return nil, fmt.Errorf("%w: truncated fixed64 in field %d", ErrMalformedRecord, number)
			}
			off += 8
		case wireFixed32:
			if len(raw)-off < 4 {
				return nil, fmt.Errorf("%w: truncated fixed32 in field %d", ErrMalformedRecord, number)
			}
			off += 4
		default:
			return nil, fmt.Errorf("%w: unsupported wire type %d", ErrMalformedRecord, wire)
		}
		seen[number] = struct{}{}
	}
	return r, nil
}

func fieldWireType(number int) (byte, bool) {
	switch number {
	case fieldValue, fieldSignature, fieldValidity, fieldPubKey, fieldSignatureV2, fieldData:
		return wireBytes, true
	case fieldValidityType, fieldSequence, fieldTTL:
		return wireVarint, true
	default:
		return 0, false
	}
}

func marshalRecord(r *Record) []byte {
	var out []byte
	out = appendBytesField(out, fieldValue, r.Value)
	out = appendBytesField(out, fieldSignature, r.Signature)
	if r.ValidityType != 0 {
		out = appendVarintField(out, fieldValidityType, uint64(r.ValidityType))
	}
	out = appendBytesField(out, fieldValidity, r.Validity)
	if r.Sequence != 0 {
		out = appendVarintField(out, fieldSequence, r.Sequence)
	}
	if r.TTL != 0 {
		out = appendVarintField(out, fieldTTL, r.TTL)
	}
	out = appendBytesField(out, fieldPubKey, r.PubKey)
	out = appendBytesField(out, fieldSignatureV2, r.SignatureV2)
	out = appendBytesField(out, fieldData, r.Data)
	return out
}

func appendBytesField(out []byte, number int, value []byte) []byte {
	if len(value) == 0 {
		return out
	}
	out = appendVarint(out, uint64(number<<3|wireBytes))
	out = appendVarint(out, uint64(len(value)))
	return append(out, value...)
}

func appendVarintField(out []byte, number int, value uint64) []byte {
	out = appendVarint(out, uint64(number<<3|wireVarint))
	return appendVarint(out, value)
}

func appendVarint(out []byte, v uint64) []byte {
	return append(out, encodeVarint(v)...)
}

func encodeVarint(v uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], v)
	return buf[:n]
}
