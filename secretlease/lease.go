package secretlease

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrLeaseReleased is returned by Use after the lease has been released.
	ErrLeaseReleased = errors.New("secretlease: lease has been released")
	// ErrNotAuthorized is returned when the calling step has no grant for the
	// lease. Derived steps are not authorized merely because their parent is.
	ErrNotAuthorized = errors.New("secretlease: step is not authorized for this lease")
)

// Lease guards one secret. The secret bytes live in a [MemPool] chunk and are
// only exposed inside callbacks passed to [Lease.Use], and only to steps that
// have been explicitly granted.
type Lease struct {
	name string

	mu     sync.Mutex
	secret *Buffer
	grants map[uint64]struct{}
	closed bool

	inFlight sync.WaitGroup
	done     chan struct{}
}

// NewLease copies secret into the pool and returns a lease with no grants.
// The caller remains responsible for zeroing its own copy of the secret.
func NewLease(name string, secret []byte, pool *MemPool) (*Lease, error) {
	buf, err := pool.Alloc(len(secret))
	if err != nil {
		return nil, err
	}
	copy(buf.Bytes(), secret)
	return &Lease{
		name:   name,
		secret: buf,
		grants: make(map[uint64]struct{}),
		done:   make(chan struct{}),
	}, nil
}

// Name returns the lease name.
func (l *Lease) Name() string { return l.name }

// Grant authorizes the step with the given ID to call [Lease.Use].
// Authorization is never inherited by derived steps.
func (l *Lease) Grant(stepID uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.grants[stepID] = struct{}{}
}

// Revoke removes a step's authorization. In-flight callbacks are allowed to
// finish; new calls from the step are rejected.
func (l *Lease) Revoke(stepID uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.grants, stepID)
}

// Authorized reports whether the step currently holds a grant.
func (l *Lease) Authorized(stepID uint64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.grants[stepID]
	return ok
}

// Use invokes fn with the secret bytes. The secret is only valid for the
// duration of fn; retaining the slice beyond fn is a caller error. Use fails
// with [ErrNotAuthorized] if the step has no grant, [ErrLeaseReleased] if the
// lease was released, and the context error if the step context is canceled.
// A panicking fn is recovered into an error so cleanup accounting stays
// consistent.
func (l *Lease) Use(ctx StepContext, fn func(secret []byte) error) (err error) {
	if ctx == nil || fn == nil {
		return ErrNotAuthorized
	}

	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return ErrLeaseReleased
	}
	if _, ok := l.grants[ctx.StepID()]; !ok {
		l.mu.Unlock()
		return ErrNotAuthorized
	}
	secret := l.secret
	l.inFlight.Add(1)
	l.mu.Unlock()
	defer l.inFlight.Done()

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("secretlease: lease %q callback panicked: %v", l.name, r)
		}
	}()

	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(secret.Bytes())
}

// Release waits for every in-flight callback to finish, then zeroes the secret
// chunk. It is safe to call more than once.
func (l *Lease) Release() {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return
	}
	l.closed = true
	secret := l.secret
	l.secret = nil
	l.mu.Unlock()

	l.inFlight.Wait()
	if secret != nil {
		secret.Destroy()
	}
	close(l.done)
}

// Done is closed once Release has finished zeroing the secret.
func (l *Lease) Done() <-chan struct{} { return l.done }

var _ context.Context = StepContext(nil)
