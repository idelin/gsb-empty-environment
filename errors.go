package netsec

import "fmt"

// ErrAccessDenied is the common sentinel for every request rejected by the
// translator.  Errors returned by this package never contain a real
// filesystem prefix; callers should not need to know one to report failures
// back to a safe interpreter.
var ErrAccessDenied = fmt.Errorf("netsec: access denied")

var (
	// ErrInvalidToken indicates that a value is not a well-formed $p(:N:)
	// virtual path.
	ErrInvalidToken = fmt.Errorf("%w: invalid token", ErrAccessDenied)
	// ErrTokenOutOfRange indicates that the token's numeric index is not a
	// configured access path.
	ErrTokenOutOfRange = fmt.Errorf("%w: token index out of range", ErrAccessDenied)
	// ErrPathTraversal indicates that a ".." component was supplied.
	ErrPathTraversal = fmt.Errorf("%w: path traversal rejected", ErrAccessDenied)
	// ErrOutsideAccessPath indicates that the canonical target is outside the
	// selected access path (including a symlink that resolves outside it).
	ErrOutsideAccessPath = fmt.Errorf("%w: outside access path", ErrAccessDenied)
	// ErrNotTcl indicates that a file requested by source is not a .tcl file.
	ErrNotTcl = fmt.Errorf("%w: source file must end in .tcl", ErrAccessDenied)
	// ErrNotRegular indicates that source was asked to open a non-regular file.
	ErrNotRegular = fmt.Errorf("%w: source is not a regular file", ErrAccessDenied)
	// ErrGlobPattern indicates an invalid filepath.Match pattern.
	ErrGlobPattern = fmt.Errorf("%w: invalid glob pattern", ErrAccessDenied)
	// ErrInvalidConfig indicates an unusable access-path configuration.
	ErrInvalidConfig = fmt.Errorf("%w: invalid access path configuration", ErrAccessDenied)
	// ErrReadFailed is returned when an opened source file cannot be read.
	ErrReadFailed = fmt.Errorf("%w: source read failed", ErrAccessDenied)
)
