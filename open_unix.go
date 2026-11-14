//go:build unix

package netsec

import (
	"fmt"
	"os"
	"syscall"
)

// openNoFollow opens path read-only, refusing to open it if the final path
// component is a symbolic link. O_CLOEXEC avoids descriptor leaks across
// exec boundaries.
func openNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if err == syscall.ELOOP {
			return nil, fmt.Errorf("%w: %q", ErrSymlink, path)
		}
		return nil, fmt.Errorf("netsec: open %q: %w", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil
}
