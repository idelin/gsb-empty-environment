package waldb

import (
	"encoding/binary"
	"sort"
)

// NumPages is the fixed number of pages in the database.
const NumPages = 32

// PageSize is the on-disk page size in bytes: PageLSN(8) Value(8).
const PageSize = 16

// page is the in-memory (volatile) copy of a disk page.
type page struct {
	id      uint32
	value   int64
	pageLSN uint64
	dirty   bool
}

// bufferPool is a steal/no-force buffer pool: dirty pages may be
// written back at any time (steal) and commit never forces pages
// (no-force). Correctness comes from the WAL rule in flushAll.
type bufferPool struct {
	db    *DB
	pages map[uint32]*page
}

func newBufferPool(db *DB) *bufferPool {
	return &bufferPool{db: db, pages: map[uint32]*page{}}
}

// fix returns the cached page, reading it from disk if necessary.
func (bp *bufferPool) fix(id uint32) (*page, error) {
	if p, ok := bp.pages[id]; ok {
		return p, nil
	}
	buf := make([]byte, PageSize)
	if _, err := bp.db.data.ReadAt(buf, int64(id)*PageSize); err != nil {
		return nil, err
	}
	p := &page{
		id:      id,
		pageLSN: binary.LittleEndian.Uint64(buf[0:8]),
		value:   int64(binary.LittleEndian.Uint64(buf[8:16])),
	}
	bp.pages[id] = p
	return p, nil
}

// flushAll writes every dirty page to disk. WAL rule: before a page
// hits disk, the log must be durable up to that page's pageLSN.
func (bp *bufferPool) flushAll() error {
	ids := make([]int, 0, len(bp.pages))
	for id := range bp.pages {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		p := bp.pages[uint32(id)]
		if !p.dirty {
			continue
		}
		bp.db.lm.flush(p.pageLSN)
		buf := make([]byte, PageSize)
		binary.LittleEndian.PutUint64(buf[0:8], p.pageLSN)
		binary.LittleEndian.PutUint64(buf[8:16], uint64(p.value))
		if err := bp.db.data.WriteAt(buf, int64(p.id)*PageSize); err != nil {
			return err
		}
		p.dirty = false
	}
	return bp.db.data.Sync()
}
