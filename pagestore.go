package aries

import (
	"encoding/binary"
	"errors"
	"os"
	"sort"
	"sync"
)

// PageSize is the fixed database page size in bytes.
const PageSize = 4096

const (
	pageFileMagic      uint32 = 0x41524442 // "ARDB"
	pageFileVersion    uint16 = 1
	pageFileHeaderSize        = 8
	pageDiskRecordSize        = 12 + PageSize // pageID(4) + pageLSN(8) + data
)

// ErrPageOverflow is returned when a write does not fit inside a page.
var ErrPageOverflow = errors.New("aries: page write exceeds page size")

// Page is one in-memory database page. LSN is the pageLSN: the LSN of the
// most recent log record applied to this page.
type Page struct {
	ID   uint32
	LSN  uint64
	Data [PageSize]byte
}

type diskPage struct {
	lsn  uint64
	data [PageSize]byte
}

// PageStore is a page-oriented store combining an in-memory buffer pool
// (pages) with a persisted on-disk snapshot (disk). Flush/FlushPage move
// pages from memory to the disk snapshot; a crash simply discards the
// in-memory state. All methods are safe for concurrent use.
type PageStore struct {
	mu     sync.RWMutex
	path   string
	pages  map[uint32]*Page
	disk   map[uint32]*diskPage
	dirty  map[uint32]struct{}
	closed bool
}

// OpenPageStore loads the persisted pages from path, if it exists.
func OpenPageStore(path string) (*PageStore, error) {
	ps := &PageStore{
		path:  path,
		pages: make(map[uint32]*Page),
		disk:  make(map[uint32]*diskPage),
		dirty: make(map[uint32]struct{}),
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ps, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return ps, nil
	}
	if len(data) < pageFileHeaderSize {
		return nil, ErrShortBuffer
	}
	if binary.BigEndian.Uint32(data[0:4]) != pageFileMagic {
		return nil, ErrBadMagic
	}
	off := pageFileHeaderSize
	for off < len(data) {
		if len(data)-off < pageDiskRecordSize {
			return nil, ErrMalformed
		}
		id := binary.BigEndian.Uint32(data[off:])
		lsn := binary.BigEndian.Uint64(data[off+4:])
		dp := &diskPage{lsn: lsn}
		copy(dp.data[:], data[off+12:off+12+PageSize])
		ps.disk[id] = dp
		pg := &Page{ID: id, LSN: lsn}
		copy(pg.Data[:], dp.data[:])
		ps.pages[id] = pg
		off += pageDiskRecordSize
	}
	return ps, nil
}

func (ps *PageStore) fetchLocked(id uint32) *Page {
	pg, ok := ps.pages[id]
	if !ok {
		pg = &Page{ID: id}
		ps.pages[id] = pg
	}
	return pg
}

// ApplyUpdate writes data at offset inside the page and stamps the pageLSN.
// data is copied into the page, so callers may pass zero-copy log views.
func (ps *PageStore) ApplyUpdate(pageID uint32, offset uint32, data []byte, lsn uint64) error {
	if uint64(offset)+uint64(len(data)) > PageSize {
		return ErrPageOverflow
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return ErrClosed
	}
	pg := ps.fetchLocked(pageID)
	copy(pg.Data[offset:], data)
	pg.LSN = lsn
	ps.dirty[pageID] = struct{}{}
	return nil
}

// PageLSN returns the pageLSN of a page (0 if never touched).
func (ps *PageStore) PageLSN(pageID uint32) uint64 {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	if pg, ok := ps.pages[pageID]; ok {
		return pg.LSN
	}
	return 0
}

// ReadAt returns a copy of n bytes at offset inside the page.
func (ps *PageStore) ReadAt(pageID uint32, offset uint32, n int) ([]byte, error) {
	if n < 0 || uint64(offset)+uint64(n) > PageSize {
		return nil, ErrPageOverflow
	}
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	out := make([]byte, n)
	if pg, ok := ps.pages[pageID]; ok {
		copy(out, pg.Data[offset:])
	}
	return out, nil
}

// FlushPage persists a single page to the disk snapshot.
func (ps *PageStore) FlushPage(pageID uint32) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return ErrClosed
	}
	if pg, ok := ps.pages[pageID]; ok {
		dp := &diskPage{lsn: pg.LSN}
		copy(dp.data[:], pg.Data[:])
		ps.disk[pageID] = dp
		delete(ps.dirty, pageID)
	}
	return ps.persistLocked()
}

// Flush persists all in-memory pages to the disk snapshot.
func (ps *PageStore) Flush() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return ErrClosed
	}
	for id, pg := range ps.pages {
		dp := &diskPage{lsn: pg.LSN}
		copy(dp.data[:], pg.Data[:])
		ps.disk[id] = dp
	}
	ps.dirty = make(map[uint32]struct{})
	return ps.persistLocked()
}

// DirtyCount reports how many in-memory pages have unflushed changes.
func (ps *PageStore) DirtyCount() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.dirty)
}

func (ps *PageStore) persistLocked() error {
	buf := make([]byte, pageFileHeaderSize, pageFileHeaderSize+len(ps.disk)*pageDiskRecordSize)
	binary.BigEndian.PutUint32(buf[0:4], pageFileMagic)
	binary.BigEndian.PutUint16(buf[4:6], pageFileVersion)
	ids := make([]uint32, 0, len(ps.disk))
	for id := range ps.disk {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		dp := ps.disk[id]
		var rec [pageDiskRecordSize]byte
		binary.BigEndian.PutUint32(rec[0:4], id)
		binary.BigEndian.PutUint64(rec[4:12], dp.lsn)
		copy(rec[12:], dp.data[:])
		buf = append(buf, rec[:]...)
	}
	return os.WriteFile(ps.path, buf, 0o644)
}

// Close flushes nothing; it only marks the store closed. Callers must Flush
// explicitly first if durability is required (mirroring a real buffer pool).
func (ps *PageStore) Close() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closed {
		return ErrClosed
	}
	ps.closed = true
	return nil
}
