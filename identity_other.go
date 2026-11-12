//go:build !unix

package netsec

import "os"

// identityOf falls back to metadata that changes on replacement. It is
// weaker than a device/inode pair but still detects the common races.
func identityOf(fi os.FileInfo) Identity {
	return Identity{Dev: uint64(fi.Size()), Ino: uint64(fi.ModTime().UnixNano())}
}
