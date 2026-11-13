package sudoedit

import (
	"os"
	"syscall"
)

// PathInspector inspects a single filesystem path without following
// symbolic links. It is an interface so callers embedding the authorization
// decision can inject the credentials of an audited user or a sandboxed
// filesystem view.
type PathInspector interface {
	// IsSymlink reports whether path itself is a symbolic link.
	IsSymlink(path string) bool

	// IsWritable reports whether the caller the decision is made for can
	// write to path.
	IsWritable(path string) bool
}

// OSInspector implements PathInspector against the real filesystem using the
// credentials of the running process.
type OSInspector struct{}

var _ PathInspector = OSInspector{}

func (OSInspector) IsSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

func (OSInspector) IsWritable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	mode := info.Mode()

	if mode&0o002 != 0 {
		return true
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}

	euid := uint32(os.Geteuid())
	egid := uint32(os.Getegid())

	if stat.Uid == euid && mode&0o200 != 0 {
		return true
	}

	if stat.Gid == egid && mode&0o020 != 0 {
		return true
	}

	groups, err := os.Getgroups()
	if err != nil {
		return false
	}
	for _, gid := range groups {
		if stat.Gid == uint32(gid) && mode&0o020 != 0 {
			return true
		}
	}

	return false
}
