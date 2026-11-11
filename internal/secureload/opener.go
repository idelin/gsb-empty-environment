package secureload

import (
	"fmt"
	"os"
	"path/filepath"
)

// Opener is the controlled substitution point used to open module objects.
//
// The production implementation, [SecureOpener], resolves every path through
// an os.Root pinned to a manifest-authorized directory. Because the root holds
// an open directory file descriptor and resolution uses the *at family of
// system calls, a rename of any directory component, a symlink swap or a
// replacement of the target name cannot make the returned object refer to a
// different inode than the one that was resolved.
//
// Tests substitute this interface with an attacker-controlled opener that
// mutates the filesystem between path lookup and the open syscall, which is
// the classic TOCTOU window.
type Opener interface {
	// OpenAuthenticated opens rootName/relPath for reading. relPath is
	// guaranteed to have passed manifest validation. Implementations must
	// refuse any resolution that escapes the authorized root.
	OpenAuthenticated(rootName, relPath string) (*os.File, error)
}

// SecureOpener is the default [Opener]. It opens exactly one os.Root per
// manifest-authorized directory and reuses the pinned root for every module
// beneath it.
type SecureOpener struct {
	roots map[string]*os.Root
}

// NewSecureOpener pins every directory listed in the manifest. Callers must
// invoke Close when done. An error opening any authorized directory (for
// example because it does not exist) aborts construction so that a missing
// trust anchor can never be silently treated as empty.
func NewSecureOpener(m *Manifest) (*SecureOpener, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	opener := &SecureOpener{roots: make(map[string]*os.Root, len(m.Roots))}
	for name, dir := range m.Roots {
		if err := rejectSymlinkedRoot(dir); err != nil {
			return nil, fmt.Errorf("secureload: authorized root %q: %w", name, err)
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			_ = opener.Close()
			return nil, fmt.Errorf("secureload: cannot open authorized root %q (%s): %w", name, dir, err)
		}
		opener.roots[name] = root
	}
	return opener, nil
}

// rejectSymlinkedRoot refuses a trust anchor whose path is itself a symlink
// or contains a symlinked component. The root directory is the boundary of
// the security policy; resolving it through an attacker-controlled link would
// let the whole authorized tree be redirected before any module is opened.
func rejectSymlinkedRoot(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("root directory %q must not be a symbolic link", dir)
	}
	parent := filepath.Dir(dir)
	if parent != dir {
		if err := rejectSymlinkedRoot(parent); err != nil {
			return err
		}
	}
	return nil
}

// OpenAuthenticated implements [Opener]. The returned *os.File is the single
// open object used for both digest verification and the load callback.
func (s *SecureOpener) OpenAuthenticated(rootName, relPath string) (*os.File, error) {
	root, ok := s.roots[rootName]
	if !ok {
		return nil, fmt.Errorf("secureload: root %q is not an authorized directory", rootName)
	}
	// Root.Open walks the path with openat relative to the pinned directory
	// fd, refuses absolute and escaping symlinks, and revalidates components
	// against rename races; the returned fd therefore names the resolved
	// object itself, not a path that could be rebound afterwards.
	file, err := root.Open(relPath)
	if err != nil {
		return nil, fmt.Errorf("secureload: open %s/%s refused: %w", rootName, relPath, err)
	}
	return file, nil
}

// Close releases the pinned directory handles.
func (s *SecureOpener) Close() error {
	var err error
	for name, root := range s.roots {
		if closeErr := root.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("secureload: closing root %q: %w", name, closeErr)
		}
	}
	return err
}
