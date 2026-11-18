package netsec

import (
	"os"
	"path/filepath"
	"syscall"
)

// FS is the host-side file system surface that the calling SFTP server must
// inject into the Authorizer. Keeping it behind an interface makes the
// authorization logic testable without touching a real disk and lets callers
// layer their own root, audit and handle bookkeeping on top.
type FS interface {
	// EvalSymlinks resolves every symlink on p (a host path) and returns the
	// real, lexically clean host path. A non-existent leaf name is allowed:
	// the longest existing prefix is resolved and the remaining components are
	// appended verbatim (as filepath.EvalSymlinks-with-fallback would).
	EvalSymlinks(p string) (string, error)

	// ResolveHandle maps an SSH file handle string previously issued by the
	// server to the host path it refers to. An unknown handle must return an
	// error that the caller may represent as SSH_FX_BAD_MESSAGE.
	ResolveHandle(handle string) (string, error)

	// Access reports whether the authenticated identity may perform perm on
	// the host path p. A nil result means the operation is permitted.
	Access(p string, perm Permission) error
}

// OSFS is the default FS implementation backed by the operating system.
// Handle resolution is delegated back to the server through ResolveHandleFunc
// because file handles are server-side state that this package cannot know.
type OSFS struct {
	Root              string
	ResolveHandleFunc func(handle string) (string, error)
}

var _ FS = (*OSFS)(nil)

func (o *OSFS) EvalSymlinks(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err == nil {
		return real, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// The leaf (or a deeper tail) does not exist yet: resolve the longest
	// existing prefix and reattach the missing components.
	parent := filepath.Dir(p)
	realParent, parentErr := filepath.EvalSymlinks(parent)
	if parentErr != nil {
		return "", err
	}
	return filepath.Join(realParent, filepath.Base(p)), nil
}

func (o *OSFS) ResolveHandle(handle string) (string, error) {
	if o.ResolveHandleFunc == nil {
		return "", statusError(SSH_FX_BAD_MESSAGE, "no handle resolver configured")
	}
	return o.ResolveHandleFunc(handle)
}

func (o *OSFS) Access(p string, perm Permission) error {
	var mode uint32
	switch perm {
	case PermRead:
		mode = rOK
	case PermWrite:
		mode = wOK
	default:
		return statusError(SSH_FX_FAILURE, "unknown permission %q", perm)
	}
	if err := syscall.Access(p, mode); err != nil {
		return statusError(SSH_FX_PERMISSION_DENIED, "access %s denied: %v", perm, err)
	}
	return nil
}

// POSIX access(2) mode bits; not all platforms expose them through syscall.
const (
	rOK uint32 = 4
	wOK uint32 = 2
)
