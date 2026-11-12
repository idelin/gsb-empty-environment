package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"
)

// ValidityType is the IPNS validity type enum.
type ValidityType uint32

// ValidityEOL means the Validity field contains an RFC 3339 expiry time.
const ValidityEOL ValidityType = 0

// V2SignaturePrefix is prepended to the DAG-CBOR data before V2 signing.
const V2SignaturePrefix = "ipns-signature:"

// Record is the decoded protobuf representation of an IPNS record.
//
// Signature is the deprecated V1 signature. It is retained so that records can
// be round-tripped, but it is never accepted as a record signature by this
// package. SignatureV2 covers "ipns-signature:" followed by Data.
type Record struct {
	Value        []byte
	Signature    []byte
	ValidityType ValidityType
	Validity     []byte
	Sequence     uint64
	TTL          uint64
	PubKey       []byte
	SignatureV2  []byte
	Data         []byte
}

var (
	// ErrMalformedRecord indicates malformed protobuf bytes.
	ErrMalformedRecord = errors.New("ipns: malformed record")
	// ErrMalformedData indicates malformed or non-DAG-CBOR signed data.
	ErrMalformedData = errors.New("ipns: malformed signed data")
	// ErrMissingV2Signature indicates that no V2 signature is present.
	ErrMissingV2Signature = errors.New("ipns: missing V2 signature")
	// ErrV1Signature indicates a record that only carries a V1 signature.
	ErrV1Signature = errors.New("ipns: V1 signature is not accepted")
	// ErrSignatureInvalid indicates that the injected verifier rejected the
	// signature, or no verifier was supplied.
	ErrSignatureInvalid = errors.New("ipns: V2 signature verification failed")
	// ErrFieldMismatch indicates a mismatch between Data and the protobuf fields.
	ErrFieldMismatch = errors.New("ipns: signed data does not match record fields")
	// ErrInvalidValidityType indicates a validity type other than EOL.
	ErrInvalidValidityType = errors.New("ipns: invalid validity type")
	// ErrInvalidValidity indicates an EOL that is not strict RFC 3339.
	ErrInvalidValidity = errors.New("ipns: invalid RFC 3339 validity")
	// ErrExpired indicates an EOL that is not strictly after the validation time.
	ErrExpired = errors.New("ipns: record is expired")
	// ErrNoVerifier indicates that VerifyOptions did not provide a verifier.
	ErrNoVerifier = errors.New("ipns: no signature verifier supplied")
)

// VerifyOptions configures record validation.
type VerifyOptions struct {
	// Verifier verifies a signature. It must return nil only when signature is
	// a valid V2 signature of data under pubKey. The package does not select or
	// trust keys; callers should bind pubKey to the IPNS name they expect.
	Verifier func(pubKey, data, signature []byte) error

	// Now is the time at which EOL is evaluated. The zero value means time.Now.
	Now time.Time

	// PublicKey overrides Record.PubKey when non-nil. This is useful when the
	// public key is derived from the IPNS name instead of carried in the record.
	PublicKey []byte

	// SignaturePrefix is prepended to Record.Data before it is passed to the
	// verifier. A nil value selects the standard "ipns-signature:" prefix.
	// A non-nil empty slice makes the verifier receive Record.Data unchanged.
	SignaturePrefix []byte
}

// Parse decodes IPNS protobuf bytes. It does not validate signatures or
// apply record-selection policy; use Validate for that.
func Parse(raw []byte) (*Record, error) {
	return parseRecord(raw)
}

// Marshal encodes the record as IPNS protobuf bytes.
func (r *Record) Marshal() ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil record", ErrMalformedRecord)
	}
	return marshalRecord(r), nil
}

// Validate checks a serialized record and returns its decoded form if it is
// acceptable for storage.
//
// The checks are, in order: a V2 signature must be present; the injected
// verifier must accept the V2 signature over the signed payload; Data must be
// a DAG-CBOR map whose Value, Validity, ValidityType, Sequence and TTL exactly
// equal the protobuf fields; ValidityType must be EOL; and the EOL must be a
// future RFC 3339 timestamp.
func Validate(raw []byte, opts VerifyOptions) (*Record, error) {
	r, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if err := r.Validate(opts); err != nil {
		return nil, err
	}
	return r, nil
}

