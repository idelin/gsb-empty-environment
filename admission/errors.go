package admission

import (
	"errors"
	"fmt"
)

// Code is a stable, transport-independent rejection code. The embedding
// gRPC layer maps these onto the corresponding google.golang.org/grpc/codes
// values (PermissionDenied, InvalidArgument, FailedPrecondition,
// AlreadyExists, Internal).
type Code string

const (
	CodePermissionDenied   Code = "permission_denied"
	CodeInvalidArgument    Code = "invalid_argument"
	CodeFailedPrecondition Code = "failed_precondition"
	CodeAlreadyExists      Code = "already_exists"
	CodeInternal           Code = "internal"
)

// Sentinel admission failures. Use errors.Is to match on the sentinel and
// CodeOf to recover the stable Code.
var (
	// ErrPermissionDenied: the writer is not trusted (for example a PR build).
	ErrPermissionDenied = &AdmissionError{Code: CodePermissionDenied, Reason: "untrusted_writer"}
	// ErrInvalidDigest: a digest/resource name is malformed (hash not 64
	// lowercase hex chars, negative size, bad resource path).
	ErrInvalidDigest = &AdmissionError{Code: CodeInvalidArgument, Reason: "invalid_digest"}
	// ErrDigestMismatch: the actual sha256 of the uploaded bytes differs
	// from the digest claimed in the resource name.
	ErrDigestMismatch = &AdmissionError{Code: CodeInvalidArgument, Reason: "digest_mismatch"}
	// ErrSizeMismatch: the uploaded byte count differs from the declared size.
	ErrSizeMismatch = &AdmissionError{Code: CodeInvalidArgument, Reason: "size_mismatch"}
	// ErrMissingBlob: an ActionResult references a digest that is absent
	// from CAS or whose stored size does not match.
	ErrMissingBlob = &AdmissionError{Code: CodeFailedPrecondition, Reason: "missing_blob"}
	// ErrConflict: an action digest already stores a different ActionResult.
	ErrConflict = &AdmissionError{Code: CodeAlreadyExists, Reason: "action_result_conflict"}
	// ErrInternal: a downstream store failure.
	ErrInternal = &AdmissionError{Code: CodeInternal, Reason: "internal_error"}
)

// AdmissionError is the typed error returned by every admission decision.
type AdmissionError struct {
	Code   Code
	Reason string
	Op     string
	Err    error
}

func (e *AdmissionError) Error() string {
	msg := "admission: " + string(e.Code)
	if e.Op != "" {
		msg += " " + e.Op
	}
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	return msg
}

func (e *AdmissionError) Unwrap() error { return e.Err }

// Is matches on Code and, when the target carries a Reason, on Reason too.
func (e *AdmissionError) Is(target error) bool {
	var t *AdmissionError
	if !errors.As(target, &t) {
		return false
	}
	if t.Code != e.Code {
		return false
	}
	return t.Reason == "" || t.Reason == e.Reason
}

// CodeOf extracts the stable Code of an admission error, or CodeInternal.
func CodeOf(err error) Code {
	var ae *AdmissionError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return CodeInternal
}

func fail(op string, sentinel *AdmissionError, format string, args ...any) error {
	return &AdmissionError{
		Code:   sentinel.Code,
		Reason: sentinel.Reason,
		Op:     op,
		Err:    fmt.Errorf(format, args...),
	}
}
