package seq

import (
	"errors"
	"sync"
)

// ErrInvalidStep is returned by NewAllocator when step is zero.
var ErrInvalidStep = errors.New("seq: segment step must be > 0")

// Allocator hands out unique, monotonically increasing IDs from segments
// reserved in a Store. It is safe for concurrent use.
type Allocator struct {
	store Store
	step  uint64

	mu   sync.Mutex
	next uint64 // next ID to hand out
	end  uint64 // exclusive end of the currently reserved segment
}

// NewAllocator creates an Allocator that reserves segments of step IDs at a
// time in store.
func NewAllocator(store Store, step uint64) (*Allocator, error) {
	if step == 0 {
		return nil, ErrInvalidStep
	}
	return &Allocator{store: store, step: step}, nil
}

// Next returns the next unique ID. It reserves and persists a new segment
// when the current one is exhausted; if that flush fails, Next returns the
// error and no ID is issued.
func (a *Allocator) Next() (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.next >= a.end {
		if err := a.reserve(); err != nil {
			return 0, err
		}
	}
	id := a.next
	a.next++
	return id, nil
}

// reserve persists a new segment [oldEnd, oldEnd+step) via CAS and adopts it.
// The persisted high-water mark only ever moves forward, so after a restart
// the allocator resumes at or beyond the last persisted SegmentEnd: the
// unused tail of the crashed segment is skipped, never reused.
func (a *Allocator) reserve() error {
	for {
		cur, err := a.store.Load()
		if err != nil {
			return err
		}
		next := State{Version: cur.Version + 1, SegmentEnd: cur.SegmentEnd + a.step}
		ok, err := a.store.CompareAndSwap(cur, next)
		if err != nil {
			// Flush failed: do not issue IDs from an unreserved range.
			return err
		}
		if !ok {
			// Lost a CAS race against another process; reload and retry.
			continue
		}
		a.next = cur.SegmentEnd
		a.end = next.SegmentEnd
		return nil
	}
}

// Segment reports the in-memory segment bounds: the next ID to be issued and
// the exclusive segment end. Intended for tests and diagnostics.
func (a *Allocator) Segment() (next, end uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next, a.end
}
