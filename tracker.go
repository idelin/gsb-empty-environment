package netsec

import (
	"errors"
	"sync"
)

var (
	// ErrRevoked is returned by Submit after the mapping has been revoked.
	ErrRevoked = errors.New("netsec: mapping revoked, no new submissions")
	// ErrStaleGeneration is returned when a submission carries a mapping
	// generation that no longer matches the tracker.
	ErrStaleGeneration = errors.New("netsec: stale mapping generation")
	// ErrUnknownOp is returned when completing an operation that was never
	// submitted (or was already retired).
	ErrUnknownOp = errors.New("netsec: unknown operation")
	// ErrAlreadyComplete is returned when an operation is completed twice;
	// the duplicate never releases page references again.
	ErrAlreadyComplete = errors.New("netsec: operation already completed")
	// ErrInFlight is returned by Reset while DMA operations are still
	// outstanding.
	ErrInFlight = errors.New("netsec: DMA operations still in flight")
	// ErrQueueNotStopped is returned by Reset when a valid stop proof is
	// missing for at least one attached queue.
	ErrQueueNotStopped = errors.New("netsec: stop proof missing or invalid for a queue")
)

type operation struct {
	gen   Generation
	pages []PageID
}

// Tracker owns the DMA mapping lifetime for one virtual device. Pages bound
// by Submit stay pinned until the corresponding operation is confirmed
// complete exactly once; revocation blocks new submissions while existing
// operations drain.
type Tracker struct {
	mu      sync.Mutex
	pool    *Pool
	gen     Generation
	revoked bool
	nextOp  OpID
	ops     map[OpID]*operation
	done    map[OpID]struct{}
	queues  map[*Queue]struct{}
}

// NewTracker creates a tracker releasing page references into pool.
func NewTracker(pool *Pool) *Tracker {
	return &Tracker{
		pool:   pool,
		gen:    1,
		ops:    make(map[OpID]*operation),
		done:   make(map[OpID]struct{}),
		queues: make(map[*Queue]struct{}),
	}
}

// Generation reports the current mapping generation.
func (t *Tracker) Generation() Generation {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.gen
}

// AttachQueue registers a DMA queue whose stop proof is required for Reset.
func (t *Tracker) AttachQueue(q *Queue) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.queues[q] = struct{}{}
}

// Submit binds a page set to a new DMA operation under mapping generation
// gen, pinning every page until the operation completes. It fails after
// revocation, on a stale generation, or on an unknown page (pinning is
// rolled back in that case).
func (t *Tracker) Submit(gen Generation, pages ...PageID) (OpID, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.revoked {
		return 0, ErrRevoked
	}
	if gen != t.gen {
		return 0, ErrStaleGeneration
	}
	for i, id := range pages {
		if err := t.pool.pin(id); err != nil {
			for _, rollback := range pages[:i] {
				t.pool.unpin(rollback)
			}
			return 0, err
		}
	}
	t.nextOp++
	id := t.nextOp
	t.ops[id] = &operation{gen: gen, pages: append([]PageID(nil), pages...)}
	return id, nil
}

// Complete confirms that the device finished an operation and releases its
// page references. Completion is exactly-once: a duplicate returns
// ErrAlreadyComplete and never releases a second time.
func (t *Tracker) Complete(id OpID) error {
	t.mu.Lock()
	if _, ok := t.done[id]; ok {
		t.mu.Unlock()
		return ErrAlreadyComplete
	}
	op, ok := t.ops[id]
	if !ok {
		t.mu.Unlock()
		return ErrUnknownOp
	}
	delete(t.ops, id)
	t.done[id] = struct{}{}
	pages := op.pages
	t.mu.Unlock()
	for _, pid := range pages {
		t.pool.unpin(pid)
	}
	return nil
}

// Revoke stops new submissions. In-flight operations remain pinned and must
// still be confirmed complete before their pages return to the pool.
func (t *Tracker) Revoke() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revoked = true
}

// InFlight reports the number of operations awaiting completion.
func (t *Tracker) InFlight() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.ops)
}

// Reset performs a device reset. It requires all in-flight operations to be
// drained and a valid stop proof for every attached queue. On success the
// mapping generation advances and submissions reopen under the new
// generation; on failure nothing changes and all pages stay pinned.
func (t *Tracker) Reset(proofs ...StopProof) error {
	t.mu.Lock()
	queues := make([]*Queue, 0, len(t.queues))
	for q := range t.queues {
		queues = append(queues, q)
	}
	t.mu.Unlock()

	byQueue := make(map[*Queue]StopProof, len(proofs))
	for _, p := range proofs {
		if p.queue != nil {
			byQueue[p.queue] = p
		}
	}
	for _, q := range queues {
		proof, ok := byQueue[q]
		if !ok || !proof.valid() {
			return ErrQueueNotStopped
		}
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.ops) > 0 {
		return ErrInFlight
	}
	t.gen++
	t.revoked = false
	return nil
}
