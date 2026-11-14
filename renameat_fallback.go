//go:build !darwin && !linux

package netsec

import (
	"os"
	"path/filepath"
)

// Platforms without a usable renameat binding fall back to absolute paths.
// The directory handles have already been pinned to inodes inside the root,
// and callers verify the resulting inode.
func renameInDir(oldDir *dirHandle, oldName string, newDir *dirHandle, newName string) error {
	return os.Rename(filepath.Join(oldDir.abs, oldName), filepath.Join(newDir.abs, newName))
}
