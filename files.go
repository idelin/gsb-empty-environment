package extsort

import (
	"os"
	"sync/atomic"
)

// Open-file accounting: every file the sorter touches goes through the
// counted helpers below so tests can observe the peak number of files
// open simultaneously (relevant when bounding merge fan-in).
var (
	openFiles    atomic.Int64
	peakOpenFile atomic.Int64
)

type trackedFile struct {
	*os.File
}

func registerOpen(f *os.File, err error) (*trackedFile, error) {
	if err != nil {
		return nil, err
	}
	cur := openFiles.Add(1)
	for {
		p := peakOpenFile.Load()
		if cur <= p || peakOpenFile.CompareAndSwap(p, cur) {
			break
		}
	}
	return &trackedFile{File: f}, nil
}

func (t *trackedFile) Close() error {
	openFiles.Add(-1)
	return t.File.Close()
}

func countedOpen(name string) (*trackedFile, error) {
	return registerOpen(os.Open(name))
}

func countedOpenFile(name string, flag int, perm os.FileMode) (*trackedFile, error) {
	return registerOpen(os.OpenFile(name, flag, perm))
}

// PeakOpenFiles reports the high-water mark since the last reset.
func PeakOpenFiles() int64 { return peakOpenFile.Load() }

// ResetOpenFileStats zeroes the counters (test use).
func ResetOpenFileStats() {
	openFiles.Store(0)
	peakOpenFile.Store(0)
}
