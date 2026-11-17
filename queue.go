package netsec

import (
	"errors"
	"sync"
)

var (
	// ErrQueueStopped reports that the DMA queue is halted or not accepting new
	// submissions.
	ErrQueueStopped = errors.New("netsec: DMA queue is not accepting submissions")

	// ErrQueueNotStopped reports that a stop proof was requested before the
	// entire queue had stopped and fenced all accepted operations.
	ErrQueueNotStopped = errors.New("netsec: DMA queue has not stopped")

	// ErrNilMapping reports a nil mapping handle.
	ErrNilMapping = errors.New("netsec: nil mapping")
)

type queueEntry struct {
	mapping   *Mapping
	op        *Operation
	completed bool
	fenced    bool
}

// DMAQueue is a deterministic simulation of a device DMA queue. It records
// accepted operations, routes completion acknowledgments to the mapping, and
// can be halted to simulate a device stop.
//
// A StopProof can only be minted by a halted queue. Once minted, it covers every
// operation the queue accepted for the mapping: completed operations are already
// safe, and remaining operations are fenced by the halt. The proof becomes
// stale when the queue restarts.
type DMAQueue struct {
	mu sync.Mutex

	epoch     uint64
	haltSeq   uint64
	accepting bool
	halted    bool

	entries     map[*Operation]*queueEntry
	outstanding map[*Operation]struct{}
	accepted    map[*Mapping]uint64
	safe        map[*Mapping]uint64
}

// NewDMAQueue creates an accepting queue.
func NewDMAQueue() *DMAQueue {
	q := &DMAQueue{}
	q.initLocked()
	return q
}

func (q *DMAQueue) initLocked() {
	if q.epoch == 0 {
		q.epoch = 1
		q.accepting = true
	}
	if q.entries == nil {
		q.entries = make(map[*Operation]*queueEntry)
		q.outstanding = make(map[*Operation]struct{})
		q.accepted = make(map[*Mapping]uint64)
		q.safe = make(map[*Mapping]uint64)
	}
}

// Submit accepts one operation against m and returns its completion capability.
// Submission fails if the queue is halted or if the mapping has been revoked.
func (q *DMAQueue) Submit(m *Mapping) (*Operation, error) {
	if q == nil {
		return nil, ErrQueueStopped
	}
	if m == nil {
		return nil, ErrNilMapping
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()

	if !q.accepting || q.halted {
		return nil, ErrQueueStopped
	}

	op, err := m.Submit()
	if err != nil {
		return nil, err
	}

	q.entries[op] = &queueEntry{mapping: m, op: op}
	q.outstanding[op] = struct{}{}
	q.accepted[m]++
	return op, nil
}

// Complete routes an acknowledgment to the mapping and records the operation as
// safe. Duplicate acknowledgments are idempotent and never decrement the
// mapping's outstanding count twice.
func (q *DMAQueue) Complete(op *Operation) error {
	if q == nil {
		return ErrUnknownOperation
	}
	if op == nil {
		return ErrNilOperation
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()

	entry, ok := q.entries[op]
	if !ok {
		return ErrUnknownOperation
	}
	if entry.completed {
		return nil
	}
	if err := entry.mapping.Complete(op); err != nil {
		return err
	}

	entry.completed = true
	delete(q.outstanding, op)
	if !entry.fenced {
		q.safe[entry.mapping]++
	}
	return nil
}

// Halt stops the queue from accepting submissions and fences every outstanding
// operation. A subsequent StopProof proves that no accepted operation can still
// access the mapping's pages.
//
// Halt is idempotent until the queue is restarted.
func (q *DMAQueue) Halt() error {
	if q == nil {
		return ErrQueueStopped
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()

	if q.halted {
		return nil
	}

	q.accepting = false
	q.halted = true
	q.epoch++
	q.haltSeq++
	for op := range q.outstanding {
		entry := q.entries[op]
		if entry.completed || entry.fenced {
			continue
		}
		entry.fenced = true
		q.safe[entry.mapping]++
		delete(q.outstanding, op)
	}
	return nil
}

// Restart starts a new queue epoch. Any StopProof minted before the restart is
// stale and will be rejected.
func (q *DMAQueue) Restart() error {
	if q == nil {
		return ErrQueueStopped
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()

	q.epoch++
	q.haltSeq = 0
	q.halted = false
	q.accepting = true
	q.entries = make(map[*Operation]*queueEntry)
	q.outstanding = make(map[*Operation]struct{})
	q.accepted = make(map[*Mapping]uint64)
	q.safe = make(map[*Mapping]uint64)
	return nil
}

// StopProof returns an opaque proof that the whole queue is stopped and that
// every operation accepted for m has either completed or been fenced.
//
// The proof also covers operations that were not acknowledged before the halt;
// the halt is what makes them safe. It does not cover operations submitted
// directly to a mapping without going through this queue.
func (q *DMAQueue) StopProof(m *Mapping) (StopProof, error) {
	if q == nil {
		return StopProof{}, ErrQueueNotStopped
	}
	if m == nil {
		return StopProof{}, ErrNilMapping
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	q.initLocked()

	if !q.halted {
		return StopProof{}, ErrQueueNotStopped
	}
	accepted := q.accepted[m]
	safe := q.safe[m]
	if safe < accepted {
		return StopProof{}, ErrQueueNotStopped
	}

	return StopProof{
		queue:      q,
		mapping:    m,
		generation: m.generation,
		epoch:      q.epoch,
		haltSeq:    q.haltSeq,
		accepted:   accepted,
		fenced:     safe,
	}, nil
}

// StopProof is an opaque capability proving that a DMA queue stopped before a
// mapping was reset. Its fields are intentionally unexported so callers cannot
// manufacture a proof.
type StopProof struct {
	queue      *DMAQueue
	mapping    *Mapping
	generation Generation
	epoch      uint64
	haltSeq    uint64
	accepted   uint64
	fenced     uint64
}

func (p StopProof) validFor(m *Mapping, accepted uint64) bool {
	if p.queue == nil || p.mapping != m || p.generation != m.generation {
		return false
	}
	if p.accepted < accepted || p.fenced < p.accepted {
		return false
	}

	p.queue.mu.Lock()
	defer p.queue.mu.Unlock()
	p.queue.initLocked()

	if !p.queue.halted || p.queue.epoch != p.epoch || p.queue.haltSeq != p.haltSeq {
		return false
	}
	if p.queue.accepted[m] != p.accepted {
		return false
	}
	return p.queue.safe[m] >= p.accepted
}
