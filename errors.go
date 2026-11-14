package netsec

import (
	"errors"
	"strconv"
)

// Sentinel errors that describe why an import row was rejected. Use
// errors.Is against the Code of a Violation.
var (
	ErrDuplicateKey     = errors.New("duplicate business key within import batch")
	ErrCrossTenant      = errors.New("record belongs to a different tenant than the importing principal")
	ErrPermissionDenied = errors.New("principal lacks the required permission")
	ErrInvalidRecord    = errors.New("record failed validation")
)

// Violation explains the rejection of a single import row.
type Violation struct {
	Index       int
	TenantID    string
	BusinessKey string
	Code        error
	Detail      string
}

// Error implements the error interface.
func (v Violation) Error() string {
	key := v.BusinessKey
	if key == "" {
		key = "<empty>"
	}
	if v.Detail != "" {
		return "import row " + strconv.Itoa(v.Index) + " [" + v.TenantID + "/" + key + "]: " +
			v.Code.Error() + ": " + v.Detail
	}
	return "import row " + strconv.Itoa(v.Index) + " [" + v.TenantID + "/" + key + "]: " + v.Code.Error()
}

// Unwrap allows errors.Is(violation, sentinel) checks.
func (v Violation) Unwrap() error {
	return v.Code
}
