package aries

import (
	"encoding/binary"
	"io"
	"os"
	"sync"
)

// PageSize is the on-disk page size: 8 bytes of pageLSN plus payload.
const PageSize = 4096

// PageDataSize is the usable payload per page.
const PageDataSize = PageSize - 8

// Page is an in-memory copy of a disk page.
type Page struct {
	ID   uint64
	LSN  LSN // LSN of the most recent log record applied to this page
	Data []byte
}

// NewPage returns a zeroed page.
func NewPage(id uint64) *Page {
	return &Page{ID: id, Data: make([]byte, PageDataSize)}
}

// PageStore is a page-slotted file. Page i occupies bytes [i*PageSize,
// (i+1)*PageSize). Writes are assumed atomic per page (sector atomicity).
type PageStore struct {
	mu  sync.Mutex
	f   *os.File
	inj *FaultInjector
}

// OpenPageStore opens (or creates) the page file at path.
func OpenPageStore(path string, inj *FaultInjector) (*PageStore, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	return &PageStore{f: f, inj: inj}, nil
}

// ReadPage loads a page from disk. A page that was never written reads as a
// zero page with pageLSN 0.
func (ps *PageStore) ReadPage(id uint64) (*Page, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	p := NewPage(id)
	buf := make([]byte, PageSize)
	n, err := ps.f.ReadAt(buf, int64(id)*PageSize)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if n >= 8 {
		p.LSN = binary.BigEndian.Uint64(buf[:8])
		copy(p.Data, buf[8:])
	}
	return p, nil
}

// WritePage persists a page, consulting the fault injector first.
func (ps *PageStore) WritePage(p *Page) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if err := ps.inj.beforePageWrite(); err != nil {
		return err
	}
	buf := make([]byte, PageSize)
	binary.BigEndian.PutUint64(buf[:8], p.LSN)
	copy(buf[8:], p.Data)
	if _, err := ps.f.WriteAt(buf, int64(p.ID)*PageSize); err != nil {
		return err
	}
	return ps.f.Sync()
}

// Close closes the underlying file.
func (ps *PageStore) Close() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.f.Close()
}
