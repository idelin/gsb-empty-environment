//go:build linux

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
		Device:    uint64(stat.Dev),
		Inode:     uint64(stat.Ino),
		Size:      stat.Size,
		Mode:      uint32(stat.Mode),
		MTimeSec:  stat.Mtim.Sec,
		MTimeNano: stat.Mtim.Nsec,
		CTimeSec:  stat.Ctim.Sec,
		CTimeNano: stat.Ctim.Nsec,
	}, true
}
