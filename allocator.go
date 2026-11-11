package noncelease

import (
	"sync"
	"time"
)

// Clock is the time source used for lease expiry. time.Now is used by default;
// tests inject a controllable clock.
type Clock func() time.Time

// Option configures an Allocator.
type Option func(*Allocator)

// WithClock overrides the wall clock, mainly for deterministic tests.
func WithClock(c Clock) Option {
	return func(a *Allocator) { a.now = c }
}

type leaseEntry struct {
	token     uint64
	key       KeyID
	version   uint32
	expiresAt int64 // 0 == never expires
}

type keyState struct {
	version uint32
	// limit is the exclusive upper bound of the key version's counter space.
	// Zero means the full uint64 space.
	limit uint64
	// high is the first counter never allocated: all counters below it are
	// covered by some (possibly expired) lease and must never be reissued.
	high uint64
	// active tracks leases issued during this process lifetime whose TTL has
	// not elapsed. It is empty after a restart because leases never persist:
	// the durable high-water mark then keeps every old counter reserved.
	active []activeRange
}

// activeRange marks [start, end) as reserved until expiresAt (0 == forever).
type activeRange struct {
	start     uint64
	end       uint64
	expiresAt int64
}

// Allocator grants disjoint counter leases out of a durable counter store.
// All methods are safe for concurrent use.
type Allocator struct {
	mu    sync.Mutex
	store *Store
	now   Clock

	keys   map[KeyID]*keyState
	leases map[uint64]leaseEntry
	closed bool
}

// Open opens (or creates) the durable store at path and returns an allocator
// backed by it. All previously persisted allocations are replayed before the
// call returns; leases are never persisted and start empty.
func Open(path string, opts ...Option) (*Allocator, error) {
	store, err := OpenFileStore(path)
	if err != nil {
		return nil, err
	}
	return newAllocator(store, opts...)
}

// OpenWithStore wraps an existing store (used with the injectable test
// storage); production callers should use Open.
func OpenWithStore(store *Store, opts ...Option) (*Allocator, error) {
	return newAllocator(store, opts...)
}

func newAllocator(store *Store, opts ...Option) (*Allocator, error) {
	a := &Allocator{
		store:  store,
		now:    time.Now,
		keys:   make(map[KeyID]*keyState),
		leases: make(map[uint64]leaseEntry),
	}
	for _, opt := range opts {
		opt(a)
	}
	for _, rec := range store.Records() {
		a.apply(rec)
	}
	return a, nil
}

// apply mutates in-memory state to reflect one replayed record.
func (a *Allocator) apply(rec record) {
	switch rec.typ {
	case recCreateKey:
		a.keys[rec.key] = &keyState{version: rec.version, limit: rec.limit, high: 0}
	case recRotateKey:
		if st, ok := a.keys[rec.key]; ok {
			st.version = rec.version
			st.high = 0
			st.active = nil
		}
	case recAllocate:
		st := a.keys[rec.key]
		if st == nil {
			return
		}
		// Only records of the current version move the watermark; older
		// versions are sealed. Leases are deliberately not rebuilt on replay.
		if rec.version == st.version && rec.end > st.high {
			st.high = rec.end
		}
	}
}

// CreateKey registers a new key whose counter space starts at zero. limit is
// the exclusive counter upper bound for the key; a limit of zero means the
// full uint64 space. It is idempotent: creating an existing key returns its
// current version without changing the limit.
func (a *Allocator) CreateKey(key KeyID, limits ...uint64) (uint32, error) {
	if key == "" {
		return 0, ErrInvalidArgument
	}
	var limit uint64
	switch len(limits) {
	case 0:
	case 1:
		limit = limits[0]
	default:
		return 0, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return 0, ErrStoreClosed
	}
	if st, ok := a.keys[key]; ok {
		return st.version, nil
	}
	if err := a.store.append(record{
		typ: recCreateKey, key: key, version: 1, limit: limit,
	}); err != nil {
		return 0, err
	}
	a.keys[key] = &keyState{version: 1, limit: limit}
	return 1, nil
}

// RotateKey seals the current version of key and starts a new version whose
// counters begin again at zero. The returned version is the new current
// version. Outstanding leases of the old version stay valid until they expire
// but no further segments are allocated for the old version.
func (a *Allocator) RotateKey(key KeyID) (uint32, error) {
	if key == "" {
		return 0, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return 0, ErrStoreClosed
	}
	st, ok := a.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	next := st.version + 1
	if next == 0 {
		return 0, ErrCounterExhausted
	}
	if err := a.store.append(record{typ: recRotateKey, key: key, version: next}); err != nil {
		return 0, err
	}
	// Persistence succeeded first; only now mutate in-memory state.
	st.version = next
	st.high = 0
	st.active = nil
	return next, nil
}

