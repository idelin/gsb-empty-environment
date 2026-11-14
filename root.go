package netsec

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// Root is a configured module root. All request paths accepted by Root are
// interpreted relative to this directory and are constrained to its subtree.
//
// The configuration path is resolved once at construction time; request
// callers cannot choose or alter the root. Reads use os.Root, which checks
// containment at the kernel boundary, and also perform an explicit
// EvalSymlinks check. Mutating operations are serialized so that two
// requests cannot race each other through a temporary path.
type Root struct {
	path   string
	handle *os.Root

	mu     sync.RWMutex
	opMu   sync.Mutex
	closed bool
}

// NewRoot opens a module root. moduleRoot must come from trusted
// configuration; it is made absolute and resolved through symbolic links
// before use.
func NewRoot(moduleRoot string) (*Root, error) {
	if moduleRoot == "" {
		return nil, fmt.Errorf("%w: empty module root", ErrInvalidPath)
	}

	absolute, err := filepath.Abs(moduleRoot)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve module root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve module root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("netsec: stat module root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("netsec: module root %q is not a directory", canonical)
	}

	handle, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, fmt.Errorf("netsec: open module root: %w", err)
	}
	return &Root{path: canonical, handle: handle}, nil
}

// OpenRoot is an alias for NewRoot.
func OpenRoot(moduleRoot string) (*Root, error) {
	return NewRoot(moduleRoot)
}

// New is an alias for NewRoot.
func New(moduleRoot string) (*Root, error) {
	return NewRoot(moduleRoot)
}

// Name returns the canonical configured root path.
func (r *Root) Name() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Path is an alias for Name.
func (r *Root) Path() string {
	return r.Name()
}

// Close releases the root handle. It waits for in-flight operations.
func (r *Root) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.handle == nil {
		return nil
	}
	err := r.handle.Close()
	r.handle = nil
	return err
}

func (r *Root) readGuard() (*os.Root, func(), error) {
	if r == nil {
		return nil, nil, ErrRootClosed
	}
	r.mu.RLock()
	if r.closed || r.handle == nil {
		r.mu.RUnlock()
		return nil, nil, ErrRootClosed
	}
	return r.handle, r.mu.RUnlock, nil
}

func (r *Root) writeGuard() (*os.Root, func(), error) {
	if r == nil {
		return nil, nil, ErrRootClosed
	}
	r.opMu.Lock()
	handle, unlock, err := r.readGuard()
	if err != nil {
		r.opMu.Unlock()
		return nil, nil, err
	}
	return handle, func() {
		unlock()
		r.opMu.Unlock()
	}, nil
}

// Validate reports whether a request path is syntactically acceptable.
// It does not consult the filesystem; Resolve additionally checks symbolic
// links.
func (r *Root) Validate(name string) error {
	_, err := normalizeRequest(name)
	return err
}

// Relative returns the cleaned request path relative to the module root.
func (r *Root) Relative(name string) (string, error) {
	if r == nil {
		return "", ErrRootClosed
	}
	return normalizeRequest(name)
}

// Resolve returns the canonical filesystem path for name after resolving all
// symbolic links and checking that the result remains within the module root.
// For a path whose final component does not exist, the nearest existing
// ancestor is resolved and the remaining components are appended.
//
// The returned string is informational. Callers should use the Root methods
// for I/O so that containment is re-checked at operation time.
func (r *Root) Resolve(name string) (string, error) {
	_, unlock, err := r.readGuard()
	if err != nil {
		return "", err
	}
	defer unlock()

	rel, err := normalizeRequest(name)
	if err != nil {
		return "", err
	}
	return r.evalRequestPath(rel)
}

// ResolvePath is an alias for Resolve.
func (r *Root) ResolvePath(name string) (string, error) {
	return r.Resolve(name)
}

// SafeJoin is an alias for Resolve.
func (r *Root) SafeJoin(name string) (string, error) {
	return r.Resolve(name)
}

