package netsec

import "errors"

var (
	// ErrGreetingTruncated indicates that fewer than 64 greeting octets
	// were received.
	ErrGreetingTruncated = errors.New("netsec: truncated ZMTP greeting")

	// ErrMalformedGreeting indicates that a greeting cannot be decoded.
	ErrMalformedGreeting = errors.New("netsec: malformed ZMTP greeting")

	// ErrGreetingSignature indicates that the 10-octet ZMTP signature is
	// missing its 0xFF first octet or 0x7F final octet.
	ErrGreetingSignature = errors.New("netsec: invalid ZMTP greeting signature")

	// ErrUnsupportedVersion indicates that the peer's major ZMTP version
	// is lower than 3.
	ErrUnsupportedVersion = errors.New("netsec: peer ZMTP version is below 3.0")

	// ErrInvalidMechanism indicates that a mechanism name is empty, too long,
	// or contains characters that are not allowed by ZMTP.
	ErrInvalidMechanism = errors.New("netsec: invalid ZMTP mechanism name")

	// ErrMechanismPadding indicates that the 20-octet mechanism field has
	// non-zero octets after its NUL padding.
	ErrMechanismPadding = errors.New("netsec: mechanism field has non-zero padding")

	// ErrMechanismMismatch indicates that the peer selected a mechanism other
	// than the one required by the local configuration.
	ErrMechanismMismatch = errors.New("netsec: peer mechanism does not match")

	// ErrMechanismDowngrade indicates that the peer attempted to replace a
	// configured security mechanism with NULL.
	ErrMechanismDowngrade = errors.New("netsec: peer attempted a mechanism downgrade")

	// ErrMalformedAsServer indicates that the as-server octet is neither 0
	// nor 1.
	ErrMalformedAsServer = errors.New("netsec: invalid as-server flag")

	// ErrRoleMismatch indicates that the peer claims the same role as the
	// local endpoint. One endpoint must be the client and the other the
	// server.
	ErrRoleMismatch = errors.New("netsec: peer role does not complement local role")

	// ErrMalformedFrame indicates that a ZMTP frame header or body is invalid.
	ErrMalformedFrame = errors.New("netsec: malformed ZMTP frame")

	// ErrFrameTooLarge indicates that a frame exceeds the configured limit.
	ErrFrameTooLarge = errors.New("netsec: ZMTP frame exceeds size limit")

	// ErrNotCurveMessage indicates that a frame is not a CURVE MESSAGE
	// command.
	ErrNotCurveMessage = errors.New("netsec: frame is not a CURVE MESSAGE command")

	// ErrMalformedCurveMessage indicates that a CURVE MESSAGE command is
	// truncated or missing its message box.
	ErrMalformedCurveMessage = errors.New("netsec: malformed CURVE MESSAGE command")

	// ErrNonceReplay indicates that a CURVE short nonce was reused or went
	// backwards. Nonces must be strictly increasing within a connection.
	ErrNonceReplay = errors.New("netsec: CURVE nonce is not strictly increasing")
)
