package netsec

import "errors"

// Sentinel errors returned by validation and storage. Callers can use
// [errors.Is] to classify a failure; some errors are additionally wrapped
// with descriptive context while still matching these sentinels.
var (
	// ErrMalformedRecord means the protobuf or CBOR wire data cannot be
	// parsed safely, including truncated buffers, duplicate field numbers,
	// unsupported wire encodings or missing required fields.
	ErrMalformedRecord = errors.New("ipns: malformed record")

	// ErrV1SignatureRejected means the record carries a legacy V1
	// signature. V1-only records and dual-signature records are refused.
	ErrV1SignatureRejected = errors.New("ipns: V1 signature is not accepted")

	// ErrMissingV2Signature means the record has no signatureV2 field.
	ErrMissingV2Signature = errors.New("ipns: missing V2 signature")

	// ErrMissingData means the record has no CBOR data payload covered by
	// the V2 signature.
	ErrMissingData = errors.New("ipns: missing signed CBOR data")

	// ErrSignature means the injected V2Verifier rejected the signature
	// over the domain-separated CBOR payload.
	ErrSignature = errors.New("ipns: V2 signature verification failed")

	// ErrFieldMismatch means a field in the signed CBOR payload does not
	// equal the corresponding protobuf field. The wrapped error names the
	// offending field.
	ErrFieldMismatch = errors.New("ipns: CBOR and protobuf field mismatch")

	// ErrUnsupportedValidityType means the validity type is anything other
	// than EOL.
	ErrUnsupportedValidityType = errors.New("ipns: only EOL validity is accepted")

	// ErrInvalidEOL means the EOL value is not an RFC 3339 timestamp.
	ErrInvalidEOL = errors.New("ipns: EOL is not an RFC 3339 timestamp")

	// ErrExpired means the EOL timestamp is not later than the current
	// time observed by the validator clock.
	ErrExpired = errors.New("ipns: record EOL is in the past")

	// ErrRecordSuperseded means an incoming record loses to an already
	// stored record (smaller sequence, or equal sequence with an earlier or
	// equal EOL).
	ErrRecordSuperseded = errors.New("ipns: incoming record is superseded by stored record")

	// ErrNoValidRecord means selection was attempted but none of the
	// candidate records passed full validation.
	ErrNoValidRecord = errors.New("ipns: no valid record among candidates")

	// ErrNilVerifier means a [Validator] was used without configuring a
	// [V2Verifier].
	ErrNilVerifier = errors.New("ipns: nil V2 verifier")
)
