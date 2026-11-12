package netsec

import (
	"errors"
	"fmt"
)

// Status is a value from the SSH_FXP_STATUS response family defined by the
// SFTP protocol. It is an alias of uint32 so it can be written directly into
// the protocol field without a conversion.
type Status = uint32

const (
	SSH_FX_OK                = 0
	SSH_FX_EOF               = 1
	SSH_FX_NO_SUCH_FILE      = 2
	SSH_FX_PERMISSION_DENIED = 3
	SSH_FX_FAILURE           = 4
	SSH_FX_BAD_MESSAGE       = 5
	SSH_FX_NO_CONNECTION     = 6
	SSH_FX_CONNECTION_LOST   = 7
	SSH_FX_OP_UNSUPPORTED    = 8
)

var (
	// ErrPermissionDenied is returned when the session policy denies access.
	ErrPermissionDenied = errors.New("permission denied")

	// ErrRootEscape is returned when a path, or a symlink target used while
	// resolving it, leaves the configured root.
	ErrRootEscape = errors.New("path escapes configured root")

	// ErrUnsupported marks an unknown SSH_FXP_EXTENDED extension name.
	ErrUnsupported = errors.New("operation unsupported")

	// ErrBadMessage marks a malformed extension request.
	ErrBadMessage = errors.New("bad message")
)

// AuthError is a request-authorization failure that carries the SSH_FX status
// which should be sent to the client.
type AuthError struct {
	Code      Status
	Operation string
	Path      string
	Err       error
}

func (e *AuthError) Error() string {
	where := ""
	if e.Path != "" {
		where = " " + e.Path
	}
	if e.Operation != "" {
		where = e.Operation + where
	}
	if e.Err == nil {
		if where == "" {
			return statusText(e.Code)
		}
		return fmt.Sprintf("%s: %s", where, statusText(e.Code))
	}
	if where == "" {
		return fmt.Sprintf("%s: %v", statusText(e.Code), e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", where, statusText(e.Code), e.Err)
}

func (e *AuthError) Unwrap() error {
	return e.Err
}

// ErrorStatus extracts the SSH_FX status from err. Non-authorization errors
// map to SSH_FX_FAILURE.
func ErrorStatus(err error) Status {
	if err == nil {
		return SSH_FX_OK
	}
	var authErr *AuthError
	if errors.As(err, &authErr) {
		return authErr.Code
	}
	return SSH_FX_FAILURE
}

func statusText(code Status) string {
	switch code {
	case SSH_FX_OK:
		return "ok"
	case SSH_FX_EOF:
		return "eof"
	case SSH_FX_NO_SUCH_FILE:
		return "no such file"
	case SSH_FX_PERMISSION_DENIED:
		return "permission denied"
	case SSH_FX_FAILURE:
		return "failure"
	case SSH_FX_BAD_MESSAGE:
		return "bad message"
	case SSH_FX_NO_CONNECTION:
		return "no connection"
	case SSH_FX_CONNECTION_LOST:
		return "connection lost"
	case SSH_FX_OP_UNSUPPORTED:
		return "operation unsupported"
	default:
		return fmt.Sprintf("status %d", uint32(code))
	}
}

func authError(code Status, operation, path string, err error) *AuthError {
	return &AuthError{Code: code, Operation: operation, Path: path, Err: err}
}
