package batchrun

import (
	"errors"
	"sync"
	"time"
)

var (
	// ErrUnknownLease is returned when a confirmation or release references
	// a lease that does not exist (e.g. reclaimed after expiry).
	ErrUnknownLease = errors.New("batchrun: unknown lease")
	// ErrLeaseClosed is returned when an operation targets a lease that was
	// already fully confirmed or released, and is not an idempotent replay.
	ErrLeaseClosed = errors.New("batchrun: lease already closed")
	// ErrNotHolder is returned when a worker operates on a lease held by
	// another worker.
	ErrNotHolder = errors.New("batchrun: lease held by another worker")
	// ErrOutOfBounds is returned when a confirmation falls outside the
	// lease's original range.
	ErrOutOfBounds = errors.New("batchrun: confirmation out of lease bounds")
)

// Lease grants a worker exclusive ownership of a range of work until its
// deadline. The Range shrinks as prefixes are confirmed; Bounds retains the
// originally dispatched range for validation.
type Lease struct {
	ID        uint64
	Worker    string
	Range     Range // unconfirmed remainder still owned by this lease
	Bounds    Range // original dispatched range, used for bounds checks
	ExpiresAt time.Time
	closed    bool
}

// Tracker splits a total workload [0, Total) into leased sub-ranges and
// tracks confirmed progress as an interval union. It is safe for
// concurrent use.
type Tracker struct {
	mu        sync.Mutex
	total     Range
	chunk     int64
	ttl       time.Duration
	confirmed intervalSet
	leases    map[uint64]*Lease
	nextID    uint64
}

// NewTracker returns a Tracker for total items, dispatched in chunks of at
// most chunkSize items per lease, with leases expiring after ttl unless
// confirmed or released.
func NewTracker(total, chunkSize int64, ttl time.Duration) *Tracker {
	if chunkSize <= 0 {
		panic("batchrun: chunkSize must be positive")
	}
	return &Tracker{
		total:  Range{0, total},
		chunk:  chunkSize,
		ttl:    ttl,
		leases: make(map[uint64]*Lease),
	}
}

// Acquire grants the worker a lease over the first unconfirmed, unleased
// gap, capped at the configured chunk size. It returns ok=false when no
// dispatchable work remains.
func (t *Tracker) Acquire(worker string, now time.Time) (Lease, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)

	avail := t.confirmed.gaps(t.total)
	for _, l := range t.leases {
		if l.closed {
			continue
		}
		var held intervalSet
		held.ivs = []Range{l.Range}
		avail = subtractAll(avail, held)
	}
	for _, g := range avail {
		if g.Len() <= 0 {
			continue
		}
		hi := g.Lo + t.chunk
		if hi > g.Hi {
			hi = g.Hi
		}
		t.nextID++
		l := &Lease{
			ID:        t.nextID,
			Worker:    worker,
			Range:     Range{g.Lo, hi},
			Bounds:    Range{g.Lo, hi},
			ExpiresAt: now.Add(t.ttl),
		}
		t.leases[l.ID] = l
		return *l, true
	}
	return Lease{}, false
}

// Confirm records that the holder of leaseID has completed the prefix
// [lease.Bounds.Lo, upto) of its lease. The unconfirmed tail stays leased
// until it is confirmed, released, or expires. Confirmations overlapping
// already-confirmed work are idempotent; a replay of a fully confirmed
// lease succeeds as a no-op. A confirmation outside the lease's original
// bounds is rejected with ErrOutOfBounds and changes nothing.
func (t *Tracker) Confirm(leaseID uint64, worker string, upto int64, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)

	l, ok := t.leases[leaseID]
	if !ok {
		return ErrUnknownLease
	}
	if l.Worker != worker {
		return ErrNotHolder
	}
	if upto < l.Bounds.Lo || upto > l.Bounds.Hi {
		return ErrOutOfBounds
	}
	if l.closed {
		// Idempotent replay of a confirmation on a finished lease.
		return nil
	}
	t.confirmed.add(Range{l.Range.Lo, upto})
	if upto >= l.Range.Hi {
		l.closed = true
		l.Range = Range{l.Range.Hi, l.Range.Hi}
	} else if upto > l.Range.Lo {
		l.Range = Range{upto, l.Range.Hi}
	}
	return nil
}

// Release returns the lease's unconfirmed remainder to the dispatchable
// pool, e.g. when a worker fails after confirming a prefix.
func (t *Tracker) Release(leaseID uint64, worker string, now time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.expireLocked(now)

	l, ok := t.leases[leaseID]
	if !ok {
		return ErrUnknownLease
	}
	if l.Worker != worker {
		return ErrNotHolder
	}
	if l.closed {
		return ErrLeaseClosed
	}
	l.closed = true
	l.Range = Range{l.Range.Hi, l.Range.Hi}
	return nil
}

// Expire reclaims leases whose deadline passed at or before now, returning
// the number reclaimed. Their unconfirmed remainders become dispatchable.
func (t *Tracker) Expire(now time.Time) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.expireLocked(now)
}

func (t *Tracker) expireLocked(now time.Time) int {
	n := 0
	for id, l := range t.leases {
		if !l.closed && !l.ExpiresAt.After(now) {
			delete(t.leases, id)
			n++
		}
	}
	return n
}

// Complete reports whether the confirmed union covers the whole workload.
func (t *Tracker) Complete() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.confirmed.covers(t.total)
}

// Progress returns the number of confirmed items and the total.
func (t *Tracker) Progress() (confirmed, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.confirmed.covered(), t.total.Len()
}

// Confirmed returns a snapshot of the confirmed interval union.
func (t *Tracker) Confirmed() []Range {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Range, len(t.confirmed.ivs))
	copy(out, t.confirmed.ivs)
	return out
}

// Outstanding returns a snapshot of the unconfirmed ranges still owned by
// live leases.
func (t *Tracker) Outstanding() []Range {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []Range
	for _, l := range t.leases {
		if !l.closed && l.Range.Len() > 0 {
			out = append(out, l.Range)
		}
	}
	return out
}

// subtractAll removes every interval in b from each range in rs.
func subtractAll(rs []Range, b intervalSet) []Range {
	var out []Range
	for _, r := range rs {
		cur := r.Lo
		for _, iv := range b.ivs {
			if iv.Hi <= cur {
				continue
			}
			if iv.Lo >= r.Hi {
				break
			}
			if iv.Lo > cur {
				out = append(out, Range{cur, iv.Lo})
			}
			if iv.Hi > cur {
				cur = iv.Hi
			}
		}
		if cur < r.Hi {
			out = append(out, Range{cur, r.Hi})
		}
	}
	return out
}
