package aries

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"
)

// DefaultPageSize is the on-disk page size used when 0 is requested.
const DefaultPageSize = 4096

// pageHeaderSize is the per-page on-disk header: an 8-byte pageLSN.
const pageHeaderSize = 8

// PageStore is the durable page file. Page i lives at offset
// i*(header+pageSize); the first 8 bytes of each slot hold the pageLSN.
type PageStore struct {
	mu       sync.Mutex
	f        *os.File
	pageSize int
	closed   bool
}

// OpenPageStore opens or creates the page file. pageSize of 0 selects the
// default. The size of an existing file is authoritative for nothing but
// capacity: pages are addressed sparsely by id.
func OpenPageStore(path string, pageSize int) (*PageStore, error) {
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &PageStore{f: f, pageSize: pageSize}, nil
}

// PageSize returns the usable data bytes per page.
func (ps *PageStore) PageSize() int { return ps.pageSize }

func (ps *PageStore) slotOffset(id uint32) int64 {
	return int64(id) * int64(pageHeaderSize+ps.pageSize)
}

// Read returns the pageLSN and a fresh copy of the page's data. Pages
// beyond the end of the file read as zero pages with pageLSN 0.
func (ps *PageStore) Read(id uint32) (uint64, []byte, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return 0, nil, fmt.Errorf("pagestore: read on closed file")
	}
	buf := make([]byte, pageHeaderSize+ps.pageSize)
	n, err := ps.f.ReadAt(buf, ps.slotOffset(id))
	if err != nil && err != io.EOF {
		return 0, nil, err
	}
	if n < pageHeaderSize {
		return 0, make([]byte, ps.pageSize), nil
	}
	pageLSN := binary.BigEndian.Uint64(buf[:pageHeaderSize])
	data := make([]byte, ps.pageSize)
	copy(data, buf[pageHeaderSize:])
	return pageLSN, data, nil
}

// Write persists a page image together with its pageLSN.
func (ps *PageStore) Write(id uint32, pageLSN uint64, data []byte) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return fmt.Errorf("pagestore: write on closed file")
	}
	if len(data) > ps.pageSize {
		return fmt.Errorf("pagestore: page image %d bytes exceeds page size %d", len(data), ps.pageSize)
	}
	buf := make([]byte, pageHeaderSize+ps.pageSize)
	binary.BigEndian.PutUint64(buf[:pageHeaderSize], pageLSN)
	copy(buf[pageHeaderSize:], data)
	if _, err := ps.f.WriteAt(buf, ps.slotOffset(id)); err != nil {
		return err
	}
	return nil
}

// NumPages returns the number of page slots that fit in the file.
func (ps *PageStore) NumPages() (int, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	st, err := ps.f.Stat()
	if err != nil {
		return 0, err
	}
	slot := int64(pageHeaderSize + ps.pageSize)
	return int(st.Size() / slot), nil
}

// Sync flushes the page file to stable storage.
func (ps *PageStore) Sync() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return nil
	}
	return ps.f.Sync()
}

// Close syncs and closes the page file. It is idempotent.
func (ps *PageStore) Close() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return nil
	}
	ps.closed = true
	if err := ps.f.Sync(); err != nil {
		ps.f.Close()
		return err
	}
	return ps.f.Close()
}

// page is an in-memory buffered page frame.
type page struct {
	data    []byte
	pageLSN uint64
	dirty   bool
}

// BufferPool caches pages, tracks the dirty page table (recLSN per dirty
// page) and the set of pages changed since the last snapshot. All methods
// are safe for concurrent use.
type BufferPool struct {
	mu        sync.Mutex
	store     *PageStore
	pages     map[uint32]*page
	recLSN    map[uint32]uint64 // dirty page table: page -> first LSN that dirtied it
	snapDelta map[uint32]bool   // pages changed since last snapshot
	pageSize  int
}

// NewBufferPool creates a buffer pool over store.
func NewBufferPool(store *PageStore) *BufferPool {
	return &BufferPool{
		store:     store,
		pages:     make(map[uint32]*page),
		recLSN:    make(map[uint32]uint64),
		snapDelta: make(map[uint32]bool),
		pageSize:  store.PageSize(),
	}
}

// getLocked returns the cached page, loading it from the store if needed.
func (bp *BufferPool) getLocked(id uint32) (*page, error) {
	if p, ok := bp.pages[id]; ok {
		return p, nil
	}
	pageLSN, data, err := bp.store.Read(id)
	if err != nil {
		return nil, err
	}
	p := &page{data: data, pageLSN: pageLSN}
	bp.pages[id] = p
	return p, nil
}

