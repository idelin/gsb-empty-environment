package aries

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
)

// PageSize is the fixed size of a database page in bytes.
const PageSize = 256

// PageHeaderSize is the number of bytes at the start of each page reserved
// for the page header. The header stores the pageLSN (LSN of the last log
// record applied to this page), which makes Redo idempotent.
const PageHeaderSize = 8

// Page is one fixed-size page. Data includes the header: bytes
// [0, PageHeaderSize) hold the pageLSN, user data lives at
// [PageHeaderSize, PageSize).
type Page struct {
	ID      uint64
	Data    []byte
	PageLSN uint64
	Dirty   bool
}

// DiskManager reads and writes pages in the data file.
type DiskManager struct {
	f *os.File
}

func OpenDiskManager(path string) (*DiskManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	return &DiskManager{f: f}, nil
}

// ReadPage loads a page from disk. Pages beyond the end of the file are
// zero-filled.
func (dm *DiskManager) ReadPage(id uint64) (*Page, error) {
	data := make([]byte, PageSize)
	n, err := dm.f.ReadAt(data, int64(id)*PageSize)
	if err != nil && err != io.EOF {
		return nil, err
	}
	p := &Page{ID: id, Data: data}
	if n >= PageHeaderSize {
		p.PageLSN = binary.BigEndian.Uint64(data[:PageHeaderSize])
	}
	return p, nil
}

// WritePage persists a page and fsyncs the data file.
func (dm *DiskManager) WritePage(p *Page) error {
	binary.BigEndian.PutUint64(p.Data[:PageHeaderSize], p.PageLSN)
	if _, err := dm.f.WriteAt(p.Data, int64(p.ID)*PageSize); err != nil {
		return err
	}
	return dm.f.Sync()
}

func (dm *DiskManager) Close() error {
	return dm.f.Close()
}

// BufferPool caches pages in memory. Writes only become durable through
// FlushAll/FlushPage; Crash() simply discards the pool, which is exactly
// what a power loss does to volatile memory.
type BufferPool struct {
	mu    sync.Mutex
	dm    *DiskManager
	pages map[uint64]*Page
}

func NewBufferPool(dm *DiskManager) *BufferPool {
	return &BufferPool{dm: dm, pages: make(map[uint64]*Page)}
}

// Fetch returns the cached page or loads it from disk.
func (bp *BufferPool) Fetch(id uint64) (*Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if p, ok := bp.pages[id]; ok {
		return p, nil
	}
	p, err := bp.dm.ReadPage(id)
	if err != nil {
		return nil, err
	}
	bp.pages[id] = p
	return p, nil
}

// FlushPage writes a single page to disk if dirty.
func (bp *BufferPool) FlushPage(id uint64) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, ok := bp.pages[id]
	if !ok || !p.Dirty {
		return nil
	}
	if err := bp.dm.WritePage(p); err != nil {
		return err
	}
	p.Dirty = false
	return nil
}

// FlushAll writes every dirty page to disk and returns the IDs flushed.
func (bp *BufferPool) FlushAll() ([]uint64, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	var flushed []uint64
	for id, p := range bp.pages {
		if !p.Dirty {
			continue
		}
		if err := bp.dm.WritePage(p); err != nil {
			return nil, err
		}
		p.Dirty = false
		flushed = append(flushed, id)
	}
	return flushed, nil
}
