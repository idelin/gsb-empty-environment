package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// FS is the filesystem surface used by Authorizer. All names are absolute
// paths in the filesystem's namespace, so an in-memory implementation is free
// to choose its own absolute naming scheme.
//
// Lstat must not follow the final symlink, Readlink must return the raw target
// of a symlink, and Stat must follow symlinks. An os.Root-backed adapter can
// implement these methods using relative names internally while retaining
// kernel-level race protection for the actual operations.
type FS interface {
	Lstat(name string) (fs.FileInfo, error)
	Stat(name string) (fs.FileInfo, error)
	Readlink(name string) (string, error)
}

// OSFS implements FS using the host operating system and absolute paths.
// Authorization is checked before the caller performs the operation, so
// callers that need race-free confinement should inject an os.Root adapter or
// re-check the operation atomically.
type OSFS struct{}

func (OSFS) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }
func (OSFS) Stat(name string) (fs.FileInfo, error)  { return os.Stat(name) }
func (OSFS) Readlink(name string) (string, error)   { return os.Readlink(name) }

var _ FS = OSFS{}

// Permission is a basic SFTP capability. Extension requests are translated
// into combinations of these capabilities before any path is authorized.
type Permission uint8

const (
	Read Permission = 1 << iota
	Write
)

func (p Permission) String() string {
	switch p {
	case Read:
		return "read"
	case Write:
		return "write"
	case Read | Write:
		return "read+write"
	default:
		return fmt.Sprintf("permission(%d)", uint8(p))
	}
}

// AccessPolicy decides whether an authenticated session has the requested
// basic permission on an already normalized and symlink-validated path.
//
// Returning nil allows the operation. Any non-nil error is treated as a
// permission denial, so policies should not use errors for transient
// filesystem failures.
type AccessPolicy interface {
	Check(path string, permission Permission, info fs.FileInfo) error
}

// AccessPolicyFunc adapts a function to AccessPolicy.
type AccessPolicyFunc func(path string, permission Permission, info fs.FileInfo) error

func (f AccessPolicyFunc) Check(path string, permission Permission, info fs.FileInfo) error {
	return f(path, permission, info)
}

// ModePolicy derives read and write permission from the Unix permission bits
// in fs.FileInfo.Mode. Read is allowed when at least one read bit is set;
// write is allowed when at least one write bit is set. This deliberately
// ignores execute and ownership bits: the policy is an authorization gate, not
// an emulation of every Unix access-control rule.
type ModePolicy struct{}

func (ModePolicy) Check(path string, permission Permission, info fs.FileInfo) error {
	if info == nil {
		return fmt.Errorf("%w: no file information for %s", ErrPermissionDenied, path)
	}
	mode := info.Mode().Perm()
	if permission&Read != 0 && mode&0o444 == 0 {
		return fmt.Errorf("%w: %s is not readable (%s)", ErrPermissionDenied, path, mode)
	}
	if permission&Write != 0 && mode&0o222 == 0 {
		return fmt.Errorf("%w: %s is not writable (%s)", ErrPermissionDenied, path, mode)
	}
	return nil
}

var _ AccessPolicy = ModePolicy{}

func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err)
}
