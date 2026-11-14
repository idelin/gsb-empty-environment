package netsec

import "errors"

var (
	// ErrInvalidPath indicates a request path that is empty, absolute,
	// contains a NUL, or otherwise cannot be interpreted as a relative path.
	ErrInvalidPath = errors.New("netsec: invalid request path")

	// ErrPathEscape indicates that a request path, after cleaning or after
	// resolving symbolic links, points outside the configured module root.
	ErrPathEscape = errors.New("netsec: path escapes module root")

	// ErrSymlink indicates that an operation which must not follow a final
	// symbolic link was given one.
	ErrSymlink = errors.New("netsec: symbolic link is not allowed for this operation")

	// ErrInodeMismatch indicates that the object observed after an operation
	// is not the same filesystem object observed before it. This catches
	// concurrent replacement of a checked path.
	ErrInodeMismatch = errors.New("netsec: inode verification failed")

	// ErrUnknownModule indicates a module name that is absent from the
	// configuration-backed module map.
	ErrUnknownModule = errors.New("netsec: unknown module")

	// ErrRootClosed indicates use of a Root after Close.
	ErrRootClosed = errors.New("netsec: module root is closed")

	// ErrUnsafeWrite indicates an attempt to open a file with write flags
	// directly. Writes must go through WriteFile, which performs an atomic
	// temporary-file replacement.
	ErrUnsafeWrite = errors.New("netsec: direct write-open is not permitted; use WriteFile")
)

// Common aliases kept for callers that use shorter names.
var (
	ErrEscape         = ErrPathEscape
	ErrModuleNotFound = ErrUnknownModule
)
