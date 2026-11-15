package netsec

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Store owns tenant dictionaries. It never shares dictionary bytes across
// tenant IDs or labels, and its global mutex only protects the tenant map and
// epoch allocation. Compression and decompression of stream bodies run
// without the global lock.
type Store struct {
	mu        sync.RWMutex
	tenants   map[string]*tenant
	nextEpoch uint64
}

type tenant struct {
	id       string
	mu       sync.RWMutex
	epoch    uint64
	parts    [2]*partition
	released bool
}

type partition struct {
	tenant string
	label  Label
	epoch  uint64

	mu      sync.RWMutex
	data    []byte
	revoked bool
}

// Service is an alias for Store.
type Service = Store

// NewStore creates an empty dictionary store.
func NewStore() *Store {
	return &Store{tenants: make(map[string]*tenant)}
}

// New is an alias for NewStore.
func New() *Store { return NewStore() }

// NewService is an alias for NewStore.
func NewService() *Store { return NewStore() }

// RegisterTenant explicitly creates a tenant. OpenStream also lazily creates
// tenants, so registration is optional.
func (s *Store) RegisterTenant(id string) error {
	if err := validateTenant(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants == nil {
		s.tenants = make(map[string]*tenant)
	}
	if _, ok := s.tenants[id]; ok {
		return ErrTenantExists
	}
	s.tenants[id] = s.newTenantLocked(id)
	return nil
}

func (s *Store) newTenantLocked(id string) *tenant {
	s.nextEpoch++
	epoch := s.nextEpoch
	t := &tenant{id: id, epoch: epoch}
	for i := range t.parts {
		t.parts[i] = &partition{
			tenant: id,
			label:  Label(i),
			epoch:  epoch,
		}
	}
	return t
}

func (s *Store) lookupOrCreate(id string) (*tenant, error) {
	if err := validateTenant(id); err != nil {
		return nil, err
	}
	s.mu.RLock()
	t := s.tenants[id]
	s.mu.RUnlock()
	if t != nil {
		return t, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tenants == nil {
		s.tenants = make(map[string]*tenant)
	}
	if t = s.tenants[id]; t != nil {
		return t, nil
	}
	t = s.newTenantLocked(id)
	s.tenants[id] = t
	return t, nil
}

// ResetTenant retains the tenant registration but atomically replaces both
// label partitions with empty ones. In-flight streams opened against the old
// epoch can no longer finish, and old dictionary versions can no longer be
// decoded.
func (s *Store) ResetTenant(id string) error {
	if err := validateTenant(id); err != nil {
		return err
	}

	s.mu.Lock()
	t := s.tenants[id]
	if t == nil {
		s.mu.Unlock()
		return ErrUnknownTenant
	}
	s.nextEpoch++
	epoch := s.nextEpoch

	t.mu.Lock()
	if t.released {
		t.mu.Unlock()
		s.mu.Unlock()
		return ErrUnknownTenant
	}
	old := t.parts
	t.epoch = epoch
	for i := range t.parts {
		t.parts[i] = &partition{
			tenant: id,
			label:  Label(i),
			epoch:  epoch,
		}
	}
	t.mu.Unlock()
	s.mu.Unlock()

	revoke(old)
	return nil
}

// ReleaseTenant removes a tenant and invalidates all of its streams and
// dictionary versions. Registering or opening the same tenant ID afterwards
// allocates a fresh epoch, so the old dictionary cannot be reused.
func (s *Store) ReleaseTenant(id string) error {
	if err := validateTenant(id); err != nil {
		return err
	}

	s.mu.Lock()
	t := s.tenants[id]
	if t == nil {
		s.mu.Unlock()
		return ErrUnknownTenant
	}
	delete(s.tenants, id)

	t.mu.Lock()
	t.released = true
	old := t.parts
	t.epoch++
	t.mu.Unlock()
	s.mu.Unlock()

	revoke(old)
	return nil
}

// RemoveTenant is an alias for ReleaseTenant.
func (s *Store) RemoveTenant(id string) error { return s.ReleaseTenant(id) }

func revoke(parts [2]*partition) {
	for _, p := range parts {
		if p == nil {
			continue
		}
		p.mu.Lock()
		p.revoked = true
		p.data = nil
		p.mu.Unlock()
	}
}

// TenantEpoch reports the current epoch for a registered tenant.
func (s *Store) TenantEpoch(id string) (uint64, bool) {
	if validateTenant(id) != nil {
		return 0, false
	}
	s.mu.RLock()
	t := s.tenants[id]
	s.mu.RUnlock()
	if t == nil {
		return 0, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.released {
		return 0, false
	}
	return t.epoch, true
}

// OpenStream snapshots the tenant/label dictionary and returns a buffered
// compression stream. The snapshot is fixed at this point: commits that
// happen later are not visible to this stream. Call Finish to compress and
// atomically publish the plaintext, or Cancel to discard it.
func (s *Store) OpenStream(ctx context.Context, tenantID string, label Label) (*Stream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateTenant(tenantID); err != nil {
		return nil, err
	}
	if err := validateLabel(label); err != nil {
		return nil, err
	}

	t, err := s.lookupOrCreate(tenantID)
	if err != nil {
		return nil, err
	}

	t.mu.RLock()
	if t.released {
		t.mu.RUnlock()
		return nil, ErrUnknownTenant
	}
	epoch := t.epoch
	part := t.parts[label]
	t.mu.RUnlock()

	part.mu.RLock()
	if part.revoked {
		part.mu.RUnlock()
		return nil, ErrStaleDictionary
	}
	offset := uint64(len(part.data))
	start := 0
	if len(part.data) > WindowSize {
		start = len(part.data) - WindowSize
	}
	dict := part.data[start:]
	part.mu.RUnlock()

	return &Stream{
		part: part,
		ctx:  ctx,
		base: Version{
			Tenant: tenantID,
			Label:  label,
			Epoch:  epoch,
			Offset: offset,
		},
		dict: dict,
	}, nil
}

// Compress opens a stream, writes src, and finishes it. The returned Version
// is the dictionary snapshot against which Data must be decoded.
func (s *Store) Compress(ctx context.Context, tenantID string, label Label, src []byte) (Compressed, error) {
	st, err := s.OpenStream(ctx, tenantID, label)
	if err != nil {
		return Compressed{}, err
	}
	if _, err := st.Write(src); err != nil {
		st.Cancel()
		return Compressed{}, err
	}
	return st.Finish()
}

// Decompress decodes encoded using the exact dictionary version recorded when
// the stream was compressed. A reset, release, or tenant-ID reuse makes the
// version stale and returns ErrStaleDictionary.
func (s *Store) Decompress(ctx context.Context, tenantID string, label Label, version Version, encoded []byte) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateTenant(tenantID); err != nil {
		return nil, err
	}
	if err := validateLabel(label); err != nil {
		return nil, err
	}
	if version.Tenant != tenantID || version.Label != label {
		return nil, ErrVersionMismatch
	}

	s.mu.RLock()
	t := s.tenants[tenantID]
	s.mu.RUnlock()
	if t == nil {
		return nil, ErrStaleDictionary
	}

	t.mu.RLock()
	if t.released || t.epoch != version.Epoch {
		t.mu.RUnlock()
		return nil, ErrStaleDictionary
	}
	part := t.parts[label]
	t.mu.RUnlock()

	// Hold the partition read lock through decoding so ResetTenant and
	// ReleaseTenant cannot interleave with the use of an old dictionary.
	part.mu.RLock()
	defer part.mu.RUnlock()
	if part.revoked || part.epoch != version.Epoch {
		return nil, ErrStaleDictionary
	}
	if version.Offset > uint64(len(part.data)) {
		return nil, ErrStaleDictionary
	}
	dict := part.data[:int(version.Offset)]
	return decompress(ctx, dict, encoded, nil)
}

// Stream is a buffered compression stream. Writes only append to its private
// buffer; compression happens in Finish. A Cancel from another goroutine is
// observed by an in-progress Finish.
type Stream struct {
	part *partition
	ctx  context.Context
	base Version
	dict []byte

	mu       sync.Mutex
	buf      []byte
	state    streamState
	err      error
	result   Compressed
	canceled atomic.Bool
}

type streamState uint8

const (
	streamOpen streamState = iota
	streamFinishing
	streamDone
	streamCanceled
	streamFailed
)

// Version reports the dictionary snapshot this stream will use.
func (st *Stream) Version() Version { return st.base }

// Write appends p to the stream's private plaintext buffer.
func (st *Stream) Write(p []byte) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()

	if st.state != streamOpen {
		return 0, ErrStreamClosed
	}
	if st.canceled.Load() {
		st.cancelStreamLocked()
		return 0, ErrStreamCanceled
	}
	if err := st.ctx.Err(); err != nil {
		st.cancelStreamLocked()
		return 0, err
	}
	st.buf = append(st.buf, p...)
	return len(p), nil
}

// Cancel abandons the stream. It never publishes dictionary data, even if a
// Finish is already compressing.
func (st *Stream) Cancel() {
	st.mu.Lock()
	st.canceled.Store(true)
	if st.state == streamOpen {
		st.cancelStreamLocked()
	}
	st.mu.Unlock()
}

// Reset discards the stream. It is an alias for Cancel.
func (st *Stream) Reset() { st.Cancel() }

func (st *Stream) cancelStreamLocked() {
	st.state = streamCanceled
	st.buf = nil
	st.dict = nil
	st.result = Compressed{}
	st.err = ErrStreamCanceled
}

// Finish compresses the buffered plaintext and, if the dictionary epoch is
// still current, atomically commits the plaintext to the partition. The commit
// linearizes at the partition-lock append: it is visible to streams that take
// a snapshot after that point, but not to this stream or to streams that
// already took their snapshot.
func (st *Stream) Finish() (Compressed, error) {
	st.mu.Lock()
	switch st.state {
	case streamDone, streamCanceled, streamFailed:
		res, err := st.result, st.err
		st.mu.Unlock()
		return res, err
	case streamFinishing:
		st.mu.Unlock()
		return Compressed{}, ErrStreamClosed
	}

	if st.canceled.Load() {
		st.cancelStreamLocked()
		res, err := st.result, st.err
		st.mu.Unlock()
		return res, err
	}
	if err := st.ctx.Err(); err != nil {
		st.state = streamFailed
		st.buf = nil
		st.dict = nil
		st.err = err
		st.result = Compressed{}
		st.mu.Unlock()
		return Compressed{}, err
	}

	input := st.buf
	dict := st.dict
	st.buf = nil
	st.dict = nil
	st.state = streamFinishing
	st.mu.Unlock()

	abort := func() error {
		if st.canceled.Load() {
			return ErrStreamCanceled
		}
		st.part.mu.RLock()
		stale := st.part.revoked || st.part.epoch != st.base.Epoch
		st.part.mu.RUnlock()
		if stale {
			return ErrStaleDictionary
		}
		return nil
	}

	encoded, err := compress(st.ctx, dict, input, abort)
	if err != nil {
		st.mu.Lock()
		st.state = streamFailed
		st.buf = nil
		st.err = err
		st.result = Compressed{}
		st.mu.Unlock()
		return Compressed{}, err
	}
	// The commit is short and per-partition. Compression itself never holds
	// this lock, so streams in the same partition still compress in parallel.
	// Hold the stream mutex across the cancel check and commit so Cancel has a
	// single linearization point: it either wins and publishes nothing, or the
	// commit happened first.
	st.mu.Lock()
	if st.canceled.Load() {
		st.cancelStreamLocked()
		res, cerr := st.result, st.err
		st.mu.Unlock()
		return res, cerr
	}
	if err := st.ctx.Err(); err != nil {
		st.state = streamFailed
		st.buf = nil
		st.err = err
		st.result = Compressed{}
		st.mu.Unlock()
		return Compressed{}, err
	}

	st.part.mu.Lock()
	if st.part.revoked || st.part.epoch != st.base.Epoch {
		st.part.mu.Unlock()
		st.state = streamFailed
		st.buf = nil
		st.err = fmt.Errorf("%w: tenant reset or released during stream", ErrStaleDictionary)
		st.result = Compressed{}
		err := st.err
		st.mu.Unlock()
		return Compressed{}, err
	}
	st.part.data = append(st.part.data, input...)
	st.part.mu.Unlock()

	res := Compressed{Data: encoded, Version: st.base}
	st.state = streamDone
	st.result = res
	st.err = nil
	st.buf = nil
	st.mu.Unlock()
	return res, nil
}

// Commit is an alias for Finish.
func (st *Stream) Commit() (Compressed, error) { return st.Finish() }
