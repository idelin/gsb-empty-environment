// Package storage provides a page-oriented disk manager and a buffer pool
// that enforces the WAL-first rule (a dirty page is never written to disk
// before the log records describing it are durable).
package storage

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"

	"aries/metrics"
	"aries/wal"
)

// PageSize is the fixed page size in bytes.
const PageSize = 4096

// DataOffset is the first byte usable for tuple data; bytes [0,8) of every
// page persist the pageLSN so recovery can compare it against log records.
const DataOffset = 8

// ErrNoFreeFrame is returned when every buffer frame is pinned.
var ErrNoFreeFrame = errors.New("storage: no evictable buffer frame")

// Page is one buffer-pool frame.
type Page struct {
	ID     int32
	Data   [PageSize]byte
	LSN    wal.LSN // pageLSN: newest log record applied to this page
	recLSN wal.LSN // oldest log record that dirtied the page since last flush
	dirty  bool
	pins   int
}

// Dirty reports whether the page has unflushed modifications.
func (p *Page) Dirty() bool { return p.dirty }

// RecLSN returns the first LSN that dirtied the page since its last flush.
func (p *Page) RecLSN() wal.LSN { return p.recLSN }

// ---------------------------------------------------------------------------
// DiskManager
// ---------------------------------------------------------------------------

// DiskManager reads and writes fixed-size pages by page id.
type DiskManager struct {
	mu   sync.Mutex
	file *os.File
}

// OpenDiskManager opens (or creates) the data file.
func OpenDiskManager(path string) (*DiskManager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}
	return &DiskManager{file: f}, nil
}

// ReadPage loads page id into p. Pages beyond the end of the file read as
// zero-filled with LSN 0.
func (dm *DiskManager) ReadPage(id int32, p *Page) error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	n, err := dm.file.ReadAt(p.Data[:], int64(id)*PageSize)
	if err != nil && err != io.EOF {
		return err
	}
	if n < PageSize {
		for i := n; i < PageSize; i++ {
			p.Data[i] = 0
		}
	}
	p.ID = id
	p.LSN = wal.LSN(binary.BigEndian.Uint64(p.Data[0:8]))
	p.dirty = false
	p.recLSN = wal.LSNInvalid
	p.pins = 0
	return nil
}

// WritePage persists p, stamping its pageLSN into the page header.
func (dm *DiskManager) WritePage(p *Page) error {
	binary.BigEndian.PutUint64(p.Data[0:8], uint64(p.LSN))
	dm.mu.Lock()
	defer dm.mu.Unlock()
	_, err := dm.file.WriteAt(p.Data[:], int64(p.ID)*PageSize)
	return err
}

// Sync fsyncs the data file.
func (dm *DiskManager) Sync() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.file.Sync()
}

// Close closes the data file.
func (dm *DiskManager) Close() error {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.file.Close()
}

// ---------------------------------------------------------------------------
// BufferPool
// ---------------------------------------------------------------------------

// BufferPool caches pages with clock eviction and WAL-first flushing.
// It is safe for concurrent use.
type BufferPool struct {
	mu       sync.Mutex
	dm       *DiskManager
	lm       *wal.LogManager // may be nil (standalone use)
	reg      *metrics.Registry
	capacity int
	frames   []*Page
	table    map[int32]*Page
	cursor   int
	hits     int64
	misses   int64
}

// NewBufferPool builds a pool over dm. lm may be nil; when set, the pool
// flushes the log before writing any dirty page (WAL-first rule). reg may
// be nil; when set, hit/miss counters are mirrored into it.
func NewBufferPool(dm *DiskManager, lm *wal.LogManager, capacity int, reg *metrics.Registry) *BufferPool {
	if capacity <= 0 {
		capacity = 1
	}
	return &BufferPool{
		dm:       dm,
		lm:       lm,
		reg:      reg,
		capacity: capacity,
		table:    make(map[int32]*Page),
	}
}

func (bp *BufferPool) count(name string) {
	if bp.reg != nil {
		bp.reg.Counter(name).Inc()
	}
}

