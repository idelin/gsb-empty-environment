package netsec

import "errors"

// OAuth 2.0 device authorization error codes. CodeOf maps component errors to
// these values so an HTTP layer can return the appropriate token-endpoint
// response.
const (
	CodeAuthorizationPending = "authorization_pending"
	CodeSlowDown             = "slow_down"
	CodeAccessDenied         = "access_denied"
	CodeExpiredToken         = "expired_token"
	CodeInvalidGrant         = "invalid_grant"
	CodeInvalidRequest       = "invalid_request"
	CodeInvalidClient        = "invalid_client"
	CodeServerError          = "server_error"
)

// Error is a protocol error returned by Manager.
type Error struct {
	Code        string
	Description string
}

func (e *Error) Error() string {
	if e.Description == "" {
		return e.Code
	}
	return e.Code + ": " + e.Description
}

// Is allows errors.Is to distinguish the exported sentinel errors even when
// two sentinel errors share the same OAuth code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && e == t
}

var (
	// ErrInvalidRequest indicates a malformed call, such as an empty client ID.
	ErrInvalidRequest = &Error{Code: CodeInvalidRequest, Description: "invalid request"}
	// ErrInvalidClient indicates that the supplied client identifier is invalid.
	ErrInvalidClient = &Error{Code: CodeInvalidClient, Description: "invalid client"}
	// ErrInvalidDeviceCode indicates an unknown, malformed, or unusable device code.
	ErrInvalidDeviceCode = &Error{Code: CodeInvalidGrant, Description: "invalid device code"}
	// ErrClientMismatch indicates that the device code belongs to another client.
	ErrClientMismatch = &Error{Code: CodeInvalidGrant, Description: "device code was not issued to this client"}
	// ErrInvalidUserCode indicates an unknown or malformed user code.
	ErrInvalidUserCode = &Error{Code: CodeInvalidGrant, Description: "invalid user code"}
	// ErrTooManyAttempts indicates that user-code verification is temporarily locked.
	ErrTooManyAttempts = &Error{Code: CodeSlowDown, Description: "too many invalid user code attempts"}

	// ErrAuthorizationPending tells the device to keep polling.
	ErrAuthorizationPending = &Error{Code: CodeAuthorizationPending, Description: "authorization is still pending"}
	// ErrSlowDown tells the device that it polled before the configured interval.
	ErrSlowDown = &Error{Code: CodeSlowDown, Description: "polling too frequently"}
	// ErrPollLimitExceeded indicates that the device exceeded its polling budget.
	ErrPollLimitExceeded = &Error{Code: CodeExpiredToken, Description: "device code poll limit exceeded"}
	// ErrAccessDenied indicates that the user denied the authorization request.
	ErrAccessDenied = &Error{Code: CodeAccessDenied, Description: "authorization was denied"}
	// ErrExpiredToken indicates that the device code or its approval has expired.
	ErrExpiredToken = &Error{Code: CodeExpiredToken, Description: "device code or approval has expired"}
	// ErrAlreadyUsed indicates that the device code has already been redeemed.
	ErrAlreadyUsed = &Error{Code: CodeInvalidGrant, Description: "device code has already been used"}
	// ErrAlreadyApproved indicates that another approval already completed.
	ErrAlreadyApproved = &Error{Code: CodeInvalidGrant, Description: "device code has already been approved"}
	// ErrInvalidSession indicates that the approval session is missing or invalid.
	ErrInvalidSession = &Error{Code: CodeInvalidGrant, Description: "invalid approval session"}
	// ErrTokenGeneration indicates that the configured token issuer failed.
	ErrTokenGeneration = &Error{Code: CodeServerError, Description: "token generation failed"}
)

// CodeOf returns the OAuth error code carried by err, or an empty string when
// err is not a component protocol error.
func CodeOf(err error) string {
	var protocolErr *Error
	if errors.As(err, &protocolErr) {
		return protocolErr.Code
	}
	return ""
}