// Validate applies the IPNS validation policy to an already decoded record.
func (r *Record) Validate(opts VerifyOptions) error {
	if r == nil {
		return fmt.Errorf("%w: nil record", ErrMalformedRecord)
	}
	if len(r.SignatureV2) == 0 {
		if len(r.Signature) != 0 {
			return ErrV1Signature
		}
		return ErrMissingV2Signature
	}
	if len(r.Data) == 0 {
		return fmt.Errorf("%w: missing data field", ErrMalformedData)
	}

	pubKey := r.PubKey
	if opts.PublicKey != nil {
		pubKey = opts.PublicKey
	}
	if opts.Verifier == nil {
		return ErrNoVerifier
	}
	prefix := []byte(V2SignaturePrefix)
	if opts.SignaturePrefix != nil {
		prefix = opts.SignaturePrefix
	}
	message := make([]byte, 0, len(prefix)+len(r.Data))
	message = append(message, prefix...)
	message = append(message, r.Data...)
	if err := opts.Verifier(pubKey, message, r.SignatureV2); err != nil {
		return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
	}

	fields, err := decodeData(r.Data)
	if err != nil {
		return err
	}
	if err := r.checkDataFields(fields); err != nil {
		return err
	}

	if r.ValidityType != ValidityEOL {
		return fmt.Errorf("%w: %d", ErrInvalidValidityType, r.ValidityType)
	}
	eol, err := parseEOL(r.Validity)
	if err != nil {
		return err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	if !eol.After(now) {
		return fmt.Errorf("%w: EOL %s is not after %s", ErrExpired, eol.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	}
	return nil
}

func (r *Record) checkDataFields(f *dataFields) error {
	if !bytes.Equal(r.Value, f.Value) {
		return fmt.Errorf("%w: Value", ErrFieldMismatch)
	}
	if !bytes.Equal(r.Validity, f.Validity) {
		return fmt.Errorf("%w: Validity", ErrFieldMismatch)
	}
	if uint64(r.ValidityType) != f.ValidityType {
		return fmt.Errorf("%w: ValidityType", ErrFieldMismatch)
	}
	if r.Sequence != f.Sequence {
		return fmt.Errorf("%w: Sequence", ErrFieldMismatch)
	}
	if r.TTL != f.TTL {
		return fmt.Errorf("%w: TTL", ErrFieldMismatch)
	}
	return nil
}

func parseEOL(validity []byte) (time.Time, error) {
	if len(validity) == 0 {
		return time.Time{}, fmt.Errorf("%w: empty EOL", ErrInvalidValidity)
	}
	t, err := time.Parse(time.RFC3339, string(validity))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %v", ErrInvalidValidity, err)
	}
	return t, nil
}

// EOL returns the record's parsed expiry time.
func (r *Record) EOL() (time.Time, error) {
	if r == nil {
		return time.Time{}, fmt.Errorf("%w: nil record", ErrMalformedRecord)
	}
	return parseEOL(r.Validity)
}

// ShouldReplace reports whether incoming should replace existing.
//
// A higher sequence always wins. With equal sequence, a strictly later EOL
// wins. If sequence and EOL are both equal, the incoming record is dropped.
// Both records are expected to have passed Validate before this comparison is
// used to mutate a store.
func ShouldReplace(existing, incoming *Record) (bool, error) {
	if incoming == nil {
		return false, fmt.Errorf("%w: nil incoming record", ErrMalformedRecord)
	}
	if existing == nil {
		return true, nil
	}
	if incoming.Sequence > existing.Sequence {
		return true, nil
	}
	if incoming.Sequence < existing.Sequence {
		return false, nil
	}
	incomingEOL, err := incoming.EOL()
	if err != nil {
		return false, err
	}
	existingEOL, err := existing.EOL()
	if err != nil {
		return false, err
	}
	return incomingEOL.After(existingEOL), nil
}

func validateValidityType(v uint64) (ValidityType, error) {
	if v > math.MaxUint32 {
		return 0, fmt.Errorf("%w: validity type %d does not fit in uint32", ErrMalformedRecord, v)
	}
	return ValidityType(v), nil
}
