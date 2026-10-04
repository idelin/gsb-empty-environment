package aries

import (
	"fmt"
	"os"
	"sync"
)

// PageSize is the on-disk page size: 8 bytes of pageLSN header + payload.
const PageSize = 256

// PayloadSize is the usable payload per page.
const PayloadSize = PageSize - 8

// Page is an in-memory image of a disk page.
type Page struct {
	ID      uint64
	LSN     uint64 // pageLSN: LSN of the most recent log record applied
	Payload []byte
}

// DiskManager stores pages in a single file, page i at offset i*PageSize.
type DiskManager struct {
	mu sync.Mutex
	f  *os.File
}

// OpenDisk opens (or creates) the data file.
func OpenDisk(path string) (*DiskManager, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	return &DiskManager{f: f}, nil
}

// Close closes the underlying file.
func (d *DiskManager) Close() error { return d.f.Close() }

// ReadPage loads page id from disk. Missing pages read as zero pages.
func (d *DiskManager) ReadPage(id uint64) (*Page, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	buf := make([]byte, PageSize)
	n, err := d.f.ReadAt(buf, int64(id)*PageSize)
	if err != nil && n == 0 {
		// beyond EOF: virgin page
		return &Page{ID: id, Payload: make([]byte, PayloadSize)}, nil
	}
	p := &Page{ID: id, Payload: make([]byte, PayloadSize)}
	p.LSN = uint64(buf[0]) | uint64(buf[1])<<8 | uint64(buf[2])<<16 | uint64(buf[3])<<24 |
		uint64(buf[4])<<32 | uint64(buf[5])<<40 | uint64(buf[6])<<48 | uint64(buf[7])<<56
	copy(p.Payload, buf[8:])
	return p, nil
}

// WritePage persists a page image and its pageLSN.
func (d *DiskManager) WritePage(p *Page) error {
	if len(p.Payload) != PayloadSize {
		return fmt.Errorf("disk: payload for page %d has %d bytes, want %d", p.ID, len(p.Payload), PayloadSize)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf := make([]byte, PageSize)
	lsn := p.LSN
	for i := 0; i < 8; i++ {
		buf[i] = byte(lsn >> (8 * uint(i)))
	}
	copy(buf[8:], p.Payload)
	if _, err := d.f.WriteAt(buf, int64(p.ID)*PageSize); err != nil {
		return err
	}
	return nil
}

// Sync flushes the data file to stable storage.
func (d *DiskManager) Sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.f.Sync()
}

// BufferPool caches pages and tracks the dirty page table (recLSN per page).
// It exists only to simulate a running system; a crash simply drops it.
type BufferPool struct {
	disk   *DiskManager
	pages  map[uint64]*Page
	recLSN map[uint64]uint64 // dirty pages: first LSN that dirtied the page since flush
}

// NewBufferPool creates an empty buffer pool over disk.
func NewBufferPool(disk *DiskManager) *BufferPool {
	return &BufferPool{disk: disk, pages: make(map[uint64]*Page), recLSN: make(map[uint64]uint64)}
}

// Fetch returns the buffered page, loading it from disk on a miss.
func (bp *BufferPool) Fetch(id uint64) (*Page, error) {
	if p, ok := bp.pages[id]; ok {
		return p, nil
	}
	p, err := bp.disk.ReadPage(id)
	if err != nil {
		return nil, err
	}
	bp.pages[id] = p
	return p, nil
}

// Apply installs a new payload and pageLSN, marking the page dirty.
func (bp *BufferPool) Apply(id uint64, lsn uint64, payload []byte) error {
	p, err := bp.Fetch(id)
	if err != nil {
		return err
	}
	if _, dirty := bp.recLSN[id]; !dirty {
		bp.recLSN[id] = lsn
	}
	copy(p.Payload, payload)
	p.LSN = lsn
	return nil
}

// Flush writes one page to disk and removes it from the DPT.
func (bp *BufferPool) Flush(id uint64) error {
	p, ok := bp.pages[id]
	if !ok {
		return nil
	}
	if err := bp.disk.WritePage(p); err != nil {
		return err
	}
	delete(bp.recLSN, id)
	return nil
}

// DirtyPageTable snapshots the current DPT (pageID -> recLSN).
func (bp *BufferPool) DirtyPageTable() map[uint64]uint64 {
	out := make(map[uint64]uint64, len(bp.recLSN))
	for k, v := range bp.recLSN {
		out[k] = v
	}
	return out
}
