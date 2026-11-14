package netsec

import (
	"errors"
	"fmt"
)

// Status is the NFS-facing outcome class for an access decision.
type Status uint8

const (
	StatusOK Status = iota
	StatusStale
	StatusBadHandle
	StatusAccess
	StatusNotFound
	StatusInvalid
	StatusIO
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusStale:
		return "STALE"
	case StatusBadHandle:
		return "BADHANDLE"
	case StatusAccess:
		return "ACCESS"
	case StatusNotFound:
		return "NOTFOUND"
	case StatusInvalid:
		return "INVALID"
	case StatusIO:
		return "IO"
	default:
		return fmt.Sprintf("status(%d)", uint8(s))
	}
}

// Class sentinels.  Specific errors below match these via errors.Is, so a
// server can map an error to an NFS status without matching every cause.
var (
	ErrStale     = errors.New("netsec: stale handle")
	ErrDenied    = errors.New("netsec: access denied")
	ErrBadHandle = errors.New("netsec: bad handle")
)

// Specific causes.  They are deliberately stable, exported sentinels so a
// caller can distinguish export replacement from rollback and object reuse.
var (
	ErrMalformed             = &AccessError{Status: StatusBadHandle, Kind: ErrBadHandle, Detail: "malformed handle"}
	ErrHandleTampered        = &AccessError{Status: StatusBadHandle, Kind: ErrBadHandle, Detail: "handle authentication failed"}
	ErrWrongServer           = &AccessError{Status: StatusBadHandle, Kind: ErrBadHandle, Detail: "handle bound to another server instance"}
	ErrUnsupportedVersion    = &AccessError{Status: StatusBadHandle, Kind: ErrBadHandle, Detail: "unsupported handle format version"}
	ErrUnknownKey            = &AccessError{Status: StatusBadHandle, Kind: ErrBadHandle, Detail: "unknown handle key"}
	ErrExportNotFound        = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "export not found"}
	ErrExportInactive        = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "export is not active"}
	ErrStaleExport           = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "export was replaced or deleted"}
	ErrStaleSnapshot         = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "export was rolled back to a snapshot"}
	ErrStaleObject           = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "backend object was deleted or recreated"}
	ErrStalePolicy           = &AccessError{Status: StatusAccess, Kind: ErrDenied, Detail: "object policy changed"}
	ErrTenantMismatch        = &AccessError{Status: StatusAccess, Kind: ErrDenied, Detail: "object is outside the export tenant"}
	ErrOperationDenied       = &AccessError{Status: StatusAccess, Kind: ErrDenied, Detail: "operation not permitted by policy"}
	ErrTypeMismatch          = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "object type changed"}
	ErrKeyExpired            = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "retired handle key is outside its grace period"}
	ErrOldKeyNewObject       = &AccessError{Status: StatusAccess, Kind: ErrDenied, Detail: "retired handle key cannot authorize a newer object generation"}
	ErrLeaseStale            = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "lease generation snapshot no longer matches"}
	ErrLeaseClosed           = &AccessError{Status: StatusInvalid, Kind: ErrBadHandle, Detail: "lease is closed"}
	ErrInvalidArgument       = &AccessError{Status: StatusInvalid, Detail: "invalid argument"}
	ErrConflict              = &AccessError{Status: StatusInvalid, Detail: "conflicting object identity or location"}
	ErrExportExists          = &AccessError{Status: StatusInvalid, Detail: "export already exists"}
	ErrObjectNotFound        = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "object not found"}
	ErrSnapshotMismatch      = &AccessError{Status: StatusStale, Kind: ErrStale, Detail: "snapshot does not belong to this export"}
	ErrUnsupportedObjectType = &AccessError{Status: StatusInvalid, Detail: "unsupported object type"}
)

// AccessError is a typed denial with an NFS status class and a stable cause.
type AccessError struct {
	Status Status
	Kind   error
	Detail string
}

func (e *AccessError) Error() string {
	if e.Detail == "" {
		if e.Kind != nil {
			return fmt.Sprintf("netsec: %s: %v", e.Status, e.Kind)
		}
		return fmt.Sprintf("netsec: %s", e.Status)
	}
	return fmt.Sprintf("netsec: %s: %s", e.Status, e.Detail)
}

// Unwrap returns the class sentinel (ErrStale, ErrDenied, or ErrBadHandle)
// when one is present.
func (e *AccessError) Unwrap() error {
	return e.Kind
}

// Is makes specific AccessError values comparable with errors.Is and also
// classifies them against ErrStale/ErrDenied/ErrBadHandle.
func (e *AccessError) Is(target error) bool {
	if target == nil {
		return false
	}
	if target == ErrStale {
		return e.Status == StatusStale
	}
	if target == ErrDenied {
		return e.Status == StatusAccess
	}
	if target == ErrBadHandle {
		return e.Status == StatusBadHandle
	}
	if other, ok := target.(*AccessError); ok {
		if e.Status == other.Status && e.Detail == other.Detail {
			return true
		}
		return e.Kind == other.Kind && e.Status == other.Status && e.Detail == other.Detail
	}
	return e.Kind == target
}

// StatusOf maps an error to the NFS-facing status class.
func StatusOf(err error) Status {
	if err == nil {
		return StatusOK
	}
	var ae *AccessError
	if errors.As(err, &ae) {
		return ae.Status
	}
	switch {
	case errors.Is(err, ErrStale):
		return StatusStale
	case errors.Is(err, ErrDenied):
		return StatusAccess
	case errors.Is(err, ErrBadHandle):
		return StatusBadHandle
	default:
		return StatusIO
	}
}

func deny(status Status, kind error, format string, args ...any) error {
	return &AccessError{
		Status: status,
		Kind:   kind,
		Detail: fmt.Sprintf(format, args...),
	}
}
