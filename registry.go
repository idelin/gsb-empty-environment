package strdict

import "sync"

// Registry publishes frozen dictionary generations and hands out read
// leases. A generation can be reclaimed only while no lease on it is
// active.
type Registry struct {
	mu   sync.Mutex
	gens map[uint32]*generation
}

type generation struct {
	dict   *Dictionary
	leases int
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{gens: make(map[uint32]*generation)}
}

// Publish registers a frozen dictionary under its version.
func (r *Registry) Publish(d *Dictionary) error {
	if !d.Frozen() {
		return ErrNotFrozen
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.gens[d.Version()]; ok {
		return ErrVersionExists
	}
	r.gens[d.Version()] = &generation{dict: d}
	return nil
}

// Acquire takes a read lease on a published generation. The lease pins the
// generation against reclamation until Release.
func (r *Registry) Acquire(version uint32) (*Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gens[version]
	if !ok {
		return nil, ErrUnknownVersion
	}
	g.leases++
	return &Lease{reg: r, gen: g}, nil
}

// Reclaim removes a generation so it can no longer be acquired or decoded
// against. It fails with ErrLeaseActive while any read lease is held.
func (r *Registry) Reclaim(version uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gens[version]
	if !ok {
		return ErrUnknownVersion
	}
	if g.leases > 0 {
		return ErrLeaseActive
	}
	delete(r.gens, version)
	return nil
}

// Lease is an active read hold on one dictionary generation.
type Lease struct {
	reg      *Registry
	gen      *generation
	mu       sync.Mutex
	released bool
}

// Dictionary returns the leased dictionary. It stays valid for the lifetime
// of the lease.
func (l *Lease) Dictionary() *Dictionary { return l.gen.dict }

// Released reports whether the lease was released (directly or by batch
// cancellation).
func (l *Lease) Released() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.released
}

// Release drops the lease. It is idempotent.
func (l *Lease) Release() {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	l.mu.Unlock()
	l.reg.mu.Lock()
	l.gen.leases--
	l.reg.mu.Unlock()
}

// Batch groups leases so a whole unit of work can be cancelled at once.
type Batch struct {
	reg       *Registry
	mu        sync.Mutex
	leases    []*Lease
	cancelled bool
}

// BeginBatch starts a new batch bound to the registry.
func (r *Registry) BeginBatch() *Batch { return &Batch{reg: r} }

// Acquire takes a lease inside the batch. It fails with ErrCancelled after
// Cancel.
func (b *Batch) Acquire(version uint32) (*Lease, error) {
	b.mu.Lock()
	if b.cancelled {
		b.mu.Unlock()
		return nil, ErrCancelled
	}
	b.mu.Unlock()
	l, err := b.reg.Acquire(version)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	if b.cancelled {
		b.mu.Unlock()
		l.Release()
		return nil, ErrCancelled
	}
	b.leases = append(b.leases, l)
	b.mu.Unlock()
	return l, nil
}

// Cancel releases every lease taken by the batch. It is idempotent.
func (b *Batch) Cancel() {
	b.mu.Lock()
	if b.cancelled {
		b.mu.Unlock()
		return
	}
	b.cancelled = true
	leases := b.leases
	b.leases = nil
	b.mu.Unlock()
	for _, l := range leases {
		l.Release()
	}
}

// Cancelled reports whether Cancel was called.
func (b *Batch) Cancelled() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancelled
}
