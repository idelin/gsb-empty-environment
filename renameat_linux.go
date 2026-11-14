//go:build linux

package netsec

import "syscall"

func renameInDir(oldDir *dirHandle, oldName string, newDir *dirHandle, newName string) error {
	return syscall.Renameat(int(oldDir.file.Fd()), oldName, int(newDir.file.Fd()), newName)
}
