package netsec

import "os"

// fileFingerprint identifies the state of a file for cache validation. It
// deliberately includes ctime, which a writer cannot set back to an earlier
// value, so same-size rewrites and timestamp-restoring writes still invalidate
// cached decisions.
type fileFingerprint struct {
	Device    uint64
	Inode     uint64
	Size      int64
	Mode      uint32
	MTimeSec  int64
	MTimeNano int64
	CTimeSec  int64
	CTimeNano int64
}

func fingerprintOf(info os.FileInfo) (fileFingerprint, bool) {
	if info == nil {
		return fileFingerprint{}, false
	}
	return platformFingerprint(info)
}
