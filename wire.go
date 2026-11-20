package netsec

import (
	"errors"
	"time"
)

// Protobuf field numbers of ipns.IpnsEntry. Only these fields are
// understood; every other field number is skipped per protobuf forward
// compatibility rules.
const (
	fieldValue        = 1 // bytes
	fieldSignature    = 2 // bytes, legacy V1 signature
	fieldValidityType = 3 // enum (varint)
	fieldValidity     = 4 // bytes
	fieldSequence     = 5 // uint64 (varint)
	fieldTTL          = 6 // uint64 (varint)
	fieldPublicKey    = 7 // bytes
	fieldV2Signature  = 8 // bytes
	fieldData         = 9 // bytes, CBOR map signed by signatureV2
)

// Validity type values of ipns.IpnsEntry.ValidityType. Only EOL is allowed.
const (
	ValidityEOL = 0
)

// V2SignaturePrefix is the libp2p domain-separation prefix prepended to the
// CBOR data before the V2 signature is created or verified:
//
//	0xFF, length-prefixed "ipns-signature:", 0x01
//
// It is defined by the IPNS record specification and shared by every
// compliant implementation.
var V2SignaturePrefix = []byte{
	0xff,
	0x01, 0x00, 0x70, // multivarint length 112 (0x70)
	'i', 'p', 'n', 's', '-', 's', 'i', 'g', 'n', 'a', 't', 'u', 'r', 'e', ':',
	0x01,
}

// Record is the parsed view of an ipns.IpnsEntry protobuf message. Slices
// reference a private copy of the input so callers cannot mutate the parsed
// record through the original buffer.
type Record struct {
	Value        []byte
	Validity     []byte
	ValidityType uint64
	Sequence     uint64
	TTL          uint64
	V1Signature  []byte
	V2Signature  []byte
	Data         []byte

	hasV1Signature bool

	raw []byte
	eol time.Time
}

// Raw returns the original protobuf bytes the record was parsed from.
func (r *Record) Raw() []byte { return r.raw }

// ParseRecord decodes an IPNS protobuf record without performing any
// signature or semantic checks. Repeated known field numbers are rejected so
// that "last write wins" protobuf semantics cannot be used to smuggle a
// second value past later checks.
func ParseRecord(b []byte) (*Record, error) {
	raw := append([]byte(nil), b...)
	buf := raw

	r := &Record{raw: raw}
	seen := make(map[uint64]bool)

	for len(buf) > 0 {
		tag, n, err := readUvarint(buf)
		if err != nil {
			return nil, err
		}
		buf = buf[n:]

		field := tag >> 3
		wire := tag & 0x7

		switch wire {
		case 0: // varint
			v, n, err := readUvarint(buf)
			if err != nil {
				return nil, err
			}
			buf = buf[n:]
			if err := r.setVarint(field, v, seen); err != nil {
				return nil, err
			}
		case 2: // length-delimited
			length, n, err := readUvarint(buf)
			if err != nil {
				return nil, err
			}
			buf = buf[n:]
			if length > uint64(len(buf)) {
				return nil, ErrMalformedRecord
			}
			chunk := buf[:length]
			buf = buf[length:]
			if err := r.setBytes(field, chunk, seen); err != nil {
				return nil, err
			}
		default:
			// Groups (wire 3/4), fixed 32-bit and fixed 64-bit values are
			// never used by IpnsEntry.
			return nil, ErrMalformedRecord
		}
	}

	required := []uint64{fieldValue, fieldValidityType, fieldValidity, fieldSequence, fieldData}
	for _, field := range required {
		if !seen[field] {
			return nil, ErrMalformedRecord
		}
	}
	return r, nil
}

func (r *Record) setVarint(field, value uint64, seen map[uint64]bool) error {
	switch field {
	case fieldValidityType, fieldSequence, fieldTTL:
		if seen[field] {
			return ErrMalformedRecord
		}
		seen[field] = true
	default:
		// Unknown varint fields are skipped.
		return nil
	}

	switch field {
	case fieldValidityType:
		r.ValidityType = value
	case fieldSequence:
		r.Sequence = value
	case fieldTTL:
		r.TTL = value
	}
	return nil
}

func (r *Record) setBytes(field uint64, chunk []byte, seen map[uint64]bool) error {
	switch field {
	case fieldValue, fieldSignature, fieldValidity, fieldV2Signature, fieldData:
		if seen[field] {
			return ErrMalformedRecord
		}
		seen[field] = true
	default:
		// Unknown length-delimited fields (including publicKey handling
		// decisions) are skipped.
		return nil
	}

	switch field {
	case fieldValue:
		r.Value = chunk
	case fieldSignature:
		// Presence, not content, is what the V1 rejection policy needs.
		r.hasV1Signature = true
		r.V1Signature = chunk
	case fieldValidity:
		r.Validity = chunk
	case fieldV2Signature:
		r.V2Signature = chunk
	case fieldData:
		r.Data = chunk
	}
	return nil
}

func readUvarint(buf []byte) (uint64, int, error) {
	var x uint64
	var shift uint
	for i, c := range buf {
		if i >= 10 {
			return 0, 0, ErrMalformedRecord
		}
		if i == 9 && c > 1 {
			return 0, 0, ErrMalformedRecord
		}
		x |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return x, i + 1, nil
		}
		shift += 7
	}
	return 0, 0, errors.Join(ErrMalformedRecord, errors.New("truncated varint"))
}
