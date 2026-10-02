package aries

import (
	"encoding/binary"
	"sync"
)

// PageHeaderSize bytes at the start of every page hold the pageLSN,
// i.e. the LSN of the most recent log record applied to the page.
const PageHeaderSize = 8

// Page is one in-memory copy of a disk page.
type Page struct {
	id    PageID
	data  []byte // full page including the header
	dirty bool
}

// LSN returns the pageLSN stored in the page header.
func (p *Page) LSN() LSN {
	return LSN(binary.LittleEndian.Uint64(p.data[:PageHeaderSize]))
}

// SetLSN stores the pageLSN in the page header.
func (p *Page) SetLSN(lsn LSN) {
	binary.LittleEndian.PutUint64(p.data[:PageHeaderSize], uint64(lsn))
}

// Data returns the usable payload area of the page (after the header).
func (p *Page) Data() []byte {
	return p.data[PageHeaderSize:]
}

// BufferPool caches pages in memory. Dirty pages are written back only
// on checkpoint or clean shutdown — never automatically — so a crash
// genuinely loses them and recovery must reconstruct them from the WAL.
type BufferPool struct {
	mu       sync.Mutex
	pages    map[PageID]*Page
	dm       *DiskManager
	pageSize int
}

func newBufferPool(dm *DiskManager, pageSize int) *BufferPool {
	return &BufferPool{
		pages:    make(map[PageID]*Page),
		dm:       dm,
		pageSize: pageSize,
	}
}

// Fetch returns the page from the pool, loading it from disk if needed.
func (bp *BufferPool) Fetch(id PageID) (*Page, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	if p, ok := bp.pages[id]; ok {
		return p, nil
	}
	data := make([]byte, bp.pageSize)
	if err := bp.dm.ReadPage(id, data); err != nil {
		return nil, err
	}
	p := &Page{id: id, data: data}
	bp.pages[id] = p
	return p, nil
}

// FlushAll writes every dirty page to disk (WAL first: the caller must
// have flushed the log) and returns the IDs of the flushed pages.
func (bp *BufferPool) FlushAll() ([]PageID, error) {
	bp.mu.Lock()
	defer bp.mu.Unlock()
	var flushed []PageID
	for id, p := range bp.pages {
		if !p.dirty {
			continue
		}
		if err := bp.dm.WritePage(id, p.data); err != nil {
			return nil, err
		}
		p.dirty = false
		flushed = append(flushed, id)
	}
	return flushed, nil
}
