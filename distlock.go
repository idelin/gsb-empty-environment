// Package distlock implements a distributed mutual-exclusion lock with
// lease-based deadlock protection, automatic keep-alive heartbeats and
// fencing tokens for stale-writer rejection.
package distlock

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	// ErrLockLost is returned when an operation is attempted on a lease
	// that has expired or been released.
	ErrLockLost = errors.New("distlock: lease lost")
	// ErrInvalidTTL is returned for non-positive lease TTLs.
	ErrInvalidTTL = errors.New("distlock: ttl must be positive")
)

// Option configures a Server.
type Option func(*Server)

// WithSweepInterval sets how often the server scans for expired leases.
// Lower values release crashed holders faster but cost more CPU.
func WithSweepInterval(d time.Duration) Option {
	return func(s *Server) { s.sweepInterval = d }
}

// Server is a lock service. In a real deployment it would be backed by a
// replicated state machine; here it is an in-process reference
// implementation with identical semantics.
type Server struct {
	mu            sync.Mutex
	locks         map[string]*lockState
	nextLeaseID   uint64
	fenceCounter  uint64
	sweepInterval time.Duration
	stopCh        chan struct{}
	wg            sync.WaitGroup
	closed        bool
}

type lockState struct {
	owner   *lease
	waiters []*waiter
}

type lease struct {
	id       uint64
	token    uint64
	ttl      time.Duration
	deadline time.Time
}

type waiter struct {
	ttl time.Duration
	ch  chan *lease // buffered (cap 1), receives the granted lease
}

// NewServer creates a lock server and starts its lease-expiry sweeper.
func NewServer(opts ...Option) *Server {
	s := &Server{
		locks:         make(map[string]*lockState),
		sweepInterval: 50 * time.Millisecond,
		stopCh:        make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.wg.Add(1)
	go s.sweepLoop()
	return s
}

// Close stops the background sweeper.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.stopCh)
	s.mu.Unlock()
	s.wg.Wait()
}

// Lock acquires the named lock, bound to a lease with the given TTL.
// While held, a background goroutine keeps the lease alive via heartbeats.
// If the lock is held by another live lease, Lock blocks until the lease
// expires or is released, or ctx is cancelled.
func (s *Server) Lock(ctx context.Context, name string, ttl time.Duration) (*Lock, error) {
	if ttl <= 0 {
		return nil, ErrInvalidTTL
	}
	s.mu.Lock()
	st := s.stateLocked(name)
	s.expireLocked(st)
	if st.owner == nil {
		l := s.newLeaseLocked(ttl)
		st.owner = l
		s.mu.Unlock()
		return s.newHandle(name, l), nil
	}
	w := &waiter{ttl: ttl, ch: make(chan *lease, 1)}
	st.waiters = append(st.waiters, w)
	s.mu.Unlock()

	select {
	case l := <-w.ch:
		return s.newHandle(name, l), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// keepAlive extends the lease deadline by its TTL. Called by the
// holder's heartbeat goroutine.
func (s *Server) keepAlive(name string, leaseID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.locks[name]
	if st == nil || st.owner == nil || st.owner.id != leaseID {
		return ErrLockLost
	}
	if !time.Now().Before(st.owner.deadline) {
		s.expireLocked(st)
		return ErrLockLost
	}
	st.owner.deadline = time.Now().Add(st.owner.ttl)
	return nil
}

// release lets go of the lock and immediately grants it to the next waiter.
func (s *Server) release(name string, leaseID uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.locks[name]
	if st == nil || st.owner == nil || st.owner.id != leaseID {
		return ErrLockLost
	}
	st.owner = nil
	s.grantLocked(st)
	return nil
}

func (s *Server) stateLocked(name string) *lockState {
	st, ok := s.locks[name]
	if !ok {
		st = &lockState{}
		s.locks[name] = st
	}
	return st
}

func (s *Server) newLeaseLocked(ttl time.Duration) *lease {
	s.nextLeaseID++
	s.fenceCounter++
	return &lease{
		id:       s.nextLeaseID,
		token:    s.fenceCounter,
		ttl:      ttl,
		deadline: time.Now().Add(ttl),
	}
}

// expireLocked releases the owner lease if its deadline has passed and
// hands the lock to the next waiter.
func (s *Server) expireLocked(st *lockState) {
	if st.owner != nil && !time.Now().Before(st.owner.deadline) {
		st.owner = nil
	}
	s.grantLocked(st)
}

// grantLocked pops the next waiter (if the lock is free) and delivers a
// fresh lease to it. If the waiter has already given up, the lease simply
// expires on its TTL and is reclaimed by the sweeper.
func (s *Server) grantLocked(st *lockState) {
	if st.owner != nil || len(st.waiters) == 0 {
		return
	}
	w := st.waiters[0]
	st.waiters = st.waiters[1:]
	l := s.newLeaseLocked(w.ttl)
	st.owner = l
	w.ch <- l
}

func (s *Server) sweepLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(s.sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.mu.Lock()
			for _, st := range s.locks {
				s.expireLocked(st)
			}
			s.mu.Unlock()
		}
	}
}

// Lock is a held lock handle bound to a lease.
type Lock struct {
	srv   *Server
	name  string
	lease *lease

	stopHB   chan struct{} // closed to stop the heartbeat (Unlock / SimulateCrash)
	lostCh   chan struct{} // closed when the lease is lost or released
	hbOnce   sync.Once
	lostOnce sync.Once
}

func (s *Server) newHandle(name string, l *lease) *Lock {
	h := &Lock{
		srv:    s,
		name:   name,
		lease:  l,
		stopHB: make(chan struct{}),
		lostCh: make(chan struct{}),
	}
	go h.heartbeatLoop()
	return h
}

// FenceToken returns the monotonically increasing fencing token issued
// with this acquisition. Downstream resources use it to reject writes
// from stale (previously fenced-out) holders.
func (l *Lock) FenceToken() uint64 { return l.lease.token }

// Done returns a channel closed when the lease is lost (expired despite
// heartbeats) or released.
func (l *Lock) Done() <-chan struct{} { return l.lostCh }

// Unlock releases the lock, waking the next waiter immediately.
func (l *Lock) Unlock() error {
	l.stopHeartbeat()
	err := l.srv.release(l.name, l.lease.id)
	l.markLost()
	return err
}

// SimulateCrash stops the heartbeat goroutine without releasing the
// lease, mimicking a node that freezes / partitions away. The lease will
// expire naturally once its TTL elapses.
func (l *Lock) SimulateCrash() { l.stopHeartbeat() }

func (l *Lock) stopHeartbeat() {
	l.hbOnce.Do(func() { close(l.stopHB) })
}

func (l *Lock) markLost() {
	l.lostOnce.Do(func() { close(l.lostCh) })
}

func (l *Lock) heartbeatLoop() {
	interval := l.lease.ttl / 3
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stopHB:
			return
		case <-l.lostCh:
			return
		case <-ticker.C:
			if err := l.srv.keepAlive(l.name, l.lease.id); err != nil {
				l.markLost()
				return
			}
		}
	}
}
