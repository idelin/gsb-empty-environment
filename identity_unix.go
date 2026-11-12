//go:build unix

package netsec

import (
	"os"
	"syscall"
)

// identityOf extracts the device/inode pair naming one object
// generation.
func identityOf(fi os.FileInfo) Identity {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return Identity{Dev: uint64(st.Dev), Ino: st.Ino}
	}
	return Identity{}
}
