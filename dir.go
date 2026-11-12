package netsec

import "strings"

// FileType classifies a directory entry without following symlinks.
type FileType int

const (
	TypeUnknown FileType = iota
	TypeFile
	TypeDir
	TypeSymlink
	TypeOther
)

// Identity names one generation of a filesystem object. When an entry is
// replaced, the successor never reuses the predecessor's Identity, so
// comparing identities detects replacement.
type Identity struct {
	Dev uint64
	Ino uint64
}

// Info describes an entry as confirmed through a Dir capability. It
// never describes a symlink's target.
type Info struct {
	ID   Identity
	Type FileType
}

// Entry is one name captured in a ReadDir snapshot.
type Entry struct {
	Name string
	// Type is the readdir hint; TypeUnknown means Lstat must decide.
	Type FileType
}

// Dir is a capability bounded to a single already-open directory.
//
// Every operation takes one entry name (never a path) resolved relative
// to the opened directory, and no operation follows symlinks. A Dir
// therefore cannot be used to reach objects outside the directory it was
// opened on, even if entries are replaced while it is in use.
type Dir interface {
	// ReadDir returns a snapshot of the current entries, sorted by
	// name. Entries created afterwards are invisible to the caller.
	ReadDir() ([]Entry, error)
	// Lstat reports the current generation of name.
	Lstat(name string) (Info, error)
	// OpenDir opens the subdirectory name as a new capability. It
	// fails if name is a symlink or not a directory.
	OpenDir(name string) (Dir, error)
	// Stat reports the identity of the opened directory itself.
	Stat() (Info, error)
	// Unlink removes the non-directory entry name.
	Unlink(name string) error
	// Rmdir removes the empty directory entry name.
	Rmdir(name string) error
	// Close releases the capability.
	Close() error
}

// validName reports whether name is a single path component.
func validName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, "/\x00")
}
