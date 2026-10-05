package main

import (
	"os"
	"sync/atomic"
)

// 打开文件描述符统计：跟踪当前打开数与历史峰值。
var (
	fdCurrent int64
	fdPeak    int64
)

func openTracked(path string, flag int, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag, perm)
	if err != nil {
		return nil, err
	}
	cur := atomic.AddInt64(&fdCurrent, 1)
	for {
		p := atomic.LoadInt64(&fdPeak)
		if cur <= p || atomic.CompareAndSwapInt64(&fdPeak, p, cur) {
			break
		}
	}
	return f, nil
}

func closeTracked(f *os.File) error {
	err := f.Close()
	atomic.AddInt64(&fdCurrent, -1)
	return err
}

func peakOpenFiles() int64 {
	return atomic.LoadInt64(&fdPeak)
}
