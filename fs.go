package cleaner

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// FileInfo is a comparable snapshot of a filesystem object, captured both
// when a plan is frozen and again at execution time.
type FileInfo struct {
	// ID identifies the object itself. A replaced file gets a new ID even
	// if it lives at the same path.
	ID uint64
	// Version changes whenever the object's content or metadata changes.
	Version uint64
	// Size is the content length in bytes (0 for directories).
	Size int64
	// IsDir reports whether the object is a directory.
	IsDir bool
}

// FS abstracts the filesystem operations the cleaner needs, so the same
// logic runs against the real filesystem (OSFS) or a virtual one (MemFS).
type FS interface {
	// Stat returns the current snapshot of the object at path.
	Stat(path string) (FileInfo, error)
	// ReadDir returns the sorted names of the direct children of path.
	ReadDir(path string) ([]string, error)
	// Remove deletes the object at path. It must fail on non-empty dirs.
	Remove(path string) error
	// Join joins path elements using the filesystem's separator.
	Join(elem ...string) string
}

// OSFS implements FS against the real operating-system filesystem.
type OSFS struct{}

// Stat implements FS.
func (OSFS) Stat(p string) (FileInfo, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return FileInfo{}, err
	}
	info := FileInfo{
		Version: uint64(fi.ModTime().UnixNano()),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		info.ID = uint64(st.Dev)<<32 ^ st.Ino
	}
	return info, nil
}

// ReadDir implements FS.
func (OSFS) ReadDir(p string) ([]string, error) {
	entries, err := os.ReadDir(p)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Remove implements FS. os.Remove refuses to delete non-empty directories.
func (OSFS) Remove(p string) error { return os.Remove(p) }

// Join implements FS.
func (OSFS) Join(elem ...string) string { return filepath.Join(elem...) }
