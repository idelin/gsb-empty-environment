package netsec

import "fmt"

// SFTP status codes, see draft-ietf-secsh-filexfer, section 7.1.
// Only the codes emitted by this package are declared.
const (
	SSH_FX_OK                uint32 = 0
	SSH_FX_NO_SUCH_FILE      uint32 = 2
	SSH_FX_PERMISSION_DENIED uint32 = 3
	SSH_FX_FAILURE           uint32 = 4
	SSH_FX_BAD_MESSAGE       uint32 = 5
	SSH_FX_OP_UNSUPPORTED    uint32 = 8
)

// StatusError is an error carrying an SSH_FX_* status code so that an SFTP
// server can translate it directly into an SSH_FXP_STATUS response.
type StatusError struct {
	Code    uint32
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("sftp status %d: %s", e.Code, e.Message)
}

func statusError(code uint32, format string, args ...any) *StatusError {
	return &StatusError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// ErrOpUnsupported is returned for every unknown SSH_FXP_EXTENDED request,
// including names that carry a trusted-looking "@vendor" suffix.
var ErrOpUnsupported = &StatusError{
	Code:    SSH_FX_OP_UNSUPPORTED,
	Message: "unsupported extended request",
}
