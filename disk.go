package aries

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
)

// PageSize 是磁盘页大小;页头 8 字节存 pageLSN,其余为数据区。
const PageSize = 4096

// PageDataSize 是页内可用数据区大小。
const PageDataSize = PageSize - 8

// ErrBadPageData 表示页数据长度不合法。
var ErrBadPageData = errors.New("aries: page data must be PageDataSize bytes")

// DiskManager 以固定大小页读写数据文件,模拟持久存储。
type DiskManager struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

// OpenDiskManager 打开(或创建)数据文件。
func OpenDiskManager(path string) (*DiskManager, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	return &DiskManager{file: f}, nil
}

// ReadPage 读取页数据与其 pageLSN;页不存在时返回零页。
func (dm *DiskManager) ReadPage(pageID uint64) ([]byte, LSN, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.closed {
		return nil, 0, ErrClosed
	}
	buf := make([]byte, PageSize)
	n, err := dm.file.ReadAt(buf, int64(pageID)*PageSize)
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	if n < 8 {
		return make([]byte, PageDataSize), NilLSN, nil
	}
	pageLSN := LSN(binary.LittleEndian.Uint64(buf[:8]))
	data := make([]byte, PageDataSize)
	copy(data, buf[8:])
	return data, pageLSN, nil
}

// PageLSN 只读取页头中的 pageLSN。
func (dm *DiskManager) PageLSN(pageID uint64) (LSN, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.closed {
		return 0, ErrClosed
	}
	hdr := make([]byte, 8)
	n, err := dm.file.ReadAt(hdr, int64(pageID)*PageSize)
	if err != nil && err != io.EOF {
		return 0, err
	}
	if n < 8 {
		return NilLSN, nil
	}
	return LSN(binary.LittleEndian.Uint64(hdr)), nil
}

// WritePage 将页数据与 pageLSN 写入磁盘。
func (dm *DiskManager) WritePage(pageID uint64, data []byte, pageLSN LSN) error {
	if len(data) != PageDataSize {
		return ErrBadPageData
	}
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.closed {
		return ErrClosed
	}
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint64(buf[:8], uint64(pageLSN))
	copy(buf[8:], data)
	_, err := dm.file.WriteAt(buf, int64(pageID)*PageSize)
	return err
}

// Sync 将数据文件落盘。
func (dm *DiskManager) Sync() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.closed {
		return ErrClosed
	}
	return dm.file.Sync()
}

// Close 关闭数据文件。
func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	if dm.closed {
		return nil
	}
	dm.closed = true
	return dm.file.Close()
}
