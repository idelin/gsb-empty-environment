package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

var (
	// ErrInvalidToken reports a malformed virtual access token.
	ErrInvalidToken = errors.New("netsec: invalid access token")
	// ErrTokenOutOfRange reports a token whose numeric id is not configured.
	ErrTokenOutOfRange = errors.New("netsec: access token out of range")
	// ErrPathEscape reports a virtual path that climbs above its access root.
	ErrPathEscape = errors.New("netsec: path escapes access path")
	// ErrSubdirDenied reports a path outside the configured accessible subdirectories.
	ErrSubdirDenied = errors.New("netsec: path is outside the accessible subdirectories")
	// ErrSymlinkEscape reports a symbolic link whose target is outside the root.
	ErrSymlinkEscape = errors.New("netsec: symbolic link escapes access path")
	// ErrNotRegularFile reports a non-regular file where a regular file is required.
	ErrNotRegularFile = errors.New("netsec: not a regular file")
	// ErrNotTclFile reports a source request for a non-.tcl file.
	ErrNotTclFile = errors.New("netsec: source file must end in .tcl")
	// ErrGlobLeak reports a glob result that still contains a real access root.
	ErrGlobLeak = errors.New("netsec: glob result would expose a real path")
	// ErrBadPattern reports a malformed glob pattern.
	ErrBadPattern = errors.New("netsec: invalid glob pattern")
	// ErrNoAccessPath reports an empty access path configuration.
	ErrNoAccessPath = errors.New("netsec: no access paths configured")

	// ErrNotFound and ErrAccess retain the conventional errors.Is behavior
	// without carrying an OS error that might contain a real path.
	ErrNotFound = fs.ErrNotExist
	ErrAccess   = fs.ErrPermission
)

// AccessError is a path-free error returned by Translator.
//
// Token, when present, is a canonical virtual token such as "$p(:0:)".
// Real filesystem roots and raw OS path errors are deliberately omitted so
// that a safe interpreter cannot recover host paths from diagnostics.
type AccessError struct {
	Op    string
	Token string
	Err   error
}

func (e *AccessError) Error() string {
	if e.Token == "" {
		return fmt.Sprintf("netsec: %s: %v", e.Op, e.Err)
	}
	return fmt.Sprintf("netsec: %s %s: %v", e.Op, e.Token, e.Err)
}

func (e *AccessError) Unwrap() error {
	return e.Err
}

func accessError(op, token string, err error) error {
	if err == nil {
		return nil
	}
	return &AccessError{Op: op, Token: token, Err: err}
}

// classifyIOError removes path-bearing OS errors from the returned error
// chain while preserving the useful broad categories.
func classifyIOError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotFound
	case errors.Is(err, fs.ErrPermission):
		return ErrAccess
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "escape") ||
		strings.Contains(msg, "symlink") ||
		strings.Contains(msg, "too many levels") {
		return ErrSymlinkEscape
	}
	return ErrAccess
}
