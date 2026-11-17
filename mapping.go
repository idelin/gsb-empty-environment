package netsec

import (
	"errors"
	"sync"
)

var (
	// ErrMappingRevoked reports that a mapping has been revoked and no longer
	// accepts submissions.
	ErrMappingRevoked = errors.New("netsec: mapping has been revoked")

	// ErrUnknownOperation reports a completion for an operation that was not
	// submitted through this mapping.
	ErrUnknownOperation = errors.New("netsec: unknown DMA operation")

	// ErrNilOperation reports a nil operation handle.
	ErrNilOperation = errors.New("netsec: nil DMA operation")

	// ErrInvalidStopProof reports that a reset was attempted without a current,
	// full-queue stop proof covering the mapping.
	ErrInvalidStopProof = errors.New("netsec: invalid or incomplete queue stop proof")
)

// Generation identifies an incarnation of a mapping. Operations carry the
// generation they were submitted under so completions from an older mapping
// incarnation cannot be credited to a newer one.
type Generation uint64

// OperationID identifies a submission within a mapping.
type OperationID uint64

// Operation is the capability returned by a successful submission. It must be
// presented to complete the corresponding DMA operation. The fields are opaque
// so that callers cannot create or retarget an operation token.
type Operation struct {
	mapping    *Mapping
	id         OperationID
	generation Generation
}

// ID returns the operation's identifier within its mapping.
func (o *Operation) ID() OperationID {
	if o == nil {
		return 0
	}
	return o.id
}

// Generation returns the mapping generation under which the operation was
// submitted.
func (o *Operation) Generation() Generation {
	if o == nil {
		return 0
	}
	return o.generation
}

type operationRecord struct {
	completed bool
}

// Mapping pins a set of pages for the lifetime of a device mapping. It accepts
// DMA submissions until revoked. Pages are returned to their pool only after
// every accepted operation has been acknowledged, or after a reset supplies a
// valid proof that the whole DMA queue stopped and fenced all accesses.
type Mapping struct {
	pool       *PagePool
	generation Generation
	pages      []*Page

	mu          sync.Mutex
	nextID      OperationID
	records     map[OperationID]*operationRecord
	accepted    uint64
	outstanding int
	revoked     bool
	reset       bool
	released    bool
}

// NewMapping binds pages to a mapping generation. All pages must be leased from
// pool, must not already be bound, and the set must not contain duplicates.
//
// On success the mapping owns the page references. On failure no page is bound.
func NewMapping(pool *PagePool, generation Generation, pages ...*Page) (*Mapping, error) {
	if pool == nil {
		return nil, errors.New("netsec: nil page pool")
	}
	owned := append([]*Page(nil), pages...)
	if err := pool.bind(owned); err != nil {
		return nil, err
	}
	return &Mapping{
		pool:       pool,
		generation: generation,
		pages:      owned,
		records:    make(map[OperationID]*operationRecord),
	}, nil
}

// Generation returns the mapping generation.
func (m *Mapping) Generation() Generation {
	if m == nil {
		return 0
	}
	return m.generation
}

// PageIDs returns the identifiers of the pages held by the mapping.
func (m *Mapping) PageIDs() []PageID {
	if m == nil {
		return nil
	}
	ids := make([]PageID, len(m.pages))
	for i, page := range m.pages {
		ids[i] = page.id
	}
	return ids
}

// Submit reserves one operation against the mapping. Once the mapping is
// revoked, Submit fails and no new operation can be added.
func (m *Mapping) Submit() (*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.revoked {
		return nil, ErrMappingRevoked
	}

	m.nextID++
	op := &Operation{
		mapping:    m,
		id:         m.nextID,
		generation: m.generation,
	}
	m.records[op.id] = &operationRecord{}
	m.accepted++
	m.outstanding++
	return op, nil
}

// Complete acknowledges an operation. Completion is idempotent: a duplicate
// acknowledgment for the same operation is accepted without decrementing the
// outstanding count a second time.
//
// If this was the final outstanding operation and the mapping has been revoked,
// the mapping releases its page references exactly once.
func (m *Mapping) Complete(op *Operation) error {
	if op == nil {
		return ErrNilOperation
	}
	if op.mapping != m || op.generation != m.generation {
		return ErrUnknownOperation
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.records[op.id]
	if !ok {
		return ErrUnknownOperation
	}
	if record.completed {
		return nil
	}

	record.completed = true
	if m.outstanding > 0 {
		m.outstanding--
	}
	if m.revoked && m.outstanding == 0 {
		m.releaseLocked()
	}
	return nil
}

// Revoke prevents any further submissions. It is safe to call more than once.
// Existing accepted operations continue to count as in-flight until they are
// completed. If none are outstanding, revocation releases the pages
// immediately.
func (m *Mapping) Revoke() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.revoked = true
	if m.outstanding == 0 {
		m.releaseLocked()
	}
}

// Reset revokes the mapping and releases its pages only if proof demonstrates
// that the entire DMA queue stopped and every accepted access is fenced or
// complete.
//
// A failed reset leaves the mapping revoked and pinned. It never partially
// releases pages.
func (m *Mapping) Reset(proof StopProof) error {
	m.mu.Lock()
	if m.reset {
		m.mu.Unlock()
		return nil
	}
	m.revoked = true
	accepted := m.accepted
	m.mu.Unlock()

	if !proof.validFor(m, accepted) {
		return ErrInvalidStopProof
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reset {
		return nil
	}

	// A valid stop proof fences every accepted operation in the queue. Mark
	// them complete so any later completion report is an idempotent no-op.
	for _, record := range m.records {
		record.completed = true
	}
	m.outstanding = 0
	m.reset = true
	m.releaseLocked()
	return nil
}

func (m *Mapping) releaseLocked() {
	if m.released {
		return
	}
	m.released = true
	m.pool.release(m.pages)
}

// Revoked reports whether the mapping stopped accepting submissions.
func (m *Mapping) Revoked() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revoked
}

// Released reports whether the mapping returned its pages to the pool.
func (m *Mapping) Released() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.released
}

// Pending returns the number of accepted operations that have not been
// acknowledged and were not fenced by a successful reset.
func (m *Mapping) Pending() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.outstanding
}

// Accepted returns the total number of operations accepted by the mapping.
func (m *Mapping) Accepted() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accepted
}
