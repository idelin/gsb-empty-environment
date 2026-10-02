package aries

import (
	"io"
	"os"
)

// DiskManager reads and writes fixed-size pages to the data file.
type DiskManager struct {
	f        *os.File
	pageSize int
}

func openDiskManager(path string, pageSize int) (*DiskManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &DiskManager{f: f, pageSize: pageSize}, nil
}

// ReadPage loads page id into buf. Bytes beyond the end of the file
// are zero-filled (the page has never been written).
func (dm *DiskManager) ReadPage(id PageID, buf []byte) error {
	off := int64(id) * int64(dm.pageSize)
	n, err := dm.f.ReadAt(buf, off)
	if err != nil && err != io.EOF {
		return err
	}
	for i := n; i < len(buf); i++ {
		buf[i] = 0
	}
	return nil
}

func (dm *DiskManager) WritePage(id PageID, buf []byte) error {
	off := int64(id) * int64(dm.pageSize)
	_, err := dm.f.WriteAt(buf, off)
	return err
}

func (dm *DiskManager) Sync() error  { return dm.f.Sync() }
func (dm *DiskManager) Close() error { return dm.f.Close() }
