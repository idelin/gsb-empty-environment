// Package storage models stable storage: a page-addressed disk.
package storage

import (
	"sync"

	"aries/wal"
)

// PageSize is the fixed size of a page image in bytes.
const PageSize = 64

// Page is a versioned page image. LSN is the pageLSN: the log
// sequence number of the record that most recently modified it.
type Page struct {
	Data []byte
	LSN  wal.LSN
}

// Disk is a thread-safe in-memory model of stable storage.
type Disk struct {
	mu    sync.RWMutex
	pages map[uint32]Page
}

func NewDisk() *Disk {
	return &Disk{pages: make(map[uint32]Page)}
}

// Read returns a copy of the page; ok is false if it does not exist.
func (d *Disk) Read(id uint32) (Page, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.pages[id]
	if !ok {
		return Page{}, false
	}
	return Page{Data: append([]byte(nil), p.Data...), LSN: p.LSN}, true
}

// Write stores a page image with its pageLSN.
func (d *Disk) Write(id uint32, data []byte, lsn wal.LSN) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	d.pages[id] = Page{Data: cp, LSN: lsn}
}

// PageLSN returns the pageLSN of a page (NilLSN if absent).
func (d *Disk) PageLSN(id uint32) wal.LSN {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.pages[id].LSN
}

// Snapshot returns a deep copy of all pages.
func (d *Disk) Snapshot() map[uint32]Page {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make(map[uint32]Page, len(d.pages))
	for id, p := range d.pages {
		out[id] = Page{Data: append([]byte(nil), p.Data...), LSN: p.LSN}
	}
	return out
}
