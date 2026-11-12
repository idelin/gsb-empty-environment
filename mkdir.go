package netsec

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// MkdirAllAt creates relPath beneath the tenant root and returns an open
// descriptor for the final directory.
//
// rootFD must be an open directory descriptor. relPath must be a non-empty,
// relative path made of ordinary components; ".", "..", empty components,
// absolute paths, and embedded NUL bytes are rejected. Existing regular files,
// symlinks, and other non-directory components are rejected.
func MkdirAllAt(rootFD int, relPath string) (*os.File, error) {
	return MkdirAllAtFS(OSFileSystem{}, rootFD, relPath)
}

// MkdirAllAtFS is MkdirAllAt with an injectable filesystem implementation.
func MkdirAllAtFS(fs FileSystem, rootFD int, relPath string) (*os.File, error) {
	if fs == nil {
		return nil, errors.New("netsec: nil filesystem")
	}
	components, err := pathComponents(relPath)
	if err != nil {
		return nil, err
	}
	if err := requireDirectory(fs, rootFD); err != nil {
		return nil, fmt.Errorf("netsec: tenant root: %w", err)
	}

	parentFD := rootFD
	ownedFD := -1
	for _, component := range components {
		err := fs.Mkdirat(parentFD, component, DefaultDirMode)
		if err != nil && !errors.Is(err, syscall.EEXIST) {
			closeIfOwned(fs, ownedFD)
			return nil, fmt.Errorf("netsec: mkdirat %q in %q: %w", component, relPath, err)
		}

		fd, err := fs.Openat(parentFD, component, DirOpenFlags, 0)
		if err != nil {
			closeIfOwned(fs, ownedFD)
			return nil, fmt.Errorf("netsec: openat %q in %q: %w", component, relPath, err)
		}
		if fd < 0 {
			closeIfOwned(fs, ownedFD)
			return nil, fmt.Errorf("netsec: openat %q in %q: invalid descriptor %d", component, relPath, fd)
		}
		if err := requireDirectory(fs, fd); err != nil {
			_ = fs.Close(fd)
			closeIfOwned(fs, ownedFD)
			return nil, fmt.Errorf("netsec: validate %q in %q: %w", component, relPath, err)
		}

		if ownedFD >= 0 {
			if err := fs.Close(ownedFD); err != nil {
				_ = fs.Close(fd)
				return nil, fmt.Errorf("netsec: close parent directory: %w", err)
			}
		}
		parentFD = fd
		ownedFD = fd
	}

	file := os.NewFile(uintptr(parentFD), relPath)
	if file == nil {
		closeIfOwned(fs, ownedFD)
		return nil, errors.New("netsec: cannot wrap final directory descriptor")
	}
	return file, nil
}

// CreateFileAt creates a new upload file in an already-anchored directory.
//
// The name must be exactly one ordinary path component. O_EXCL and O_NOFOLLOW
// prevent overwriting an existing file and prevent following a symlink at the
// final component.
func CreateFileAt(dirFD int, name string, perm os.FileMode) (*os.File, error) {
	return CreateFileAtFS(OSFileSystem{}, dirFD, name, perm)
}

// CreateFileAtFS is CreateFileAt with an injectable filesystem implementation.
func CreateFileAtFS(fs FileSystem, dirFD int, name string, perm os.FileMode) (*os.File, error) {
	if fs == nil {
		return nil, errors.New("netsec: nil filesystem")
	}
	if err := validateComponent(name); err != nil {
		return nil, err
	}
	if err := requireDirectory(fs, dirFD); err != nil {
		return nil, fmt.Errorf("netsec: upload directory: %w", err)
	}

	fd, err := fs.Openat(dirFD, name, UploadFileOpenFlags, uint32(perm.Perm()))
	if err != nil {
		return nil, fmt.Errorf("netsec: create upload file %q: %w", name, err)
	}
	if fd < 0 {
		return nil, fmt.Errorf("netsec: create upload file %q: invalid descriptor %d", name, fd)
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = fs.Close(fd)
		return nil, errors.New("netsec: cannot wrap upload descriptor")
	}
	return file, nil
}

func requireDirectory(fs FileSystem, fd int) error {
	st, err := fs.Fstat(fd)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return ErrNotDirectory
	}
	return nil
}

func closeIfOwned(fs FileSystem, fd int) {
	if fd >= 0 {
		_ = fs.Close(fd)
	}
}

func pathComponents(relPath string) ([]string, error) {
	if relPath == "" || strings.HasPrefix(relPath, "/") || strings.IndexByte(relPath, 0) >= 0 {
		return nil, fmt.Errorf("%w: %q", ErrUnsafePath, relPath)
	}

	raw := strings.Split(relPath, "/")
	components := make([]string, 0, len(raw))
	for _, component := range raw {
		if err := validateComponent(component); err != nil {
			return nil, fmt.Errorf("%w: %q", err, relPath)
		}
		components = append(components, component)
	}
	return components, nil
}

func validateComponent(component string) error {
	if component == "" || component == "." || component == ".." || strings.ContainsRune(component, '/') || strings.IndexByte(component, 0) >= 0 {
		return ErrUnsafePath
	}
	return nil
}