// ApplyUpdate writes data at offset within the page, stamps the page with
// lsn, and records the page in the dirty page table and snapshot delta.
func (bp *BufferPool) ApplyUpdate(id uint32, lsn uint64, offset int, data []byte) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, err := bp.getLocked(id)
	if err != nil {
		return err
	}
	if offset+len(data) > bp.pageSize {
		return fmt.Errorf("bufferpool: write of %d bytes at offset %d exceeds page size %d",
			len(data), offset, bp.pageSize)
	}
	copy(p.data[offset:], data)
	p.pageLSN = lsn
	if !p.dirty {
		p.dirty = true
		bp.recLSN[id] = lsn
	}
	bp.snapDelta[id] = true
	return nil
}

// ReadAt returns a copy of n bytes at offset within the page.
func (bp *BufferPool) ReadAt(id uint32, offset, n int) ([]byte, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, err := bp.getLocked(id)
	if err != nil {
		return nil, err
	}
	if offset+n > bp.pageSize {
		return nil, fmt.Errorf("bufferpool: read of %d bytes at offset %d exceeds page size %d",
			n, offset, bp.pageSize)
	}
	out := make([]byte, n)
	copy(out, p.data[offset:offset+n])
	return out, nil
}

// PageLSN returns the current pageLSN of the page (loading it if needed).
func (bp *BufferPool) PageLSN(id uint32) (uint64, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, err := bp.getLocked(id)
	if err != nil {
		return 0, err
	}
	return p.pageLSN, nil
}

// DirtyPageTable returns a copy of the dirty page table (page -> recLSN).
func (bp *BufferPool) DirtyPageTable() map[uint32]uint64 {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	out := make(map[uint32]uint64, len(bp.recLSN))
	for id, lsn := range bp.recLSN {
		out[id] = lsn
	}
	return out
}

// FlushAll writes every dirty page to the store and clears the dirty page
// table. It does not clear the snapshot delta set.
func (bp *BufferPool) FlushAll() error {
	bp.mu.Lock()
	dirty := make(map[uint32]page, len(bp.recLSN))
	for id := range bp.recLSN {
		if p, ok := bp.pages[id]; ok && p.dirty {
			dirty[id] = page{data: append([]byte(nil), p.data...), pageLSN: p.pageLSN}
		}
	}
	bp.mu.Unlock()

	for id, p := range dirty {
		if err := bp.store.Write(id, p.pageLSN, p.data); err != nil {
			return err
		}
	}
	if err := bp.store.Sync(); err != nil {
		return err
	}

	bp.mu.Lock()
	for id := range dirty {
		if p, ok := bp.pages[id]; ok {
			p.dirty = false
		}
		delete(bp.recLSN, id)
	}
	bp.mu.Unlock()
	return nil
}

// SnapshotPage is an immutable page image captured by a snapshot.
type SnapshotPage struct {
	PageLSN uint64
	Data    []byte
}

// CollectDelta returns copies of all pages changed since the last
// collection and resets the delta set. The pool lock is held only for the
// in-memory copy, keeping the stall proportional to the changed set.
func (bp *BufferPool) CollectDelta() map[uint32]SnapshotPage {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	out := make(map[uint32]SnapshotPage, len(bp.snapDelta))
	for id := range bp.snapDelta {
		if p, ok := bp.pages[id]; ok {
			out[id] = SnapshotPage{PageLSN: p.pageLSN, Data: append([]byte(nil), p.data...)}
		}
	}
	bp.snapDelta = make(map[uint32]bool)
	return out
}

// CollectAll returns copies of every allocated page (cached or only on
// disk) and resets the delta set. Used for the base (full) snapshot.
func (bp *BufferPool) CollectAll() (map[uint32]SnapshotPage, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	n, err := bp.store.NumPages()
	if err != nil {
		return nil, err
	}
	out := make(map[uint32]SnapshotPage, n)
	for i := 0; i < n; i++ {
		id := uint32(i)
		p, err := bp.getLocked(id)
		if err != nil {
			return nil, err
		}
		out[id] = SnapshotPage{PageLSN: p.pageLSN, Data: append([]byte(nil), p.data...)}
	}
	// Pages updated but never flushed may exist beyond the current file
	// size; include any cached page not covered above.
	for id, p := range bp.pages {
		if _, ok := out[id]; !ok {
			out[id] = SnapshotPage{PageLSN: p.pageLSN, Data: append([]byte(nil), p.data...)}
		}
	}
	bp.snapDelta = make(map[uint32]bool)
	return out, nil
}
