package aries

import "sync"

// PageSize is the fixed page size in bytes.
const PageSize = 4096

// Page is a slotted-agnostic raw data page. LSN is the pageLSN: the LSN of
// the most recent log record whose update is reflected in Data.
type Page struct {
	ID   int64
	LSN  LSN
	Data [PageSize]byte
}

// Disk simulates durable storage. It survives the simulated crash; the
// buffer pool inside DB does not.
type Disk struct {
	mu    sync.Mutex
	pages map[int64]*Page
}

// NewDisk returns an empty disk.
func NewDisk() *Disk { return &Disk{pages: make(map[int64]*Page)} }

// ReadPage returns a private copy of the page (a zero page if never written).
func (d *Disk) ReadPage(id int64) *Page {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.pages[id]
	if !ok {
		return &Page{ID: id}
	}
	cp := *p
	return &cp
}

// WritePage stores a private copy of p.
func (d *Disk) WritePage(p *Page) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := *p
	d.pages[p.ID] = &cp
}
