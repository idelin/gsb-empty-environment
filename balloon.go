package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

const (
	// BlockSize is the size of a single Balloon hash block in bytes.
	BlockSize = 32
	// MinDelta is the minimum allowed number of dependency layers.
	MinDelta = 3
	// MinTCost is the minimum allowed number of time-cost rounds.
	MinTCost = 1
	// MaxRequestBytes caps the memory a single hash request may use (64 MiB).
	MaxRequestBytes = 64 << 20
)

var (
	ErrSCostInvalid   = errors.New("netsec: s_cost must be positive")
	ErrDeltaTooSmall  = errors.New("netsec: delta weakens dependency layers")
	ErrTCostTooSmall  = errors.New("netsec: t_cost must be >= 1")
	ErrMemoryLimit    = errors.New("netsec: request exceeds 64 MiB memory limit")
	ErrQuotaExhausted = errors.New("netsec: tenant memory quota exhausted")
	ErrBufferLength   = errors.New("netsec: hash buffer length mismatch")
)

// Params describes a single Balloon hash request.
type Params struct {
	SCost int // number of 32-byte blocks held in memory
	TCost int // time-cost rounds
	Delta int // dependency layers
}

// MemoryBytes returns the buffer size the request needs: s_cost * BlockSize.
func (p Params) MemoryBytes() int64 {
	if p.SCost <= 0 {
		return 0
	}
	return int64(p.SCost) * BlockSize
}

// Validate checks the parameters against the package limits.
func (p Params) Validate() error {
	if p.SCost <= 0 {
		return fmt.Errorf("%w: got %d", ErrSCostInvalid, p.SCost)
	}
	if p.Delta < MinDelta {
		return fmt.Errorf("%w: delta=%d, need >= %d", ErrDeltaTooSmall, p.Delta, MinDelta)
	}
	if p.TCost < MinTCost {
		return fmt.Errorf("%w: t_cost=%d", ErrTCostTooSmall, p.TCost)
	}
	if mem := p.MemoryBytes(); mem > MaxRequestBytes {
		return fmt.Errorf("%w: %d bytes requested", ErrMemoryLimit, mem)
	}
	return nil
}

// Limiter enforces a shared memory quota across tenants. Reservations
// always happen before any buffer is allocated.
type Limiter struct {
	mu         sync.Mutex
	capacity   int64
	used       int64
	tenantUsed map[string]int64

	// alloc allocates the hash buffer; nil means make([]byte, n).
	// Overridable in tests to inject a mis-sized buffer.
	alloc func(n int) []byte
}

// NewLimiter returns a Limiter allowing capacityBytes of concurrent usage.
func NewLimiter(capacityBytes int64) *Limiter {
	return &Limiter{capacity: capacityBytes, tenantUsed: make(map[string]int64)}
}

// Used reports the currently reserved bytes across all tenants.
func (l *Limiter) Used() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

// TenantUsed reports the currently reserved bytes for one tenant.
func (l *Limiter) TenantUsed(tenant string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tenantUsed[tenant]
}

// Reservation is a held quota grant. Release is idempotent.
type Reservation struct {
	lim    *Limiter
	tenant string
	bytes  int64
	once   sync.Once
}

// Reserve takes bytes out of the shared quota for tenant.
func (l *Limiter) Reserve(tenant string, bytes int64) (*Reservation, error) {
	if bytes <= 0 {
		return nil, fmt.Errorf("%w: reservation of %d bytes", ErrSCostInvalid, bytes)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used+bytes > l.capacity {
		return nil, fmt.Errorf("%w: tenant %q wants %d bytes, %d of %d in use",
			ErrQuotaExhausted, tenant, bytes, l.used, l.capacity)
	}
	l.used += bytes
	l.tenantUsed[tenant] += bytes
	return &Reservation{lim: l, tenant: tenant, bytes: bytes}, nil
}

// Release returns the reserved bytes to the quota. Safe to call twice.
func (r *Reservation) Release() {
	r.once.Do(func() {
		r.lim.mu.Lock()
		r.lim.used -= r.bytes
		r.lim.tenantUsed[r.tenant] -= r.bytes
		r.lim.mu.Unlock()
	})
}

// HashFunc computes the hash into buf, which is exactly s_cost*BlockSize bytes.
type HashFunc func(buf []byte) error

// Hash validates p, reserves quota for tenant, allocates the buffer, and
// invokes fn. The reservation is released on completion, panic, or context
// cancellation/timeout. A buffer whose length is not exactly s_cost*BlockSize
// is treated as a failure and the reservation is released.
func (l *Limiter) Hash(ctx context.Context, tenant string, p Params, fn HashFunc) (err error) {
	if err := p.Validate(); err != nil {
		return err
	}
	res, err := l.Reserve(tenant, p.MemoryBytes())
	if err != nil {
		return err
	}
	defer res.Release()

	if err := ctx.Err(); err != nil {
		return err
	}

	want := p.SCost * BlockSize
	alloc := l.alloc
	if alloc == nil {
		alloc = func(n int) []byte { return make([]byte, n) }
	}
	buf := alloc(want)
	if len(buf) != want {
		return fmt.Errorf("%w: want %d bytes, got %d", ErrBufferLength, want, len(buf))
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("netsec: hash panicked: %v", r)
		}
	}()
	if err := fn(buf); err != nil {
		return err
	}
	return ctx.Err()
}
