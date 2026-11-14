package netsec

import "errors"

// Sentinel errors returned by the registry. Callers should use errors.Is so
// wrapped error details remain available.
var (
	// ErrInvalidEnvelope means the payload is not a well-formed self-describing
	// envelope, or contains duplicate/unknown envelope keys.
	ErrInvalidEnvelope = errors.New("netsec: invalid envelope")

	// ErrUnknownType means the type identifier is not registered.
	ErrUnknownType = errors.New("netsec: unknown type identifier")

	// ErrVersionMismatch means the type identifier is known but the payload
	// version does not exactly match the registered version.
	ErrVersionMismatch = errors.New("netsec: type version mismatch")

	// ErrFieldNotAllowed means a payload object contains a field that was not
	// explicitly allowed for its registered type.
	ErrFieldNotAllowed = errors.New("netsec: field not allowed")

	// ErrMissingField means a required field is absent.
	ErrMissingField = errors.New("netsec: required field missing")

	// ErrDuplicateField means a JSON object contains the same key more than
	// once. Duplicate keys are rejected rather than resolved last-wins.
	ErrDuplicateField = errors.New("netsec: duplicate field")

	// ErrMaxDepth means the payload exceeds the configured nesting limit.
	ErrMaxDepth = errors.New("netsec: maximum nesting depth exceeded")

	// ErrCycle means object references form a cycle.
	ErrCycle = errors.New("netsec: cyclic reference")

	// ErrInvalidReference means a $ref value is malformed, escapes the data
	// section, or points at a missing location.
	ErrInvalidReference = errors.New("netsec: invalid reference")

	// ErrValidation means post-decode range or cross-field validation failed.
	ErrValidation = errors.New("netsec: post-decode validation failed")

	// ErrDuplicateType means a type identifier or Go type was registered twice.
	ErrDuplicateType = errors.New("netsec: duplicate type registration")

	// ErrInvalidSpec means a TypeSpec is unsafe or internally inconsistent.
	ErrInvalidSpec = errors.New("netsec: invalid type specification")

	// ErrUnsupportedFieldType means a field's Go type cannot be decoded safely
	// by the allowlist decoder.
	ErrUnsupportedFieldType = errors.New("netsec: unsupported field type")

	// ErrUnregisteredFieldType means a nested struct field has no registered
	// allowlist specification.
	ErrUnregisteredFieldType = errors.New("netsec: unregistered nested field type")

	// ErrPayloadTooLarge means the encoded payload exceeds the configured
	// byte limit.
	ErrPayloadTooLarge = errors.New("netsec: payload too large")

	// ErrTypeMismatch is returned by DecodeAs when the decoded allowlisted
	// value is not the requested concrete type.
	ErrTypeMismatch = errors.New("netsec: decoded type mismatch")

	// ErrUnregisteredType is returned by Encode for a Go value whose type is
	// not registered.
	ErrUnregisteredType = errors.New("netsec: unregistered Go type")
)

// Compatibility aliases for callers that prefer descriptive names.
var (
	ErrUnknownField        = ErrFieldNotAllowed
	ErrNestingTooDeep      = ErrMaxDepth
	ErrCyclicReference     = ErrCycle
	ErrInvalidFieldValue   = ErrInvalidEnvelope
	ErrUnsupportedType     = ErrUnsupportedFieldType
	ErrVersionNotMatching  = ErrVersionMismatch
	ErrUnknownTypeID       = ErrUnknownType
	ErrTypeVersionMismatch = ErrVersionMismatch
	ErrDepthExceeded       = ErrMaxDepth
	ErrRefCycle            = ErrCycle
	ErrFieldUnknown        = ErrFieldNotAllowed
)
