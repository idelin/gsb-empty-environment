package writebacksim

import "sync"

// WriteRequest is a snapshot of one page version sent to the disk.
type WriteRequest struct {
	PageID  uint64
	Version uint64
	Data    []byte
}

// Completion is the disk's receipt for a WriteRequest. It acknowledges
// exactly one version of one page; Err is non-nil if the write failed.
type Completion struct {
	PageID  uint64
	Version uint64
	Err     error
}

type storedImage struct {
	version uint64
	data    []byte
}

// VersionedDisk is a simulated disk that stores versioned page images and
// delivers completion receipts asynchronously. A barrier (Hold) can queue
// receipts so tests control exactly when and in which order they arrive,
// and receipts can be injected, duplicated, or failed on demand.
type VersionedDisk struct {
	mu       sync.Mutex
	stored   map[uint64]storedImage
	held     bool
	queue    []Completion
	failNext []error
	failAll  error
	writes   int
	deliver  func(Completion)
}

func NewVersionedDisk() *VersionedDisk {
	return &VersionedDisk{stored: make(map[uint64]storedImage)}
}

// Write durably applies the image (unless a failure is injected) and emits
// a receipt, which is queued while the barrier is held.
func (d *VersionedDisk) Write(req WriteRequest) {
	d.mu.Lock()
	d.writes++
	var comp Completion
	switch {
	case d.failAll != nil:
		comp = Completion{PageID: req.PageID, Version: req.Version, Err: d.failAll}
	case len(d.failNext) > 0:
		comp = Completion{PageID: req.PageID, Version: req.Version, Err: d.failNext[0]}
		d.failNext = d.failNext[1:]
	default:
		if cur, ok := d.stored[req.PageID]; !ok || req.Version >= cur.version {
			d.stored[req.PageID] = storedImage{version: req.Version, data: append([]byte(nil), req.Data...)}
		}
		comp = Completion{PageID: req.PageID, Version: req.Version}
	}
	if d.held {
		d.queue = append(d.queue, comp)
		d.mu.Unlock()
		return
	}
	deliver := d.deliver
	d.mu.Unlock()
	if deliver != nil {
		deliver(comp)
	}
}

// Hold engages the barrier: subsequent receipts are queued instead of
// being delivered.
func (d *VersionedDisk) Hold() {
	d.mu.Lock()
	d.held = true
	d.mu.Unlock()
}

// Unhold disengages the barrier and flushes any queued receipts.
func (d *VersionedDisk) Unhold() {
	d.mu.Lock()
	d.held = false
	out := append([]Completion(nil), d.queue...)
	d.queue = nil
	deliver := d.deliver
	d.mu.Unlock()
	if deliver != nil {
		for _, comp := range out {
			deliver(comp)
		}
	}
}

// ReleaseMatching delivers every queued receipt for which match returns
// true (preserving order) and removes it from the queue.
func (d *VersionedDisk) ReleaseMatching(match func(Completion) bool) int {
	d.mu.Lock()
	var out, keep []Completion
	for _, comp := range d.queue {
		if match(comp) {
			out = append(out, comp)
		} else {
			keep = append(keep, comp)
		}
	}
	d.queue = keep
	deliver := d.deliver
	d.mu.Unlock()
	if deliver != nil {
		for _, comp := range out {
			deliver(comp)
		}
	}
	return len(out)
}

// ReleaseAll delivers all queued receipts.
func (d *VersionedDisk) ReleaseAll() int {
	return d.ReleaseMatching(func(Completion) bool { return true })
}

// Inject delivers an arbitrary receipt directly, bypassing the barrier.
// It simulates late, duplicated, or straggler receipts from a previous
// writeback round.
func (d *VersionedDisk) Inject(comp Completion) {
	d.mu.Lock()
	deliver := d.deliver
	d.mu.Unlock()
	if deliver != nil {
		deliver(comp)
	}
}

// FailNext makes the next n writes fail with err.
func (d *VersionedDisk) FailNext(err error, n int) {
	d.mu.Lock()
	for i := 0; i < n; i++ {
		d.failNext = append(d.failNext, err)
	}
	d.mu.Unlock()
}

// SetFailAll makes every write fail with err; a nil err clears the fault.
func (d *VersionedDisk) SetFailAll(err error) {
	d.mu.Lock()
	d.failAll = err
	d.mu.Unlock()
}

// Pending returns a snapshot of the receipts currently held at the barrier.
func (d *VersionedDisk) Pending() []Completion {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Completion(nil), d.queue...)
}

// Stored returns the version and data durably stored for a page.
func (d *VersionedDisk) Stored(pageID uint64) (uint64, []byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	img, ok := d.stored[pageID]
	if !ok {
		return 0, nil, false
	}
	return img.version, append([]byte(nil), img.data...), true
}

// Writes returns how many write requests the disk has received.
func (d *VersionedDisk) Writes() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writes
}
