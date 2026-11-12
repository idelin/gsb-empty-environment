package netsec

import (
	"errors"
	"fmt"
)

// TURN/STUN error codes used by the authorization checks.
const (
	CodeBadRequest         = 400
	CodeForbidden          = 403
	CodeAllocationMismatch = 437
	CodeStaleNonce         = 438
	CodeWrongCredentials   = 441
)

// ProtocolError is an error that carries the TURN error code a handler must
// put into an ERROR-CODE attribute.
type ProtocolError struct {
	Code   int
	Reason string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("%d %s", e.Code, e.Reason)
}

// Is lets errors.Is match any ProtocolError carrying the same TURN code.
func (e *ProtocolError) Is(target error) bool {
	var other *ProtocolError
	return errors.As(target, &other) && other.Code == e.Code
}

var (
	ErrBadRequest         = &ProtocolError{Code: CodeBadRequest, Reason: "Bad Request"}
	ErrForbiddenPeer      = &ProtocolError{Code: CodeForbidden, Reason: "Forbidden Peer Address"}
	ErrAllocationMismatch = &ProtocolError{Code: CodeAllocationMismatch, Reason: "Allocation Mismatch"}
	ErrStaleNonce         = &ProtocolError{Code: CodeStaleNonce, Reason: "Stale Nonce"}
	ErrWrongCredentials   = &ProtocolError{Code: CodeWrongCredentials, Reason: "Wrong Credentials"}
)

// ErrorCode returns the TURN error code carried by err, or 0 when err does not
// belong to this package.
func ErrorCode(err error) int {
	var protocolErr *ProtocolError
	if errors.As(err, &protocolErr) {
		return protocolErr.Code
	}
	return 0
}

func badRequest(detail string) error {
	if detail == "" {
		return ErrBadRequest
	}
	return &ProtocolError{Code: CodeBadRequest, Reason: "Bad Request: " + detail}
}

func forbiddenPeer(peer string) error {
	return &ProtocolError{Code: CodeForbidden, Reason: "Forbidden Peer Address: " + peer}
}
