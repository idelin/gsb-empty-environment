//go:build darwin

package netsec

import (
	"os"
	"syscall"
)

func platformFingerprint(info os.FileInfo) (fileFingerprint, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileFingerprint{}, false
	}
	return fileFingerprint{
		Device:    uint64(uint32(stat.Dev)),
		Inode:     uint64(stat.Ino),
		Size:      stat.Size,
		Mode:      uint32(stat.Mode),
		MTimeSec:  stat.Mtimespec.Sec,
		MTimeNano: stat.Mtimespec.Nsec,
		CTimeSec:  stat.Ctimespec.Sec,
		CTimeNano: stat.Ctimespec.Nsec,
	}, true
}
