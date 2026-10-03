// Package storage models a paged disk file and a buffer pool with
// pageLSN tracking, the pieces of durable state ARIES reasons about.
package storage

import (
	"fmt"
	"os"
	"sync"
)

// PageSize is the payload capacity of a single page in bytes.
const PageSize = 4096

// slotSize = 8-byte pageLSN header + 2-byte data length + payload.
const slotSize = 8 + 2 + PageSize

// DiskStore is a file-backed page store. It represents durable
// storage: contents survive a simulated crash (i.e. the loss of the
// buffer pool). Safe for concurrent use.
type DiskStore struct {
	mu sync.RWMutex
	f  *os.File
}

// OpenDiskStore opens (or creates) the page file at path.
func OpenDiskStore(path string) (*DiskStore, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	return &DiskStore{f: f}, nil
}

// ReadPage returns the pageLSN and payload of pageID. A page that was
// never written reads as LSN 0 with a nil payload.
func (d *DiskStore) ReadPage(pageID int) (uint64, []byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.readPageLocked(pageID)
}

func (d *DiskStore) readPageLocked(pageID int) (uint64, []byte, error) {
	hdr := make([]byte, 10)
	n, err := d.f.ReadAt(hdr, d.offset(pageID))
	if n == 0 {
		return 0, nil, nil
	}
	if err != nil {
		return 0, nil, fmt.Errorf("storage: read page %d: %w", pageID, err)
	}
	lsn := uint64From(hdr[0:8])
	dataLen := int(uint16From(hdr[8:10]))
	data := make([]byte, dataLen)
	if dataLen > 0 {
		if _, err := d.f.ReadAt(data, d.offset(pageID)+10); err != nil {
			return 0, nil, fmt.Errorf("storage: read page %d payload: %w", pageID, err)
		}
	}
	return lsn, data, nil
}

// WritePage persists the payload and pageLSN of pageID.
func (d *DiskStore) WritePage(pageID int, lsn uint64, data []byte) error {
	if len(data) > PageSize {
		return fmt.Errorf("storage: payload %d bytes exceeds page size %d", len(data), PageSize)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	slot := make([]byte, slotSize)
	putUint64(slot[0:8], lsn)
	putUint16(slot[8:10], uint16(len(data)))
	copy(slot[10:], data)
	if _, err := d.f.WriteAt(slot, d.offset(pageID)); err != nil {
		return fmt.Errorf("storage: write page %d: %w", pageID, err)
	}
	return nil
}

// PageLSN returns the durable pageLSN of pageID (0 if never written).
func (d *DiskStore) PageLSN(pageID int) uint64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	hdr := make([]byte, 8)
	if n, _ := d.f.ReadAt(hdr, d.offset(pageID)); n < 8 {
		return 0
	}
	return uint64From(hdr)
}

// Sync forces the page file to durable storage.
func (d *DiskStore) Sync() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.f.Sync()
}

// Close closes the page file.
func (d *DiskStore) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.f.Close()
}

func (d *DiskStore) offset(pageID int) int64 {
	return int64(pageID) * slotSize
}

// FrameMeta is an inspectable snapshot of a buffered page.
type FrameMeta struct {
	LSN   uint64
	Dirty bool
}

type frame struct {
	lsn   uint64
	data  []byte
	dirty bool
}

// BufferPool caches pages in memory and tracks, per page, the LSN of
// the most recent log record applied to it. Safe for concurrent use.
type BufferPool struct {
	mu     sync.RWMutex
	disk   *DiskStore
	frames map[int]*frame
}

// NewBufferPool creates a buffer pool over disk.
func NewBufferPool(disk *DiskStore) *BufferPool {
	return &BufferPool{disk: disk, frames: make(map[int]*frame)}
}

// Fetch returns the current payload of pageID and its pageLSN,
// loading the page from disk on first access.
func (p *BufferPool) Fetch(pageID int) ([]byte, uint64, error) {
	p.mu.RLock()
	fr, ok := p.frames[pageID]
	p.mu.RUnlock()
	if ok {
		cp := make([]byte, len(fr.data))
		copy(cp, fr.data)
		return cp, fr.lsn, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if fr, ok := p.frames[pageID]; ok {
		cp := make([]byte, len(fr.data))
		copy(cp, fr.data)
		return cp, fr.lsn, nil
	}
	lsn, data, err := p.disk.ReadPage(pageID)
	if err != nil {
		return nil, 0, err
	}
	p.frames[pageID] = &frame{lsn: lsn, data: data}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, lsn, nil
}

// Apply installs image as the page contents and stamps the page with
// the given LSN, marking it dirty.
func (p *BufferPool) Apply(pageID int, image []byte, lsn uint64) error {
	if len(image) > PageSize {
		return fmt.Errorf("storage: image %d bytes exceeds page size %d", len(image), PageSize)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]byte, len(image))
	copy(cp, image)
	p.frames[pageID] = &frame{lsn: lsn, data: cp, dirty: true}
	return nil
}

// Flush writes pageID to disk if it is dirty.
func (p *BufferPool) Flush(pageID int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	fr, ok := p.frames[pageID]
	if !ok || !fr.dirty {
		return nil
	}
	if err := p.disk.WritePage(pageID, fr.lsn, fr.data); err != nil {
		return err
	}
	fr.dirty = false
	return nil
}

// FlushAll writes every dirty page to disk and syncs the page file.
func (p *BufferPool) FlushAll() error {
	p.mu.Lock()
	for id, fr := range p.frames {
		if !fr.dirty {
			continue
		}
		if err := p.disk.WritePage(id, fr.lsn, fr.data); err != nil {
			p.mu.Unlock()
			return err
		}
		fr.dirty = false
	}
	p.mu.Unlock()
	p.disk.Sync()
	return nil
}

// Snapshot returns per-page metadata for inspection.
func (p *BufferPool) Snapshot() map[int]FrameMeta {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[int]FrameMeta, len(p.frames))
	for id, fr := range p.frames {
		out[id] = FrameMeta{LSN: fr.lsn, Dirty: fr.dirty}
	}
	return out
}

func uint64From(b []byte) uint64 {
	var v uint64
	for i := 0; i < 8; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v
}

func putUint64(b []byte, v uint64) {
	for i := 7; i >= 0; i-- {
		b[i] = byte(v)
		v >>= 8
	}
}

func uint16From(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

func putUint16(b []byte, v uint16) {
	b[0] = byte(v >> 8)
	b[1] = byte(v)
}
