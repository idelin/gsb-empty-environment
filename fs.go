package netsec

import (
	"errors"
	"syscall"
)

const (
	// DefaultDirMode is the mode used for newly created upload directories.
	DefaultDirMode uint32 = 0o700

	// DirOpenFlags is applied to every directory component during traversal.
	DirOpenFlags = syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC

	// UploadFileOpenFlags creates a new regular upload file without following
	// a final-component symlink.
	UploadFileOpenFlags = syscall.O_WRONLY | syscall.O_CREAT | syscall.O_EXCL | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
)

var (
	// ErrUnsafePath is returned for absolute paths, empty components, "." and
	// "..", and names containing NUL.
	ErrUnsafePath = errors.New("netsec: unsafe path")

	// ErrNotDirectory is returned when a component is not a directory, even if
	// an injected filesystem incorrectly reports success for an openat call.
	ErrNotDirectory = errors.New("netsec: path component is not a directory")
)

// FileStat is the subset of stat information needed for type validation.
type FileStat struct {
	Mode uint32
}

// IsDir reports whether the mode describes a directory.
func (s FileStat) IsDir() bool {
	return s.Mode&syscall.S_IFMT == syscall.S_IFDIR
}

// FileSystem is the injectable, descriptor-relative filesystem boundary.
//
// Implementations must use the supplied dirFD as the base for every
// operation. The production implementation is OSFileSystem.
type FileSystem interface {
	Mkdirat(dirFD int, name string, mode uint32) error
	Openat(dirFD int, name string, flags int, mode uint32) (int, error)
	Fstat(fd int) (FileStat, error)
	Close(fd int) error
}

// OSFileSystem uses the host's descriptor-relative syscalls.
type OSFileSystem struct{}

// Fstat returns the type information for an already-open descriptor.
func (OSFileSystem) Fstat(fd int) (FileStat, error) {
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return FileStat{}, err
	}
	return FileStat{Mode: uint32(st.Mode)}, nil
}

// Close closes a descriptor owned by the traversal.
func (OSFileSystem) Close(fd int) error {
	return syscall.Close(fd)
}
