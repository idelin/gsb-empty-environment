package writeback

import (
	"errors"
	"sync"
)

// ErrDiskClosed is delivered to writes submitted after the disk is closed.
var ErrDiskClosed = errors.New("disk: closed")

// storedPage is the durable image of a page: a version plus its contents.
type storedPage struct {
	version uint64
	data    []byte
}

// pendingWrite is a write that has been submitted but not yet completed.
type pendingWrite struct {
	pageID  uint64
	version uint64
	data    []byte
	done    chan error
}

// pendingEntry is either a queued write or a barrier marker.
type pendingEntry struct {
	write   *pendingWrite
	barrier chan struct{} // non-nil for barrier entries
}

// SimDisk is a versioned disk model.
//
// Writes carry the page version they were made from. A write is only applied
// if its version is newer than the version already durable, so a stale write
// that loses the race can never clobber newer data. Completions are driven
// manually by the test (CompleteNext / FailNext), which makes reordering,
// failure and shutdown scenarios deterministic.
type SimDisk struct {
	mu      sync.Mutex
	stored  map[uint64]storedPage
	pending []pendingEntry
	closed  bool
}

func NewSimDisk() *SimDisk {
	return &SimDisk{stored: make(map[uint64]storedPage)}
}

// Submit enqueues a versioned write. The result is delivered on the returned
// channel exactly once (nil on success).
func (d *SimDisk) Submit(pageID, version uint64, data []byte) <-chan error {
	d.mu.Lock()
	defer d.mu.Unlock()
	w := &pendingWrite{
		pageID:  pageID,
		version: version,
		data:    append([]byte(nil), data...),
		done:    make(chan error, 1),
	}
	if d.closed {
		w.done <- ErrDiskClosed
		close(w.done)
		return w.done
	}
	d.pending = append(d.pending, pendingEntry{write: w})
	return w.done
}

// Barrier returns a channel that is closed once every write submitted before
// this call has completed (successfully or not).
func (d *SimDisk) Barrier() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	ch := make(chan struct{})
	if d.closed {
		close(ch)
		return ch
	}
	d.pending = append(d.pending, pendingEntry{barrier: ch})
	return ch
}

// PendingWrites reports how many writes are queued but not yet completed.
func (d *SimDisk) PendingWrites() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, e := range d.pending {
		if e.write != nil {
			n++
		}
	}
	return n
}

// CompleteNext persists the oldest queued write and reports success.
// Barriers it passes are released. Returns false if no write is queued.
func (d *SimDisk) CompleteNext() bool {
	w := d.shift()
	if w == nil {
		return false
	}
	d.mu.Lock()
	d.applyLocked(w)
	d.releaseBarriersLocked()
	d.mu.Unlock()
	w.done <- nil
	close(w.done)
	return true
}

// FailNext drops the oldest queued write and reports err to its waiter.
// Nothing is persisted. Barriers it passes are released.
func (d *SimDisk) FailNext(err error) bool {
	w := d.shift()
	if w == nil {
		return false
	}
	d.mu.Lock()
	d.releaseBarriersLocked()
	d.mu.Unlock()
	w.done <- err
	close(w.done)
	return true
}

// releaseBarriersLocked releases barriers that have reached the head of the
// queue, i.e. every write submitted before them has completed.
func (d *SimDisk) releaseBarriersLocked() {
	for len(d.pending) > 0 && d.pending[0].write == nil {
		close(d.pending[0].barrier)
		d.pending = d.pending[1:]
	}
}

// shift releases any barriers at the head of the queue, then pops the first
// queued write. Barriers only observe writes submitted before them, so a
// barrier is released as soon as every entry ahead of it is gone.
func (d *SimDisk) shift() *pendingWrite {
	d.mu.Lock()
	defer d.mu.Unlock()
	for len(d.pending) > 0 && d.pending[0].write == nil {
		close(d.pending[0].barrier)
		d.pending = d.pending[1:]
	}
	if len(d.pending) == 0 {
		return nil
	}
	w := d.pending[0].write
	d.pending = d.pending[1:]
	return w
}

// applyLocked makes a write durable unless a newer version already is.
func (d *SimDisk) applyLocked(w *pendingWrite) {
	cur, ok := d.stored[w.pageID]
	if ok && cur.version >= w.version {
		return
	}
	d.stored[w.pageID] = storedPage{version: w.version, data: w.data}
}

// Close performs a graceful shutdown: every queued write is completed in
// order and every queued barrier is released. Subsequent Submit calls fail
// with ErrDiskClosed.
func (d *SimDisk) Close() {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}
	d.closed = true
	pending := d.pending
	d.pending = nil
	d.mu.Unlock()
	for _, e := range pending {
		if e.write == nil {
			close(e.barrier)
			continue
		}
		d.mu.Lock()
		d.applyLocked(e.write)
		d.mu.Unlock()
		e.write.done <- nil
		close(e.write.done)
	}
}

// Read returns the durable image of a page.
func (d *SimDisk) Read(pageID uint64) (data []byte, version uint64, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.stored[pageID]
	if !ok {
		return nil, 0, false
	}
	return append([]byte(nil), p.data...), p.version, true
}