// FetchPage pins and returns the page, loading it from disk on a miss.
func (bp *BufferPool) FetchPage(id int32) (*Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if p, ok := bp.table[id]; ok {
		p.pins++
		atomic.AddInt64(&bp.hits, 1)
		bp.count("buffer.hits")
		return p, nil
	}
	atomic.AddInt64(&bp.misses, 1)
	bp.count("buffer.misses")
	var p *Page
	if len(bp.frames) < bp.capacity {
		p = &Page{}
		bp.frames = append(bp.frames, p)
	} else {
		victim, err := bp.evictLocked()
		if err != nil {
			return nil, err
		}
		delete(bp.table, victim.ID)
		p = victim
	}
	if err := bp.dm.ReadPage(id, p); err != nil {
		return nil, err
	}
	p.pins = 1
	bp.table[id] = p
	return p, nil
}

// UnpinPage releases a pin; dirty marks the page modified. The first dirty
// marking after a flush records the page's current LSN as its recLSN.
func (bp *BufferPool) UnpinPage(id int32, dirty bool) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, ok := bp.table[id]
	if !ok {
		return
	}
	if p.pins > 0 {
		p.pins--
	}
	if dirty && !p.dirty {
		p.dirty = true
		p.recLSN = p.LSN
	}
}

// evictLocked finds an unpinned victim with a clock sweep, flushing it first
// if dirty (honoring the WAL-first rule).
func (bp *BufferPool) evictLocked() (*Page, error) {
	for i := 0; i < 2*len(bp.frames); i++ {
		p := bp.frames[bp.cursor]
		bp.cursor = (bp.cursor + 1) % len(bp.frames)
		if p.pins > 0 {
			continue
		}
		if err := bp.writePageLocked(p); err != nil {
			return nil, err
		}
		return p, nil
	}
	return nil, ErrNoFreeFrame
}

// writePageLocked flushes p to disk if dirty, enforcing WAL-first.
func (bp *BufferPool) writePageLocked(p *Page) error {
	if !p.dirty {
		return nil
	}
	if bp.lm != nil && bp.lm.FlushedLSN() <= p.LSN {
		if err := bp.lm.Flush(); err != nil {
			return err
		}
	}
	if err := bp.dm.WritePage(p); err != nil {
		return err
	}
	p.dirty = false
	p.recLSN = wal.LSNInvalid
	return nil
}

// FlushPage writes the page to disk if it is dirty and present in the pool.
func (bp *BufferPool) FlushPage(id int32) error {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	p, ok := bp.table[id]
	if !ok {
		return nil
	}
	return bp.writePageLocked(p)
}

// FlushAll durably flushes the log and every dirty page, then fsyncs disk.
func (bp *BufferPool) FlushAll() error {
	if bp.lm != nil {
		if err := bp.lm.Flush(); err != nil {
			return err
		}
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	for _, p := range bp.frames {
		if err := bp.writePageLocked(p); err != nil {
			return err
		}
	}
	return bp.dm.Sync()
}

// DirtyPages returns pageID -> recLSN for every dirty page (the DPT snapshot
// used by checkpoints).
func (bp *BufferPool) DirtyPages() map[int32]wal.LSN {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	dpt := make(map[int32]wal.LSN)
	for _, p := range bp.frames {
		if p.dirty {
			dpt[p.ID] = p.recLSN
		}
	}
	return dpt
}

// Hits returns the number of buffer-pool hits.
func (bp *BufferPool) Hits() int64 { return atomic.LoadInt64(&bp.hits) }

// Misses returns the number of buffer-pool misses.
func (bp *BufferPool) Misses() int64 { return atomic.LoadInt64(&bp.misses) }

// HitRate returns hits / (hits + misses), or 0 when no accesses occurred.
func (bp *BufferPool) HitRate() float64 {
	hits, misses := bp.Hits(), bp.Misses()
	if hits+misses == 0 {
		return 0
	}
	return float64(hits) / float64(hits+misses)
}
