//go:build windows

package netsec

import "os"

// ownerStat is a placeholder payload on Windows, which does not expose POSIX
// UIDs. osOwner below is the platform-specific entry point there.
type ownerStat struct {
	Uid int
}

// Windows has no numeric UID concept comparable to POSIX, so ownership checks
// cannot be expressed with the same rule. Return an error rather than a guess
// so callers fail closed instead of trusting untrusted directories.
func osOwner(path string) (int, error) {
	return -1, &os.PathError{Op: "owner", Path: path, Err: os.ErrInvalid}
}
