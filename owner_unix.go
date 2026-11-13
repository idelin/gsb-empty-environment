//go:build !windows

package netsec

import (
	"os"
	"syscall"
)

// ownerStat is the concrete file info payload carrying the owning UID on
// Unix-like platforms.
type ownerStat = syscall.Stat_t

// osOwner resolves the owning UID of a path using the platform file info.
func osOwner(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return -1, err
	}
	return int(info.Sys().(*ownerStat).Uid), nil
}
