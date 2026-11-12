package netsec

import "sync"

// Quota is a concurrency-safe repository object-byte budget shared across
// concurrent pushes. A single Quota is typically created per repository and
// handed to every pre-receive invocation; it is safe for simultaneous use by
// multiple goroutines.
//
// The reservation model mirrors a two-phase commit: the full byte cost of a
// push is reserved atomically before admission succeeds, and the reservation
// is either Committed once the objects are durably stored or Released if the
// push is rejected or fails. Rejected pushes therefore never permanently
// consume capacity.
type Quota struct {
	mu      sync.Mutex
	limit   int64
	used    int64
	nextID  int64
	granted map[int64]int64
}

// NewQuota creates a Quota with limit object bytes. A non-positive limit
// rejects every push that introduces at least one byte.
func NewQuota(limit int64) *Quota {
	return &Quota{
		limit:   limit,
		granted: make(map[int64]int64),
	}
}

// Limit returns the configured total byte budget.
func (q *Quota) Limit() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.limit
}

// Used returns the bytes currently held by committed and outstanding grants.
func (q *Quota) Used() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used
}

// Available returns the bytes that could still be reserved.
func (q *Quota) Available() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.limit - q.used
}

// reserve atomically claims bytes for one push. It either succeeds entirely,
// returning a Grant that owns the reservation, or fails without changing any
// state. Concurrent pushes therefore cannot oversubscribe the quota by racing
// on the available-bytes check.
func (q *Quota) reserve(bytes int64) (*Grant, error) {
	if bytes < 0 {
		return nil, admissionError("netsec: negative reservation size")
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.used+bytes > q.limit {
		return nil, ErrQuotaExhausted
	}

	q.nextID++
	id := q.nextID
	q.granted[id] = bytes
	q.used += bytes

	return &Grant{quota: q, id: id, bytes: bytes}, nil
}

// release returns a grant's bytes. It is idempotent: a grant that was already
// released or committed has no effect.
func (q *Quota) release(id int64) int64 {
	q.mu.Lock()
	defer q.mu.Unlock()

	bytes, ok := q.granted[id]
	if !ok {
		return 0
	}
	delete(q.granted, id)
	q.used -= bytes
	return bytes
}

// Grant represents an atomic quota reservation owned by one admitted push.
// The hook must call Release if the push does not complete, or Commit once the
// objects are stored. A zero-value Grant is usable and performs no work, which
// lets callers safely defer Release even on failed admission.
type Grant struct {
	quota *Quota
	id    int64
	bytes int64
}

// Bytes returns the number of bytes held by the grant.
func (g *Grant) Bytes() int64 {
	return g.bytes
}

// Release returns the reserved bytes to the quota. It is safe to call more
// than once and after Commit; subsequent calls are no-ops.
func (g *Grant) Release() int64 {
	if g == nil || g.quota == nil {
		return 0
	}
	released := g.quota.release(g.id)
	g.quota = nil
	g.bytes = 0
	return released
}

// Commit finalizes the reservation: the bytes remain counted as used and the
// grant can no longer be released. It is idempotent.
func (g *Grant) Commit() {
	if g == nil || g.quota == nil {
		return
	}
	g.quota.mu.Lock()
	delete(g.quota.granted, g.id)
	g.quota.mu.Unlock()
	g.quota = nil
}
