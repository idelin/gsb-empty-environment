//go:build !linux && !darwin

package netsec

import "os"

func platformFingerprint(info os.FileInfo) (fileFingerprint, bool) {
	return fileFingerprint{
		Size:      info.Size(),
		Mode:      uint32(info.Mode()),
		MTimeSec:  info.ModTime().Unix(),
		MTimeNano: int64(info.ModTime().Nanosecond()),
	}, true
}
