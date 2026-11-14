//go:build !unix

package netsec

import (
	"fmt"
	"os"
)

// openNoFollow is the portable fallback: it rejects symbolic links with
// Lstat before opening. Unix-like platforms use the atomic O_NOFOLLOW path
// in open_unix.go instead.
func openNoFollow(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: stat %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %q", ErrSymlink, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: open %q: %w", path, err)
	}
	return f, nil
}
