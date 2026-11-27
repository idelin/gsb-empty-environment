package webauthn

import "errors"

// Sentinel errors returned by the verifier. Callers can use errors.Is to
// distinguish policy rejections from malformed-input failures.
var (
	// ErrMalformed indicates the CBOR/JSON/binary input could not be parsed.
	ErrMalformed = errors.New("webauthn: malformed input")

	// ErrPolicy indicates an enterprise policy rejected the response
	// (forbidden attestation format, missing UV, cross-origin, ...).
	ErrPolicy = errors.New("webauthn: policy rejected")

	// ErrRPIDHash is returned when authenticatorData.rpIdHash does not equal
	// SHA-256 of the expected relying-party identifier.
	ErrRPIDHash = errors.New("webauthn: rpIdHash mismatch")

	// ErrChallenge is returned when clientDataJSON.challenge differs from the
	// one-time challenge issued by the relying party.
	ErrChallenge = errors.New("webauthn: challenge mismatch")

	// ErrOrigin is returned when clientDataJSON.origin differs from the
	// expected relying-party origin.
	ErrOrigin = errors.New("webauthn: origin mismatch")

	// ErrClientData is returned for type/crossOrigin/clientDataJSON failures.
	ErrClientData = errors.New("webauthn: clientDataJSON invalid")

	// ErrFlags is returned when the authenticatorData flag combination is
	// illegal or does not satisfy the required UP/AT/UV policy.
	ErrFlags = errors.New("webauthn: authenticatorData flags invalid")

	// ErrSignature is returned when an attestation signature does not verify.
	ErrSignature = errors.New("webauthn: attestation signature invalid")

	// ErrCertificate is returned when an attestation certificate fails one of
	// the mandatory FIDO metadata checks (version, basic constraints, OU,
	// AAGUID extension) or cannot be chained to an injected trust root.
	ErrCertificate = errors.New("webauthn: attestation certificate invalid")

	// ErrAAGUID is returned when the attestation certificate AAGUID extension
	// does not match the AAGUID carried in authenticatorData, or the AAGUID is
	// not present in the enterprise allow-list.
	ErrAAGUID = errors.New("webauthn: AAGUID mismatch or not allow-listed")
)
