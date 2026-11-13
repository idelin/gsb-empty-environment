package netsec

import "errors"

// ErrRequestFailed is the only error ever returned across the service
// boundary. Identity failures and protocol failures are intentionally
// indistinguishable to callers.
var ErrRequestFailed = errors.New("netsec: request failed")

// Internal errors. These never escape the package boundary; every public
// service method maps them through Opaque.
var (
	errProtocolState    = errors.New("invalid pipe state")
	errIdentityMismatch = errors.New("connection snapshot mismatch")
	errHandleRevoked    = errors.New("impersonation handle revoked or stale")
	errDuplicateRequest = errors.New("duplicate request id")
	errIncompleteFrame  = errors.New("incomplete frame")
	errNoPendingRequest = errors.New("no pending request")
	errCanceled         = errors.New("operation canceled")
)

// Opaque collapses any internal error into ErrRequestFailed so that
// identity errors and protocol errors look identical to the outside.
func Opaque(err error) error {
	if err == nil {
		return nil
	}
	return ErrRequestFailed
}
