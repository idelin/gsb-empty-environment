package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// Config configures an Authorizer.
type Config struct {
	// Root is the physical directory that bounds every authorized path.
	Root string

	// ReadOnly denies every operation that requires write permission.
	ReadOnly bool

	// FS supplies filesystem metadata and symlink resolution. It is required
	// and may be OSFS or an injected in-memory implementation.
	FS FS

	// Policy checks basic read/write permission after path validation. When
	// nil, ModePolicy is used.
	Policy AccessPolicy
}

// Authorizer maps SFTP operations, including SSH_FXP_EXTENDED requests, onto
// one basic permission model and one shared path-validation pipeline.
type Authorizer struct {
	root     string
	readOnly bool
	fsys     FS
	policy   AccessPolicy
}

// New creates an Authorizer. Root may be relative, in which case it is made
// absolute. FS must be non-nil.
func New(config Config) (*Authorizer, error) {
	if config.FS == nil {
		return nil, fmt.Errorf("netsec: Config.FS is required")
	}
	if config.Root == "" {
		return nil, fmt.Errorf("netsec: Config.Root is required")
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve root: %w", err)
	}
	root = filepath.Clean(root)
	if root == "." || root == "" {
		return nil, fmt.Errorf("netsec: invalid root %q", config.Root)
	}
	policy := config.Policy
	if policy == nil {
		policy = ModePolicy{}
	}
	return &Authorizer{
		root:     root,
		readOnly: config.ReadOnly,
		fsys:     config.FS,
		policy:   policy,
	}, nil
}

// NewAuthorizer is an alias for New.
func NewAuthorizer(config Config) (*Authorizer, error) {
	return New(config)
}

// Root returns the cleaned absolute root used for confinement checks.
func (a *Authorizer) Root() string {
	return a.root
}

// ReadOnly reports whether write operations are denied.
func (a *Authorizer) ReadOnly() bool {
	return a.readOnly
}

// AuthorizeRead authorizes reading an existing path.
func (a *Authorizer) AuthorizeRead(name string) error {
	return a.authorizePath("read", name, Read, true)
}

// CheckRead is an alias for AuthorizeRead.
func (a *Authorizer) CheckRead(name string) error {
	return a.AuthorizeRead(name)
}

// AuthorizeWrite authorizes modifying an existing path, or creating a missing
// final component when its parent directory is writable.
func (a *Authorizer) AuthorizeWrite(name string) error {
	return a.authorizePath("write", name, Write, false)
}

// CheckWrite is an alias for AuthorizeWrite.
func (a *Authorizer) CheckWrite(name string) error {
	return a.AuthorizeWrite(name)
}

// AuthorizeRename authorizes posix-rename@openssh.com. Rename requires write
// permission on both the source entry's directory and the target entry's
// directory; it does not require write permission on the source file itself.
func (a *Authorizer) AuthorizeRename(oldPath, newPath string) error {
	if err := a.readOnlyError("posix-rename@openssh.com", oldPath); err != nil {
		return err
	}
	if err := a.authorizeEntryDirectory("posix-rename@openssh.com", oldPath); err != nil {
		return err
	}
	if err := a.authorizeEntryDirectory("posix-rename@openssh.com", newPath); err != nil {
		return err
	}
	return nil
}

// CheckRename is an alias for AuthorizeRename.
func (a *Authorizer) CheckRename(oldPath, newPath string) error {
	return a.AuthorizeRename(oldPath, newPath)
}

// AuthorizeHardlink authorizes hardlink@openssh.com. Hard linking requires
// read permission on the source and write permission on the target entry's
// directory.
func (a *Authorizer) AuthorizeHardlink(source, target string) error {
	if err := a.readOnlyError("hardlink@openssh.com", target); err != nil {
		return err
	}
	if err := a.authorizePath("hardlink source", source, Read, true); err != nil {
		return err
	}
	return a.authorizeEntryDirectory("hardlink@openssh.com", target)
}

// CheckHardlink is an alias for AuthorizeHardlink.
func (a *Authorizer) CheckHardlink(source, target string) error {
	return a.AuthorizeHardlink(source, target)
}

// AuthorizeCopyData authorizes the copy-data extension. It requires read
// permission on the source and write permission on the target.
func (a *Authorizer) AuthorizeCopyData(source, target string) error {
	if err := a.readOnlyError("copy-data", target); err != nil {
		return err
	}
	if err := a.authorizePath("copy-data source", source, Read, true); err != nil {
		return err
	}
	return a.authorizePath("copy-data target", target, Write, false)
}

