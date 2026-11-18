package netsec

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

// Permission is the basic, extension-independent operation class that every
// request — basic or extended — is reduced to before authorization.
type Permission string

const (
	PermRead  Permission = "read"
	PermWrite Permission = "write"
)

// Config configures an Authorizer.
type Config struct {
	// Root is the host directory that all virtual SFTP paths are confined to.
	Root string
	// FS is the caller-injected file system interface.
	FS FS
	// ReadOnly puts the server into read-only mode: every write-class
	// permission check fails with SSH_FX_PERMISSION_DENIED.
	ReadOnly bool
}

// Authorizer performs path normalization, root containment, symlink-safe
// resolution and read/write authorization for SFTP requests. Basic and
// extended requests share exactly the same code path.
type Authorizer struct {
	root     string
	fs       FS
	readOnly bool
}

// New builds an Authorizer. The root itself is symlink-resolved once so that
// later containment checks compare real paths against a real root.
func New(cfg Config) (*Authorizer, error) {
	if cfg.FS == nil {
		return nil, errors.New("netsec: FS implementation is required")
	}
	absRoot, err := filepath.Abs(filepath.Clean(cfg.Root))
	if err != nil {
		return nil, err
	}
	realRoot, err := cfg.FS.EvalSymlinks(absRoot)
	if err != nil {
		return nil, err
	}
	return &Authorizer{
		root:     filepath.Clean(realRoot),
		fs:       cfg.FS,
		readOnly: cfg.ReadOnly,
	}, nil
}

// Root returns the resolved host root.
func (a *Authorizer) Root() string { return a.root }

// Check authorizes a basic read/write operation on a virtual SFTP path.
// The virtual path uses forward slashes and is relative to the server root.
func (a *Authorizer) Check(virtualPath string, perm Permission) error {
	hostPath, err := a.normalize(virtualPath)
	if err != nil {
		return err
	}
	return a.checkHostPath(hostPath, perm)
}

// CheckRead and CheckWrite are convenience wrappers around Check.
func (a *Authorizer) CheckRead(virtualPath string) error {
	return a.Check(virtualPath, PermRead)
}

func (a *Authorizer) CheckWrite(virtualPath string) error {
	return a.Check(virtualPath, PermWrite)
}

// normalize maps a virtual SFTP path to a host path, rejecting anything that
// lexically escapes the root before the file system is touched.
func (a *Authorizer) normalize(virtualPath string) (string, error) {
	if virtualPath == "" {
		return "", statusError(SSH_FX_BAD_MESSAGE, "empty path")
	}
	clean := path.Clean("/" + strings.ReplaceAll(virtualPath, "\\", "/"))
	rel := strings.TrimPrefix(clean, "/")
	hostPath := filepath.Join(a.root, filepath.FromSlash(rel))
	if !a.insideRoot(hostPath) {
		return "", statusError(SSH_FX_PERMISSION_DENIED,
			"path %q escapes the server root", virtualPath)
	}
	return hostPath, nil
}

// checkHostPath enforces the read-only switch, resolves symlinks, re-checks
// containment on the real path and finally asks the injected FS for access.
func (a *Authorizer) checkHostPath(hostPath string, perm Permission) error {
	if a.readOnly && perm == PermWrite {
		return statusError(SSH_FX_PERMISSION_DENIED,
			"server is read-only; %s operation rejected", perm)
	}
	realPath, err := a.fs.EvalSymlinks(hostPath)
	if err != nil {
		return statusError(SSH_FX_NO_SUCH_FILE, "cannot resolve %q: %v", hostPath, err)
	}
	realPath = filepath.Clean(realPath)
	if !a.insideRoot(realPath) {
		return statusError(SSH_FX_PERMISSION_DENIED,
			"resolved path %q escapes the server root", realPath)
	}
	if err := a.fs.Access(realPath, perm); err != nil {
		var se *StatusError
		if errors.As(err, &se) {
			return err
		}
		return statusError(SSH_FX_PERMISSION_DENIED, "access denied: %v", err)
	}
	return nil
}

// insideRoot reports whether p is the root or located beneath it. Both
// arguments must be lexically clean host paths.
func (a *Authorizer) insideRoot(p string) bool {
	if p == a.root {
		return true
	}
	prefix := a.root + string(filepath.Separator)
	return strings.HasPrefix(p, prefix)
}
