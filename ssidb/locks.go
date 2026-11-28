package ssidb

import "hash/fnv"

// predicateLocks is the per-transaction SIREAD lock set. Locks never block
// anything: they are only consulted when another transaction writes.
type predicateLocks struct {
	table  bool
	pages  map[uint64]struct{}
	tuples map[string]struct{}
}

func newPredicateLocks() predicateLocks {
	return predicateLocks{
		pages:  map[uint64]struct{}{},
		tuples: map[string]struct{}{},
	}
}

// pageOf maps a key to its index leaf page (hash bucket). A scan locks every
// leaf page it visits, so a key inserted later onto a visited page -- including
// a brand-new phantom key -- is detected through the page lock.
func (e *Engine) pageOf(key string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return h.Sum64() % uint64(e.cfg.PagesPerTable)
}

// addTupleLock registers a tuple-level SIREAD lock and performs conservative
// tuple -> page escalation when the per-transaction tuple budget is exceeded.
func (e *Engine) addTupleLock(t *Txn, key string) {
	if t.locks.table {
		return
	}
	if _, ok := t.locks.tuples[key]; ok {
		return
	}
	t.locks.tuples[key] = struct{}{}
	readers := e.tupleReaders[key]
	if readers == nil {
		readers = map[*Txn]struct{}{}
		e.tupleReaders[key] = readers
	}
	readers[t] = struct{}{}
	if len(t.locks.tuples) > e.cfg.MaxTupleLocks {
		e.escalateTuples(t)
	}
}

// escalateTuples coalesces every tuple lock of t into page locks. Conflict
// coverage is preserved (a page lock is a superset of its tuple locks).
func (e *Engine) escalateTuples(t *Txn) {
	for key := range t.locks.tuples {
		e.addPageLock(t, e.pageOf(key))
		delete(e.tupleReaders[key], t)
		if len(e.tupleReaders[key]) == 0 {
			delete(e.tupleReaders, key)
		}
	}
	t.locks.tuples = map[string]struct{}{}
}

// addPageLock registers a page-level SIREAD lock and escalates to a table
// lock when the per-transaction page budget is exceeded.
func (e *Engine) addPageLock(t *Txn, page uint64) {
	if t.locks.table {
		return
	}
	if _, ok := t.locks.pages[page]; ok {
		return
	}
	t.locks.pages[page] = struct{}{}
	readers := e.pageReaders[page]
	if readers == nil {
		readers = map[*Txn]struct{}{}
		e.pageReaders[page] = readers
	}
	readers[t] = struct{}{}
	if len(t.locks.pages) > e.cfg.MaxPageLocks {
		e.escalatePages(t)
	}
}

// escalatePages replaces all page (and residual tuple) locks of t with one
// relation-level lock, which conservatively covers the whole key space.
func (e *Engine) escalatePages(t *Txn) {
	for page := range t.locks.pages {
		delete(e.pageReaders[page], t)
		if len(e.pageReaders[page]) == 0 {
			delete(e.pageReaders, page)
		}
	}
	t.locks.pages = map[uint64]struct{}{}
	e.addTableLock(t)
}

// addTableLock registers a relation-level SIREAD lock and drops every finer
// lock of t, since the table lock subsumes them.
func (e *Engine) addTableLock(t *Txn) {
	if t.locks.table {
		return
	}
	t.locks.table = true
	for page := range t.locks.pages {
		delete(e.pageReaders[page], t)
		if len(e.pageReaders[page]) == 0 {
			delete(e.pageReaders, page)
		}
	}
	for key := range t.locks.tuples {
		delete(e.tupleReaders[key], t)
		if len(e.tupleReaders[key]) == 0 {
			delete(e.tupleReaders, key)
		}
	}
	t.locks.pages = map[uint64]struct{}{}
	t.locks.tuples = map[string]struct{}{}
	e.tableReaders[t] = struct{}{}
}

// releaseLocks detaches t from every global SIREAD index. It is only called
// when the transaction is garbage collected, never at commit time.
func (e *Engine) releaseLocks(t *Txn) {
	if t.locks.table {
		delete(e.tableReaders, t)
	}
	for page := range t.locks.pages {
		delete(e.pageReaders[page], t)
		if len(e.pageReaders[page]) == 0 {
			delete(e.pageReaders, page)
		}
	}
	for key := range t.locks.tuples {
		delete(e.tupleReaders[key], t)
		if len(e.tupleReaders[key]) == 0 {
			delete(e.tupleReaders, key)
		}
	}
	t.locks = newPredicateLocks()
}

// readerConflict returns all transactions whose SIREAD locks cover key,
// excluding the writer itself. Table, page and tuple indexes are all consulted
// so that escalated locks keep full conflict coverage.
func (e *Engine) readerConflict(writer *Txn, key string) []*Txn {
	page := e.pageOf(key)
	seen := map[*Txn]struct{}{}
	var readers []*Txn
	for r := range e.tableReaders {
		if r != writer {
			seen[r] = struct{}{}
			readers = append(readers, r)
		}
	}
	for r := range e.pageReaders[page] {
		if r != writer {
			if _, ok := seen[r]; !ok {
				seen[r] = struct{}{}
				readers = append(readers, r)
			}
		}
	}
	for r := range e.tupleReaders[key] {
		if r != writer {
			if _, ok := seen[r]; !ok {
				seen[r] = struct{}{}
				readers = append(readers, r)
			}
		}
	}
	return readers
}
