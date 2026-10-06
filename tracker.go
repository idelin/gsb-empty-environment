package batch

import (
	"errors"
	"fmt"
	"sync"
)

// Sentinel errors returned by Tracker operations. Go 1.11 has no
// errors.Is, so compare with ==.
var (
	// ErrOutOfBounds is returned when a confirmation falls outside the
	// lease's range (and therefore possibly outside [0, total)).
	ErrOutOfBounds = errors.New("batch: confirmation out of bounds")
	// ErrUnknownLease is returned when the lease ID does not exist.
	ErrUnknownLease = errors.New("batch: unknown lease")
	// ErrStaleLease is returned when a confirmation arrives for a lease
	// that is no longer active (failed, revoked, or already finished).
	// This is how receipts from a superseded lease generation are
	// rejected after lease turnover.
	ErrStaleLease = errors.New("batch: stale lease")
)

type leaseState int

const (
	leaseActive leaseState = iota
	leaseFailed
	leaseDone
)

// Lease is an exclusive grant to work on Range. It is identified by a
// monotonically increasing ID; a new generation is issued on every
// re-dispatch, so receipts carrying an old ID are rejected as stale.
type Lease struct {
	ID    uint64
	Range Interval
	state leaseState
}

// Tracker coordinates batch execution over [0, total). It is safe for
// concurrent use.
type Tracker struct {
	total int64

	mu        sync.Mutex
	confirmed intervalSet // union of all confirmed work
	leases    map[uint64]*Lease
	nextID    uint64
}

// NewTracker creates a Tracker for total work items. total must be > 0.
func NewTracker(total int64) (*Tracker, error) {
	if total <= 0 {
		return nil, fmt.Errorf("batch: total must be positive, got %d", total)
	}
	return &Tracker{total: total, leases: make(map[uint64]*Lease)}, nil
}

// Total returns the total number of work items.
func (t *Tracker) Total() int64 { return t.total }

// Dispatch hands out leases covering currently unconfirmed and unleased
// work, splitting it into chunks of at most chunkSize. It returns at most
// maxLeases leases (maxLeases <= 0 means no limit). Because only gaps in
// the confirmed union that are not already under an active lease are
// handed out, re-dispatch after failures contains exactly the unconfirmed
// remainder.
func (t *Tracker) Dispatch(chunkSize, maxLeases int64) []*Lease {
	if chunkSize <= 0 {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// Dispatchable = unconfirmed gaps minus actively leased ranges.
	gaps := t.confirmed.gaps(t.total)
	leased := intervalSet{}
	for _, l := range t.leases {
		if l.state == leaseActive {
			leased.add(l.Range)
		}
	}
	var free []Interval
	for _, g := range gaps {
		free = append(free, leased.subtract(g.Start, g.End)...)
	}

	var out []*Lease
	for _, f := range free {
		for start := f.Start; start < f.End; start += chunkSize {
			if maxLeases > 0 && int64(len(out)) >= maxLeases {
				return out
			}
			end := start + chunkSize
			if end > f.End {
				end = f.End
			}
			t.nextID++
			l := &Lease{ID: t.nextID, Range: Interval{Start: start, End: end}}
			t.leases[l.ID] = l
			out = append(out, l)
		}
	}
	return out
}

// Confirm records sub-interval iv of the given lease as completed. The
// interval must lie within the lease's range; otherwise ErrOutOfBounds is
// returned. Confirmations on a failed lease are rejected with
// ErrStaleLease (or ErrUnknownLease for a never-issued ID). Overlapping
// or duplicate confirmations are idempotent, including receipts for a
// lease whose range is already fully confirmed.
func (t *Tracker) Confirm(leaseID uint64, iv Interval) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.leases[leaseID]
	if !ok {
		return ErrUnknownLease
	}
	if l.state == leaseFailed {
		return ErrStaleLease
	}
	if iv.Start < l.Range.Start || iv.End > l.Range.End || iv.Start >= iv.End {
		return ErrOutOfBounds
	}
	if l.state == leaseDone {
		return nil // duplicate receipt for a finished lease: no-op
	}
	t.confirmed.add(iv)
	if t.confirmed.contains(l.Range.Start, l.Range.End) {
		l.state = leaseDone
	}
	return nil
}

// ConfirmPrefix confirms the completed prefix [lease.Start, upto) of the
// lease — the common "did some work, then failed" receipt. upto may equal
// the lease start (nothing done) or end (lease fully done).
func (t *Tracker) ConfirmPrefix(leaseID uint64, upto int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.leases[leaseID]
	if !ok {
		return ErrUnknownLease
	}
	if l.state == leaseFailed {
		return ErrStaleLease
	}
	if upto < l.Range.Start || upto > l.Range.End {
		return ErrOutOfBounds
	}
	if l.state == leaseDone {
		return nil // duplicate receipt for a finished lease: no-op
	}
	if upto > l.Range.Start {
		t.confirmed.add(Interval{Start: l.Range.Start, End: upto})
	}
	if upto == l.Range.End {
		l.state = leaseDone
	}
	return nil
}

// Fail marks the lease as failed. Its unconfirmed remainder becomes
// eligible for re-dispatch under a new lease ID; late receipts carrying
// the old ID are rejected as stale.
func (t *Tracker) Fail(leaseID uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.leases[leaseID]
	if !ok {
		return ErrUnknownLease
	}
	if l.state == leaseActive {
		l.state = leaseFailed
	}
	return nil
}

// Unconfirmed returns the gaps of [0, total) not yet confirmed, in
// ascending order.
func (t *Tracker) Unconfirmed() []Interval {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.confirmed.gaps(t.total)
}

// Confirmed returns the merged union of confirmed intervals.
func (t *Tracker) Confirmed() []Interval {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]Interval, len(t.confirmed.ivs))
	copy(out, t.confirmed.ivs)
	return out
}

// ConfirmedCount returns how many items have been confirmed.
func (t *Tracker) ConfirmedCount() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.confirmed.sum()
}

// Complete reports whether the confirmed union covers all of [0, total).
func (t *Tracker) Complete() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.confirmed.covers(t.total)
}
