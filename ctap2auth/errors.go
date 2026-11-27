package ctap2auth

import "errors"

// CTAP 2.1 status codes (FIDO CTAP §6.3 "CTAP Status Codes").
const (
	StatusMissingParameter   = 0x14
	StatusOperationDenied    = 0x27
	StatusPinInvalid         = 0x31
	StatusPinBlocked         = 0x32
	StatusPinAuthInvalid     = 0x33
	StatusPinAuthBlocked     = 0x34
	StatusPinNotSet          = 0x35
	StatusPinRequired        = 0x36
	StatusPinPolicyViolation = 0x37
)

// CTAPError carries a CTAP2 status code so callers can serialize it directly on
// the CBOR wire. Sentinel values are comparable pointer errors and work with
// errors.Is / errors.As.
type CTAPError struct {
	Code byte
	Msg  string
}

func (e *CTAPError) Error() string {
	return "ctap2auth: ctap status 0x" + hexByte(e.Code) + ": " + e.Msg
}

func hexByte(b byte) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[b>>4], digits[b&0x0f]})
}

// Sentinel CTAP status errors.
var (
	ErrMissingParameter = &CTAPError{Code: StatusMissingParameter, Msg: "missing mandatory parameter"}
	ErrOperationDenied  = &CTAPError{Code: StatusOperationDenied, Msg: "operation denied by policy"}

	ErrPinInvalid         = &CTAPError{Code: StatusPinInvalid, Msg: "PIN mismatch"}
	ErrPinBlocked         = &CTAPError{Code: StatusPinBlocked, Msg: "PIN retries exhausted; PIN is permanently blocked"}
	ErrPinAuthInvalid     = &CTAPError{Code: StatusPinAuthInvalid, Msg: "PIN/UV auth parameter or token is invalid"}
	ErrPinAuthBlocked     = &CTAPError{Code: StatusPinAuthBlocked, Msg: "three consecutive PIN/UV auth failures since power-up; authenticator blocked"}
	ErrPinNotSet          = &CTAPError{Code: StatusPinNotSet, Msg: "no PIN has been provisioned"}
	ErrPinRequired        = &CTAPError{Code: StatusPinRequired, Msg: "a PIN is required for this operation"}
	ErrPinPolicyViolation = &CTAPError{Code: StatusPinPolicyViolation, Msg: "PIN policy violation (must be 4-63 Unicode code points)"}
)

// Local, non-wire hardening errors.
var (
	ErrInvalidPoint         = errors.New("ctap2auth: invalid, non-curve or low-order P-256 public point")
	ErrInvalidSharedSecret  = errors.New("ctap2auth: ECDH shared secret Z must be exactly 32 bytes")
	ErrInvalidKeyLength     = errors.New("ctap2auth: symmetric key must be exactly 32 bytes")
	ErrInvalidIV            = errors.New("ctap2auth: IV must be 16 random bytes; all-zero/non-random IV is rejected")
	ErrInvalidCiphertext    = errors.New("ctap2auth: ciphertext is empty or not AES block aligned")
	ErrBadPadding           = errors.New("ctap2auth: invalid PKCS#7 padding")
	ErrTruncatedMAC         = errors.New("ctap2auth: PIN/UV auth param must be a full 32-byte HMAC-SHA-256")
	ErrProtocolOneDowngrade = errors.New("ctap2auth: rejected 16-byte truncated MAC (PIN/UV Auth Protocol One downgrade)")

	ErrPinNotConfigured = errors.New("ctap2auth: no PIN provisioned on this authenticator")
	ErrUnknownToken     = errors.New("ctap2auth: unknown or revoked pinUvAuthToken")
	ErrPermissionDenied = errors.New("ctap2auth: pinUvAuthToken does not carry the required permission")
	ErrRPIDMismatch     = errors.New("ctap2auth: pinUvAuthToken is bound to a different RP ID (cross-site reuse denied)")
	ErrTokenNotRPBound  = errors.New("ctap2auth: makeCredential/getAssertion require an RP-bound pinUvAuthToken")
)