// CheckCopyData is an alias for AuthorizeCopyData.
func (a *Authorizer) CheckCopyData(source, target string) error {
	return a.AuthorizeCopyData(source, target)
}

// AuthorizeFsetstat authorizes fsetstat@openssh.com. A setstat operation
// modifies file metadata and therefore requires write permission on the target.
func (a *Authorizer) AuthorizeFsetstat(name string) error {
	if err := a.readOnlyError("fsetstat@openssh.com", name); err != nil {
		return err
	}
	return a.authorizePath("fsetstat@openssh.com", name, Write, true)
}

// CheckFsetstat is an alias for AuthorizeFsetstat.
func (a *Authorizer) CheckFsetstat(name string) error {
	return a.AuthorizeFsetstat(name)
}

func (a *Authorizer) authorizePath(operation, name string, permission Permission, mustExist bool) error {
	rel, err := a.normalizePath(name)
	if err != nil {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, err)
	}
	if permission&Write != 0 && a.readOnly {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, ErrPermissionDenied)
	}

	abs, info, err := a.resolveRel(rel, mustExist)
	if err != nil {
		return mapPathError(operation, name, err)
	}

	if info == nil {
		// A missing final component is a creation. Writing it requires write
		// permission on the containing directory.
		parent := filepath.Dir(abs)
		if !withinRoot(a.root, parent) {
			return authError(SSH_FX_PERMISSION_DENIED, operation, name, ErrRootEscape)
		}
		parentInfo, statErr := a.fsys.Stat(parent)
		if statErr != nil {
			return mapPathError(operation, name, statErr)
		}
		if !parentInfo.IsDir() {
			return authError(SSH_FX_NO_SUCH_FILE, operation, name, errNotDirectory)
		}
		if err := a.checkPolicy(operation, name, parent, permission, parentInfo); err != nil {
			return err
		}
		return nil
	}

	return a.checkPolicy(operation, name, abs, permission, info)
}

func (a *Authorizer) authorizeEntryDirectory(operation, name string) error {
	if a.readOnly {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, ErrPermissionDenied)
	}

	rel, err := a.normalizePath(name)
	if err != nil {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, err)
	}
	parentRel, base, ok := splitEntry(rel)
	if !ok {
		return authError(SSH_FX_BAD_MESSAGE, operation, name, fmt.Errorf("cannot use root directory as an entry"))
	}

	parentAbs, parentInfo, err := a.resolveRel(parentRel, true)
	if err != nil {
		return mapPathError(operation, name, err)
	}
	if !parentInfo.IsDir() {
		return authError(SSH_FX_NO_SUCH_FILE, operation, name, errNotDirectory)
	}

	entryAbs := filepath.Join(parentAbs, base)
	if !withinRoot(a.root, entryAbs) {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, ErrRootEscape)
	}

	// Even though rename and hardlink act on the directory entry, resolve any
	// final symlink as well. A symlink whose real target is outside the root
	// must never be reachable through an extension request.
	if _, err := a.evalRel(rel); err != nil {
		return mapPathError(operation, name, err)
	}

	return a.checkPolicy(operation, name, parentAbs, Write, parentInfo)
}

func (a *Authorizer) checkPolicy(operation, name, resolved string, permission Permission, info fs.FileInfo) error {
	if permission&Write != 0 && a.readOnly {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, ErrPermissionDenied)
	}
	if err := a.policy.Check(resolved, permission, info); err != nil {
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, err)
	}
	return nil
}

func (a *Authorizer) readOnlyError(operation, name string) error {
	if !a.readOnly {
		return nil
	}
	return authError(SSH_FX_PERMISSION_DENIED, operation, name, ErrPermissionDenied)
}

func mapPathError(operation, name string, err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrRootEscape):
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, err)
	case errors.Is(err, ErrPermissionDenied):
		return authError(SSH_FX_PERMISSION_DENIED, operation, name, err)
	case isNotExist(err), errors.Is(err, errNotDirectory):
		return authError(SSH_FX_NO_SUCH_FILE, operation, name, err)
	default:
		return authError(SSH_FX_FAILURE, operation, name, err)
	}
}
