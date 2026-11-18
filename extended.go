package netsec

import (
	"errors"
	"path"
	"path/filepath"
)

// SSH_FXP_EXTENDED request names recognized by this package.
const (
	ExtPosixRename = "posix-rename@openssh.com"
	ExtHardlink    = "hardlink@openssh.com"
	ExtCopyData    = "copy-data"
	ExtFSetStat    = "fsetstat@openssh.com"
)

// ExtendedRequest is a decoded SSH_FXP_EXTENDED request. Path fields carry
// virtual SFTP paths; Handle fields carry opaque server-issued handle strings.
type ExtendedRequest struct {
	Name    string
	OldPath string
	NewPath string
	// LinkPath is the new link location for hardlink@openssh.com; TargetPath
	// is the existing source file.
	LinkPath   string
	TargetPath string
	// ReadHandle and WriteHandle back a copy-data request.
	ReadHandle  string
	WriteHandle string
	// Handle backs an fsetstat@openssh.com request.
	Handle string
}

// CheckExtended authorizes an SSH_FXP_EXTENDED request. Every recognized
// extension is first translated into the equivalent basic read/write
// permissions and then runs through the same normalization, root-containment
// and read-only checks as a plain request. Any unknown name — including a
// forged name carrying an "@openssh.com" or other vendor suffix — fails with
// SSH_FX_OP_UNSUPPORTED.
func (a *Authorizer) CheckExtended(req ExtendedRequest) error {
	switch req.Name {
	case ExtPosixRename:
		if req.OldPath == "" || req.NewPath == "" {
			return badMessage("posix-rename requires oldpath and newpath")
		}
		// A rename mutates two directories: the source's parent and the
		// target's parent. The data itself need not be readable.
		if err := a.CheckWrite(parentVirtual(req.OldPath)); err != nil {
			return err
		}
		return a.CheckWrite(parentVirtual(req.NewPath))

	case ExtHardlink:
		if req.TargetPath == "" || req.LinkPath == "" {
			return badMessage("hardlink requires target and linkpath")
		}
		// Creating a hard link reads the source inode and adds a directory
		// entry in the link's parent directory.
		if err := a.CheckRead(req.TargetPath); err != nil {
			return err
		}
		return a.CheckWrite(parentVirtual(req.LinkPath))

	case ExtCopyData:
		if req.ReadHandle == "" || req.WriteHandle == "" {
			return badMessage("copy-data requires read-handle and write-handle")
		}
		readPath, err := a.resolveHandle(req.ReadHandle)
		if err != nil {
			return err
		}
		writePath, err := a.resolveHandle(req.WriteHandle)
		if err != nil {
			return err
		}
		if err := a.checkHostPath(readPath, PermRead); err != nil {
			return err
		}
		return a.checkHostPath(writePath, PermWrite)

	case ExtFSetStat:
		if req.Handle == "" {
			return badMessage("fsetstat requires a handle")
		}
		// fsetstat mutates file metadata, which is a write-class operation.
		hostPath, err := a.resolveHandle(req.Handle)
		if err != nil {
			return err
		}
		return a.checkHostPath(hostPath, PermWrite)

	default:
		return ErrOpUnsupported
	}
}

// resolveHandle maps an opaque handle to its host path and ensures the
// resulting path itself stays inside the root.
func (a *Authorizer) resolveHandle(handle string) (string, error) {
	hostPath, err := a.fs.ResolveHandle(handle)
	if err != nil {
		var se *StatusError
		if errors.As(err, &se) {
			return "", err
		}
		return "", statusError(SSH_FX_BAD_MESSAGE, "invalid handle: %v", err)
	}
	hostPath = filepath.Clean(hostPath)
	if !a.insideRoot(hostPath) {
		return "", statusError(SSH_FX_PERMISSION_DENIED,
			"handle path %q escapes the server root", hostPath)
	}
	return hostPath, nil
}

func parentVirtual(virtualPath string) string {
	return path.Dir("/" + cleanVirtual(virtualPath))
}

func cleanVirtual(virtualPath string) string {
	return path.Clean("/" + virtualPath)
}

func badMessage(format string, args ...any) *StatusError {
	return statusError(SSH_FX_BAD_MESSAGE, format, args...)
}