// Open opens a file for reading.
func (r *Root) Open(name string) (*os.File, error) {
	rel, err := normalizeRequest(name)
	if err != nil {
		return nil, err
	}
	if _, err := r.relativeResolved(rel); err != nil {
		return nil, err
	}

	handle, unlock, err := r.readGuard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	file, err := handle.Open(rel)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// OpenFile opens an existing file. Write flags are rejected: caller-visible
// file descriptors must not bypass atomic replacement.
func (r *Root) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_TRUNC|os.O_EXCL) != 0 {
		return nil, ErrUnsafeWrite
	}
	rel, err := normalizeRequest(name)
	if err != nil {
		return nil, err
	}
	if _, err := r.relativeResolved(rel); err != nil {
		return nil, err
	}

	handle, unlock, err := r.readGuard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return handle.OpenFile(rel, flag, perm)
}

// ReadFile reads a file after lexical and symbolic-link containment checks.
func (r *Root) ReadFile(name string) ([]byte, error) {
	rel, err := normalizeRequest(name)
	if err != nil {
		return nil, err
	}
	if _, err := r.relativeResolved(rel); err != nil {
		return nil, err
	}

	handle, unlock, err := r.readGuard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	file, err := handle.Open(rel)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

// Stat returns file information after containment checks. It follows
// symbolic links only when their targets remain within the module root.
func (r *Root) Stat(name string) (fs.FileInfo, error) {
	rel, err := normalizeRequest(name)
	if err != nil {
		return nil, err
	}
	if _, err := r.relativeResolved(rel); err != nil {
		return nil, err
	}

	handle, unlock, err := r.readGuard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return handle.Stat(rel)
}

// Lstat returns file information without following the final symbolic link,
// after verifying that the link and its parents are within the root. A final
// link whose target escapes the root is rejected.
func (r *Root) Lstat(name string) (fs.FileInfo, error) {
	rel, err := normalizeRequest(name)
	if err != nil {
		return nil, err
	}
	if _, err := r.relativeResolved(rel); err != nil {
		return nil, err
	}

	handle, unlock, err := r.readGuard()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return handle.Lstat(rel)
}

func (r *Root) relativeResolved(rel string) (string, error) {
	resolved, err := r.evalRequestPath(rel)
	if err != nil {
		return "", err
	}
	if !withinRoot(r.path, resolved) {
		return "", fmt.Errorf("%w: %s", ErrPathEscape, resolved)
	}
	return resolved, nil
}

// dirHandle pins a checked parent directory. Operations on the handle are
// relative to the directory's file descriptor, so replacing the original path
// with a symbolic link cannot redirect them outside the module root.
type dirHandle struct {
	root *os.Root
	file *os.File
	rel  string
	abs  string
	info fs.FileInfo
}

func (r *Root) openDir(handle *os.Root, rel string) (*dirHandle, error) {
	abs, err := r.relativeResolved(rel)
	if err != nil {
		return nil, err
	}
	expected, err := handle.Stat(rel)
	if err != nil {
		return nil, err
	}
	if !expected.IsDir() {
		return nil, fmt.Errorf("%w: parent %q is not a directory", ErrInvalidPath, rel)
	}
	absInfo, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(expected, absInfo) {
		return nil, fmt.Errorf("%w: parent %q changed during check", ErrInodeMismatch, rel)
	}

	dirRoot, err := handle.OpenRoot(rel)
	if err != nil {
		return nil, err
	}
	dirFile, err := dirRoot.Open(".")
	if err != nil {
		_ = dirRoot.Close()
		return nil, err
	}
	info, err := dirFile.Stat()
	if err != nil {
		_ = dirFile.Close()
		_ = dirRoot.Close()
		return nil, err
	}
	if !info.IsDir() || !os.SameFile(expected, info) {
		_ = dirFile.Close()
		_ = dirRoot.Close()
		return nil, fmt.Errorf("%w: parent %q changed while opening", ErrInodeMismatch, rel)
	}
	return &dirHandle{root: dirRoot, file: dirFile, rel: rel, abs: abs, info: info}, nil
}

func (dir *dirHandle) close() {
	if dir == nil {
		return
	}
	if dir.file != nil {
		_ = dir.file.Close()
	}
	if dir.root != nil {
		_ = dir.root.Close()
	}
}

func (r *Root) verifyDir(handle *os.Root, dir *dirHandle) error {
	if dir == nil {
		return fmt.Errorf("%w: missing parent handle", ErrInodeMismatch)
	}
	if dir.file != nil {
		info, err := dir.file.Stat()
		if err != nil {
			return fmt.Errorf("%w: parent %q: %v", ErrInodeMismatch, dir.rel, err)
		}
		if !info.IsDir() || !os.SameFile(info, dir.info) {
			return fmt.Errorf("%w: parent %q changed", ErrInodeMismatch, dir.rel)
		}
	}
	absInfo, err := os.Stat(dir.abs)
	if err != nil {
		return fmt.Errorf("%w: parent %q: %v", ErrInodeMismatch, dir.rel, err)
	}
	if !absInfo.IsDir() || !os.SameFile(absInfo, dir.info) {
		return fmt.Errorf("%w: parent %q changed", ErrInodeMismatch, dir.rel)
	}
	if handle != nil {
		info, err := handle.Stat(dir.rel)
		if err != nil {
			return fmt.Errorf("%w: parent %q: %v", ErrInodeMismatch, dir.rel, err)
		}
		if !info.IsDir() || !os.SameFile(info, dir.info) {
			return fmt.Errorf("%w: parent %q changed", ErrInodeMismatch, dir.rel)
		}
	}
	return nil
}

// Mkdir creates one directory beneath the root.
func (r *Root) Mkdir(name string, perm fs.FileMode) error {
	rel, err := normalizeRequest(name)
	if err != nil {
		return err
	}
	if requestOrRoot(rel) {
		return fs.ErrExist
	}

	handle, unlock, err := r.writeGuard()
	if err != nil {
		return err
	}
	defer unlock()

	parentRel := path.Dir(rel)
	parent, err := r.openDir(handle, parentRel)
	if err != nil {
		return err
	}
	defer parent.close()
	if _, err := r.relativeResolved(rel); err != nil {
		return err
	}
	if err := parent.root.Mkdir(path.Base(rel), perm); err != nil {
		return err
	}
	return r.verifyDir(handle, parent)
}

// MkdirAll creates a directory and any missing parents, checking every
// existing component for symbolic-link escape.
func (r *Root) MkdirAll(name string, perm fs.FileMode) error {
	rel, err := normalizeRequest(name)
	if err != nil {
		return err
	}
	if requestOrRoot(rel) {
		return nil
	}

	handle, unlock, err := r.writeGuard()
	if err != nil {
		return err
	}
	defer unlock()

	parts := splitPath(rel)
	for i := range parts {
		current := path.Join(parts[:i+1]...)
		_, statErr := handle.Lstat(current)
		if statErr == nil {
			if err := r.ensureDirComponent(handle, current); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(statErr, fs.ErrNotExist) {
			return statErr
		}
		if _, err := r.relativeResolved(current); err != nil {
			return err
		}
		if err := handle.Mkdir(current, perm); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return err
			}
			if err := r.ensureDirComponent(handle, current); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Root) ensureDirComponent(handle *os.Root, rel string) error {
	if _, err := r.relativeResolved(rel); err != nil {
		return err
	}
	info, err := handle.Lstat(rel)
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		info, err = handle.Stat(rel)
		if err != nil {
			return err
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %q is not a directory", ErrInvalidPath, rel)
	}
	return nil
}

func splitPath(rel string) []string {
	return strings.Split(rel, "/")
}
