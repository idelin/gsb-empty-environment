package netsec

import (
	"bytes"
	"fmt"
	"time"
)

// V2Verifier performs the cryptographic check of an IPNS V2 signature.
//
// key is the DHT record key the record was stored under (typically the
// /ipns/<peer-id> key), signed is the exact domain-separated payload
// ([V2SignaturePrefix] followed by Record.Data), and signature is
// Record.V2Signature. Implementations resolve the public key bound to key
// (for example through the embedded public key field or the peer ID) and
// run the appropriate libp2p signature check.
//
// A non-nil error means the signature is invalid and the record is rejected.
type V2Verifier func(key string, signed, signature []byte) error

// Config configures a [Validator].
type Config struct {
	// Verify checks V2 signatures. Required.
	Verify V2Verifier

	// Now returns the current time used for EOL checks. When nil,
	// [time.Now] is used. Tests can inject a fixed clock.
	Now func() time.Time
}

// Validator turns raw IPNS record bytes into fully validated [Record]
// values for a DHT record store.
type Validator struct {
	verify V2Verifier
	now    func() time.Time
}

// NewValidator builds a [Validator] from the supplied configuration.
func NewValidator(cfg Config) *Validator {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Validator{verify: cfg.Verify, now: now}
}

// Validate parses raw, enforces every IPNS acceptance rule and returns the
// parsed record with its parsed EOL attached.
//
// The checks run in order:
//  1. protobuf structure;
//  2. no legacy V1 signature and a present V2 signature;
//  3. injected verification of signatureV2 over the prefixed CBOR data;
//  4. strict CBOR structure and exact field-by-field equality with the
//     protobuf copy;
//  5. ValidityType == EOL and an RFC 3339 Validity;
//  6. EOL strictly later than the validator clock.
func (v *Validator) Validate(key string, raw []byte) (*Record, error) {
	if v == nil || v.verify == nil {
		return nil, ErrNilVerifier
	}

	record, err := ParseRecord(raw)
	if err != nil {
		return nil, err
	}

	if record.hasV1Signature {
		return nil, ErrV1SignatureRejected
	}
	if len(record.V2Signature) == 0 {
		return nil, ErrMissingV2Signature
	}
	if len(record.Data) == 0 {
		return nil, ErrMissingData
	}

	signed := make([]byte, 0, len(V2SignaturePrefix)+len(record.Data))
	signed = append(signed, V2SignaturePrefix...)
	signed = append(signed, record.Data...)
	if err := v.verify(key, signed, record.V2Signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignature, err)
	}

	cborRecord, err := parseCBOR(record.Data)
	if err != nil {
		return nil, err
	}
	if err := checkConsistency(record, cborRecord); err != nil {
		return nil, err
	}

	if record.ValidityType != ValidityEOL {
		return nil, fmt.Errorf("%w: got %d", ErrUnsupportedValidityType, record.ValidityType)
	}

	eol, err := time.Parse(time.RFC3339, string(record.Validity))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidEOL, err)
	}
	record.eol = eol

	if !eol.After(v.now()) {
		return nil, ErrExpired
	}
	return record, nil
}

// checkConsistency enforces that the signed CBOR copy and the protobuf copy
// of every policy-relevant field are identical. Comparing the raw byte
// slices for Value and Validity also rules out re-encoding tricks.
func checkConsistency(record *Record, signed *cborData) error {
	if !bytes.Equal(record.Value, signed.Value) {
		return fmt.Errorf("%w: Value", ErrFieldMismatch)
	}
	if !bytes.Equal(record.Validity, signed.Validity) {
		return fmt.Errorf("%w: Validity", ErrFieldMismatch)
	}
	if record.ValidityType != signed.ValidityType {
		return fmt.Errorf("%w: ValidityType", ErrFieldMismatch)
	}
	if record.Sequence != signed.Sequence {
		return fmt.Errorf("%w: Sequence", ErrFieldMismatch)
	}
	if record.TTL != signed.TTL {
		return fmt.Errorf("%w: TTL", ErrFieldMismatch)
	}
	return nil
}

// EOL returns the parsed end-of-life timestamp. It is available after a
// successful [Validator.Validate].
func (r *Record) EOL() time.Time { return r.eol }
