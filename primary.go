// Package seqstore is a query library for reading multiple tables that
// replicate a single unified commit log. Every transaction commits with
// one global sequence number, even when it writes several tables, so a
// reader can always tell whether a table replica has caught up far
// enough to serve a consistent cross-table result.
package seqstore

import "sync"

// LogEntry is one committed transaction in the unified commit log.
// Writes to every table share the same Seq, which is what makes
// cross-table consistency checkable by readers.
type LogEntry struct {
	Seq    uint64
	Writes map[string]map[string]int64 // table -> key -> value
}

// Primary is the source of truth: an ordered, unified commit log.
type Primary struct {
	mu  sync.Mutex
	log []LogEntry
}

func NewPrimary() *Primary { return &Primary{} }

// Commit assigns the next unified sequence number and appends the
// transaction's cross-table writes as one atomic log entry.
func (p *Primary) Commit(writes map[string]map[string]int64) LogEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := LogEntry{Seq: uint64(len(p.log)) + 1, Writes: writes}
	p.log = append(p.log, e)
	return e
}

// Entries returns a copy of the committed log.
func (p *Primary) Entries() []LogEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]LogEntry, len(p.log))
	copy(out, p.log)
	return out
}

// Head returns the latest committed sequence number.
func (p *Primary) Head() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return uint64(len(p.log))
}
