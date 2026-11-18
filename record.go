package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"time"
)

// V2SignaturePrefix is the domain-separation prefix prepended to the
// serialized V2 record before its signature is verified.
const V2SignaturePrefix = "/ipns/validator/v2/signature"

// ValidityType mirrors the protobuf ValidityType enum. Only EOL is accepted.
type ValidityType int64

const (
	ValidityTypeEOL ValidityType = 0
)

// Verifier is injected by the caller. It must verify sig against the
// V2-signature input data (V2SignaturePrefix followed by the raw CBOR bytes)
// using the record owner's public key. A non-nil return rejects the record.
type Verifier func(data, sig []byte) error

// Record is the validated, decoded form of an IPNS DHT record.
type Record struct {
	Value        []byte
	Validity     []byte
	ValidityType ValidityType
	Sequence     uint64
	TTL          uint64
	SignatureV2  []byte
	CBORData     []byte

	eol time.Time
}

// EOL returns the parsed Validity timestamp. It is only meaningful after a
// successful Validate, which guarantees Validity is an RFC 3339 string.
func (r *Record) EOL() time.Time { return r.eol }

var (
	ErrMalformedRecord     = errors.New("ipns: malformed record")
	ErrV1Signature         = errors.New("ipns: V1 signatures are not accepted")
	ErrMissingV2Signature  = errors.New("ipns: missing V2 signature")
	ErrInvalidSignature    = errors.New("ipns: invalid V2 signature")
	ErrFieldMismatch       = errors.New("ipns: CBOR and protobuf fields do not match")
	ErrUnsupportedValidity = errors.New("ipns: only EOL validity type is supported")
	ErrInvalidValidity     = errors.New("ipns: validity is not an RFC 3339 timestamp")
	ErrRecordExpired       = errors.New("ipns: record has expired")
	ErrOldSequence         = errors.New("ipns: stored record has a higher sequence")
	ErrEqualSequence       = errors.New("ipns: equal sequence with earlier EOL")
	ErrEqualRecord         = errors.New("ipns: duplicate record with equal sequence and EOL")
)

// Validator parses and validates IPNS records for a DHT record store.
// Signature verification is delegated to the injected Verifier.
type Validator struct {
	Verify Verifier
	Now    func() time.Time
}

// NewValidator builds a Validator using the current wall clock.
func NewValidator(v Verifier) *Validator {
	return &Validator{Verify: v, Now: time.Now}
}

// Validate parses data as a protobuf IPNS record and applies every
// acceptance rule:
//
//   - only V2 signatures are accepted; a present V1 signature is rejected;
//   - the Verifier must accept the signature over the V2 signature input;
//   - Value, Validity, ValidityType, Sequence and TTL in the V2-signed CBOR
//     payload must be byte-for-byte identical to the protobuf fields;
//   - ValidityType must be EOL and Validity must be an RFC 3339 timestamp
//     later than the current time.
func (v *Validator) Validate(data []byte) (*Record, error) {
	if v == nil || v.Verify == nil {
		return nil, errors.New("ipns: no signature verifier configured")
	}

	entry, err := parsePBEntry(data)
	if err != nil {
		return nil, err
	}

	if len(entry.signatureV1) > 0 {
		return nil, ErrV1Signature
	}
	if len(entry.signatureV2) == 0 {
		return nil, ErrMissingV2Signature
	}
	if len(entry.cborData) == 0 {
		return nil, fmt.Errorf("%w: empty V2 CBOR payload", ErrMalformedRecord)
	}

	signed := make([]byte, 0, len(V2SignaturePrefix)+len(entry.cborData))
	signed = append(signed, V2SignaturePrefix...)
	signed = append(signed, entry.cborData...)
	if err := v.Verify(signed, entry.signatureV2); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}

	cborFields, err := parseCBORPayload(entry.cborData)
	if err != nil {
		return nil, err
	}
	if err := compareFields(entry, cborFields); err != nil {
		return nil, err
	}

	if ValidityType(entry.validityType) != ValidityTypeEOL {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedValidity, entry.validityType)
	}

	eol, err := parseRFC3339(entry.validity)
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidValidity, string(entry.validity))
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	if !eol.After(now()) {
		return nil, fmt.Errorf("%w: eol %s", ErrRecordExpired, eol.Format(time.RFC3339Nano))
	}

	return &Record{
		Value:        append([]byte(nil), entry.value...),
		Validity:     append([]byte(nil), entry.validity...),
		ValidityType: ValidityTypeEOL,
		Sequence:     entry.sequence,
		TTL:          entry.ttl,
		SignatureV2:  append([]byte(nil), entry.signatureV2...),
		CBORData:     append([]byte(nil), entry.cborData...),
		eol:          eol,
	}, nil
}

func compareFields(entry *pbEntry, fields map[string]any) error {
	cborValue, ok := fields["Value"].([]byte)
	if !ok || !bytes.Equal(cborValue, entry.value) {
		return fieldMismatch("Value")
	}
	cborValidity, ok := fields["Validity"].([]byte)
	if !ok || !bytes.Equal(cborValidity, entry.validity) {
		return fieldMismatch("Validity")
	}
	cborValidityType, ok := fields["ValidityType"].(uint64)
	if !ok || cborValidityType != uint64(entry.validityType) {
		return fieldMismatch("ValidityType")
	}
	cborSequence, ok := fields["Sequence"].(uint64)
	if !ok || cborSequence != entry.sequence {
		return fieldMismatch("Sequence")
	}
	cborTTL, ok := fields["TTL"].(uint64)
	if !ok || cborTTL != entry.ttl {
		return fieldMismatch("TTL")
	}
	return nil
}

func fieldMismatch(name string) error {
	return fmt.Errorf("%w: %s", ErrFieldMismatch, name)
}

func parseRFC3339(raw []byte) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, string(raw)); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.RFC3339, string(raw))
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}
