package bufpool

import (
	"fmt"
	"sync"
)

// LedgerEntry is the ledger's independent record of one physical buffer.
type LedgerEntry struct {
	ID         uint64
	Capacity   int // billed against the pool budget
	Length     int // tracked separately from capacity
	Refs       int
	Generation uint64
	Idle       bool
}

// Ledger independently tracks physical buffer identity, capacity billing and
// reference counts. It is updated by the pool but owns its own state, so
// tests can cross-check the pool against it.
type Ledger struct {
	mu       sync.Mutex
	entries  map[uint64]*LedgerEntry
	Allocs   int64 // physical buffers ever allocated
	Frees    int64 // physical buffers freed by Clear
	Borrows  int64 // total borrows (new + reused)
	Retains  int64
	Releases int64
}

// NewLedger creates an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{entries: make(map[uint64]*LedgerEntry)}
}

func (l *Ledger) noteAlloc(buf *buffer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[buf.id] = &LedgerEntry{
		ID:         buf.id,
		Capacity:   cap(buf.data),
		Length:     buf.length,
		Refs:       buf.refs,
		Generation: buf.generation,
	}
	l.Allocs++
	l.Borrows++
}

func (l *Ledger) noteBorrow(buf *buffer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[buf.id]
	e.Length = buf.length
	e.Refs = buf.refs
	e.Generation = buf.generation
	e.Idle = false
	l.Borrows++
}

func (l *Ledger) noteRetain(buf *buffer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[buf.id].Refs = buf.refs
	l.Retains++
}

func (l *Ledger) noteRelease(buf *buffer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[buf.id]
	e.Refs = buf.refs
	e.Idle = buf.refs == 0
	l.Releases++
}

func (l *Ledger) noteFree(buf *buffer) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, buf.id)
	l.Frees++
}

// LedgerSnapshot is a consistent copy of the ledger.
type LedgerSnapshot struct {
	Entries           map[uint64]LedgerEntry
	TotalCapacity     int64 // sum of entry capacities, billed bytes
	TotalLength       int64 // sum of entry lengths, separately billed
	Allocs            int64
	Frees             int64
	Borrows           int64
	Retains           int64
	Releases          int64
	LivePhysicalCount int
}

func (l *Ledger) snapshot() LedgerSnapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := LedgerSnapshot{
		Entries:  make(map[uint64]LedgerEntry, len(l.entries)),
		Allocs:   l.Allocs,
		Frees:    l.Frees,
		Borrows:  l.Borrows,
		Retains:  l.Retains,
		Releases: l.Releases,
	}
	for id, e := range l.entries {
		s.Entries[id] = *e
		s.TotalCapacity += int64(e.Capacity)
		s.TotalLength += int64(e.Length)
	}
	s.LivePhysicalCount = len(l.entries)
	return s
}

// Snapshot returns a consistent copy of the ledger for verification.
func (l *Ledger) Snapshot() LedgerSnapshot { return l.snapshot() }

// VerifyPool cross-checks the ledger against the pool: every physical buffer
// is billed exactly once, idle entries have zero refs, and the ledger's total
// capacity equals the pool's billed bytes.
func (l *Ledger) VerifyPool(p *Pool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := l.snapshot()
	if s.TotalCapacity != p.usedBytes {
		return fmt.Errorf("bufpool: ledger capacity %d != pool used %d (double billing?)",
			s.TotalCapacity, p.usedBytes)
	}
	idleSeen := make(map[uint64]bool, len(p.idle))
	for _, buf := range p.idle {
		if idleSeen[buf.id] {
			return fmt.Errorf("bufpool: buffer %d appears twice in idle list", buf.id)
		}
		idleSeen[buf.id] = true
		e, ok := s.Entries[buf.id]
		if !ok {
			return fmt.Errorf("bufpool: idle buffer %d missing from ledger", buf.id)
		}
		if e.Refs != 0 {
			return fmt.Errorf("bufpool: idle buffer %d has refs %d", buf.id, e.Refs)
		}
		if !e.Idle {
			return fmt.Errorf("bufpool: buffer %d idle in pool but not in ledger", buf.id)
		}
		if e.Capacity != cap(buf.data) {
			return fmt.Errorf("bufpool: buffer %d capacity mismatch ledger=%d actual=%d",
				buf.id, e.Capacity, cap(buf.data))
		}
	}
	for id, e := range s.Entries {
		if e.Idle && !idleSeen[id] {
			return fmt.Errorf("bufpool: buffer %d idle in ledger but not in pool", id)
		}
		if e.Refs < 0 {
			return fmt.Errorf("bufpool: buffer %d has negative refs %d", id, e.Refs)
		}
	}
	return nil
}
