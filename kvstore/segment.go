package kvstore

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// segment 是一个不可变（或正在追加的活跃）日志段文件。
// 读者通过 pin/unpin 固定句柄：引用计数归零且被标记待回收时，
// 才关闭并删除底层文件，保证并发读绝不会读到已回收文件。
type segment struct {
	id   uint64
	path string
	file *os.File
	size int64 // 已写入（且校验有效）的字节数

	refs           atomic.Int64 // 当前固定该段的读者数
	removeWhenDone atomic.Bool  // 压缩换入新段后置位
	reclaimed      atomic.Bool
}

func segmentPath(dir string, id uint64) string {
	return filepath.Join(dir, fmt.Sprintf("%06d.seg", id))
}

func createSegment(dir string, id uint64) (*segment, error) {
	path := segmentPath(dir, id)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	return &segment{id: id, path: path, file: f}, nil
}

func openSegment(dir string, id uint64, writable bool) (*segment, error) {
	flag := os.O_RDONLY
	if writable {
		flag = os.O_RDWR
	}
	f, err := os.OpenFile(segmentPath(dir, id), flag, 0o644)
	if err != nil {
		return nil, err
	}
	return &segment{id: id, path: segmentPath(dir, id), file: f}, nil
}

// pin 固定段句柄。调用方必须持有 Store 读锁且段仍在段表中，
// 因此段被移出段表（写锁下）之后不可能再有新的 pin。
func (s *segment) pin() {
	s.refs.Add(1)
}

func (s *segment) unpin() {
	if s.refs.Add(-1) == 0 && s.removeWhenDone.Load() {
		s.reclaim()
	}
}

// reclaim 关闭并删除段文件；只会在引用归零后执行一次。
func (s *segment) reclaim() {
	if s.reclaimed.CompareAndSwap(false, true) {
		s.file.Close()
		os.Remove(s.path)
	}
}

// readRecordAt 读取 offset 处长度为 length 的完整记录字节。
func (s *segment) readRecordAt(off, length int64) ([]byte, error) {
	buf := make([]byte, length)
	_, err := s.file.ReadAt(buf, off)
	if err != nil {
		return nil, err
	}
	return buf, nil
}