// Allocate reserves a segment of count counters for key and returns the lease
// covering it. The reservation is fsynced before the lease is returned, so a
// later crash cannot cause those counters to be handed out again.
//
// ttl of zero means the lease never expires; otherwise it becomes invalid
// after ttl and its tail segment may be recycled. Allocation is
// all-or-nothing: if fewer than count counters remain, ErrCounterExhausted is
// returned without consuming any counters.
func (a *Allocator) Allocate(key KeyID, count uint64, ttl time.Duration) (Lease, error) {
	if key == "" || count == 0 {
		return Lease{}, ErrInvalidArgument
	}
	if ttl < 0 {
		return Lease{}, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return Lease{}, ErrStoreClosed
	}
	st, ok := a.keys[key]
	if !ok {
		return Lease{}, ErrKeyNotFound
	}

	a.purgeExpired(st)

	start := st.high
	// Reject overflow and the key-specific limit before touching durable
	// state, keeping allocation all-or-nothing.
	capacity := ^uint64(0)
	if st.limit != 0 {
		capacity = st.limit
	}
	if start >= capacity || count > capacity-start {
		return Lease{}, ErrCounterExhausted
	}
	end := start + count

	var expiresAt int64
	if ttl > 0 {
		expiresAt = a.now().Add(ttl).UnixNano()
	}
	rec := record{
		typ:       recAllocate,
		key:       key,
		version:   st.version,
		start:     start,
		end:       end,
		expiresAt: expiresAt,
	}
	if err := a.store.append(rec); err != nil {
		return Lease{}, err
	}

	st.high = end
	st.active = append(st.active, activeRange{
		start: start, end: end, expiresAt: expiresAt,
	})

	token, err := randomToken()
	if err != nil {
		return Lease{}, err
	}
	for {
		if _, exists := a.leases[token]; !exists {
			break
		}
		token, err = randomToken()
		if err != nil {
			return Lease{}, err
		}
	}
	a.leases[token] = leaseEntry{
		token: token, key: key, version: st.version, expiresAt: expiresAt,
	}

	return Lease{
		Interval: Interval{
			Start: start, End: end, Key: key, Version: st.version,
		},
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

// purgeExpired drops leases whose TTL has elapsed. Counters are recycled only
// when every tracked lease of the version is gone: the high-water mark then
// returns to zero. A single live or permanent (ttl == 0) lease keeps the whole
// watermark in place, so no counter of a valid lease is ever reissued. After a
// restart the active list is empty by construction and the mark is left
// untouched at its durable value.
func (a *Allocator) purgeExpired(st *keyState) {
	if len(st.active) == 0 {
		return
	}
	now := a.now().UnixNano()
	alive := st.active[:0]
	for _, r := range st.active {
		if r.expiresAt == 0 || r.expiresAt > now {
			alive = append(alive, r)
		}
	}
	if len(alive) == 0 {
		st.high = 0
	}
	st.active = alive
}

// Valid reports whether lease may still be used. It fails for leases issued
// by a previous allocator instance (restart fencing), unknown tokens, expired
// TTLs and rotations that sealed the lease's version.
func (a *Allocator) Valid(lease Lease) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrStoreClosed
	}
	entry, ok := a.leases[lease.Token]
	if !ok {
		return ErrLeaseInvalid
	}
	if entry.key != lease.Interval.Key || entry.version != lease.Interval.Version {
		return ErrLeaseInvalid
	}
	if entry.expiresAt != 0 && entry.expiresAt <= a.now().UnixNano() {
		return ErrLeaseInvalid
	}
	st, ok := a.keys[entry.key]
	if !ok || st.version != entry.version {
		return ErrKeySealed
	}
	return nil
}

// Version returns the current version of key.
func (a *Allocator) Version(key KeyID) (uint32, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	return st.version, nil
}

// HighWater returns the first counter never allocated for the current version
// of key. It is mainly useful for tests and observability.
func (a *Allocator) HighWater(key KeyID) (uint64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.keys[key]
	if !ok {
		return 0, ErrKeyNotFound
	}
	a.purgeExpired(st)
	return st.high, nil
}

// Status is a point-in-time snapshot of one key version.
type Status struct {
	Key          KeyID
	Version      uint32
	HighWater    uint64
	ActiveLeases int
}

// Status returns a snapshot for key.
func (a *Allocator) Status(key KeyID) (Status, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.keys[key]
	if !ok {
		return Status{}, ErrKeyNotFound
	}
	a.purgeExpired(st)
	return Status{
		Key:          key,
		Version:      st.version,
		HighWater:    st.high,
		ActiveLeases: len(st.active),
	}, nil
}

// Close flushes and closes the durable store. It is idempotent.
func (a *Allocator) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	return a.store.Close()
}
