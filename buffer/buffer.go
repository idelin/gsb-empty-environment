// Package buffer implements a simplified buffer pool with a page
// table, FIFO replacement, dirty-page write-back and hit/miss
// accounting. Stable storage is modelled as an in-memory disk map;
// Crash drops all unpinned in-memory state to simulate a crash.
package buffer

import (
	"sync"

	"aries/metrics"
	"aries/wal"
)

// Page is a single database page with its durable LSN.
type Page struct {
	ID    uint64
	LSN   wal.LSN
	Data  []byte
	Dirty bool
}

// Pool is a fixed-capacity buffer pool over a simulated disk.
type Pool struct {
	mu       sync.Mutex
	capacity int
	frames   map[uint64]*Page
	order    []uint64 // FIFO eviction order
	disk     map[uint64]Page
	hits     *metrics.Counter
	misses   *metrics.Counter
}

// NewPool creates a pool; hits and misses receive access accounting.
func NewPool(capacity int, hits, misses *metrics.Counter) *Pool {
	return &Pool{
		capacity: capacity,
		frames:   make(map[uint64]*Page),
		disk:     make(map[uint64]Page),
		hits:     hits,
		misses:   misses,
	}
}

// Fetch returns the in-memory copy of page id, loading it from disk
// (or creating an empty page) on a miss. Callers may mutate the
// returned page; they must set Dirty when modifying it.
func (p *Pool) Fetch(id uint64) *Page {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg, ok := p.frames[id]; ok {
		p.hits.Inc()
		return pg
	}
	p.misses.Inc()
	pg := &Page{ID: id}
	if d, ok := p.disk[id]; ok {
		pg.LSN = d.LSN
		pg.Data = append([]byte(nil), d.Data...)
	}
	p.insertLocked(pg)
	return pg
}

func (p *Pool) insertLocked(pg *Page) {
	for len(p.order) >= p.capacity {
		victim := p.order[0]
		p.order = p.order[1:]
		if v, ok := p.frames[victim]; ok {
			if v.Dirty {
				p.writeBackLocked(v)
			}
			delete(p.frames, victim)
		}
	}
	p.frames[pg.ID] = pg
	p.order = append(p.order, pg.ID)
}

func (p *Pool) writeBackLocked(pg *Page) {
	p.disk[pg.ID] = Page{ID: pg.ID, LSN: pg.LSN, Data: append([]byte(nil), pg.Data...)}
	pg.Dirty = false
}

// FlushPage writes page id back to disk if present and dirty.
func (p *Pool) FlushPage(id uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pg, ok := p.frames[id]; ok && pg.Dirty {
		p.writeBackLocked(pg)
	}
}

// FlushAll writes back every dirty page.
func (p *Pool) FlushAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, pg := range p.frames {
		if pg.Dirty {
			p.writeBackLocked(pg)
		}
	}
}

// Crash simulates a system crash: all in-memory frames are lost
// without write-back; disk contents survive.
func (p *Pool) Crash() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.frames = make(map[uint64]*Page)
	p.order = nil
}

// DiskPage returns the durable copy of page id.
func (p *Pool) DiskPage(id uint64) (Page, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pg, ok := p.disk[id]
	return pg, ok
}

// HitRate returns hits / (hits + misses), or 0 with no accesses.
func (p *Pool) HitRate() float64 {
	h := p.hits.Value()
	m := p.misses.Value()
	if h+m == 0 {
		return 0
	}
	return float64(h) / float64(h+m)
}
