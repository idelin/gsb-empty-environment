package fetchquota

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RateLimit is a token-bucket specification: Rate tokens per second with an
// instantaneous allowance of Burst.
type RateLimit struct {
	Rate  float64
	Burst float64
}

// Config declares the per-host and per-account limits. Hosts and accounts
// absent from the maps are rejected unless the corresponding Default is set.
type Config struct {
	Hosts          map[string]RateLimit
	Accounts       map[string]RateLimit
	DefaultHost    RateLimit
	DefaultAccount RateLimit
}

var (
	// ErrUnknownHost is returned when no limit (and no default) matches a host.
	ErrUnknownHost = errors.New("fetchquota: unknown host")
	// ErrUnknownAccount is returned when no limit (and no default) matches an account.
	ErrUnknownAccount = errors.New("fetchquota: unknown account")
	// ErrDeadlineExceeded means the queue did not reach the request before its
	// deadline expired.
	ErrDeadlineExceeded = errors.New("fetchquota: acquire deadline exceeded")
)

// queueKey identifies one FIFO queue: all requests of one account against one
// host. Cooldown applies at this granularity, so a Retry-After received for
// account A on host H never stalls account B on host H.
type queueKey struct {
	account string
	host    string
}

type queueState struct {
	cooldownUntil time.Time
	waiters       []*waiter
}

type waiter struct {
	timer Timer
	stop  chan struct{}
}

// Scheduler mediates crawler requests across shared host and per-account
// quotas. It is safe for concurrent use.
type Scheduler struct {
	clock Clock

	mu             sync.Mutex
	cond           *sync.Cond
	wakeSeq        uint64
	hosts          map[string]*bucket
	accounts       map[string]*bucket
	queues         map[queueKey]*queueState
	defaultHost    RateLimit
	defaultAccount RateLimit
}

// New builds a Scheduler using the given clock. Pass NewRealClock() in
// production or a FakeClock in tests.
func New(clock Clock, cfg Config) (*Scheduler, error) {
	if clock == nil {
		return nil, errors.New("fetchquota: clock is required")
	}
	now := clock.Now()
	s := &Scheduler{
		clock:    clock,
		hosts:    make(map[string]*bucket, len(cfg.Hosts)),
		accounts: make(map[string]*bucket, len(cfg.Accounts)),
		queues:   make(map[queueKey]*queueState),
	}
	s.cond = sync.NewCond(&s.mu)
	if fc, ok := clock.(*FakeClock); ok {
		fc.onAdvance = func() {
			s.mu.Lock()
			s.notify()
			s.mu.Unlock()
		}
	}
	for name, rl := range cfg.Hosts {
		if err := validateLimit(rl); err != nil {
			return nil, fmt.Errorf("fetchquota: host %q: %w", name, err)
		}
		s.hosts[name] = newBucket(rl, now)
	}
	for name, rl := range cfg.Accounts {
		if err := validateLimit(rl); err != nil {
			return nil, fmt.Errorf("fetchquota: account %q: %w", name, err)
		}
		s.accounts[name] = newBucket(rl, now)
	}
	if len(cfg.Hosts) == 0 {
		if err := validateLimit(cfg.DefaultHost); err != nil {
			return nil, fmt.Errorf("fetchquota: default host: %w", err)
		}
	}
	if len(cfg.Accounts) == 0 {
		if err := validateLimit(cfg.DefaultAccount); err != nil {
			return nil, fmt.Errorf("fetchquota: default account: %w", err)
		}
	}
	s.defaultHost = cfg.DefaultHost
	s.defaultAccount = cfg.DefaultAccount
	return s, nil
}

func validateLimit(rl RateLimit) error {
	if rl.Rate <= 0 || rl.Burst <= 0 {
		return errors.New("rate and burst must be positive")
	}
	return nil
}

// hostBucket returns the bucket for host, creating it from the default limit
// on first use.
func (s *Scheduler) hostBucket(name string) (*bucket, error) {
	if b, ok := s.hosts[name]; ok {
		return b, nil
	}
	if s.defaultHost.Rate <= 0 {
		return nil, ErrUnknownHost
	}
	b := newBucket(s.defaultHost, s.clock.Now())
	s.hosts[name] = b
	return b, nil
}

// accountBucket returns the bucket for account, creating it from the default
// limit on first use.
func (s *Scheduler) accountBucket(name string) (*bucket, error) {
	if b, ok := s.accounts[name]; ok {
		return b, nil
	}
	if s.defaultAccount.Rate <= 0 {
		return nil, ErrUnknownAccount
	}
	b := newBucket(s.defaultAccount, s.clock.Now())
	s.accounts[name] = b
	return b, nil
}

// Acquire reserves one request slot for account against host, blocking until
// both quotas can be charged, ctx is cancelled, or the wait runs past a zero
// deadline. The returned Lease must be finalized exactly once: call MarkSent
// after a successful send or Release to refund both levels.
func (s *Scheduler) Acquire(ctx context.Context, account, host string) (*Lease, error) {
	return s.AcquireUntil(ctx, account, host, time.Time{})
}

// AcquireUntil is Acquire with an absolute deadline measured against the
// injected clock. A zero deadline means no deadline. At the exact boundary
// instant a ready grant is made before the deadline is judged to have passed.
func (s *Scheduler) AcquireUntil(ctx context.Context, account, host string, deadline time.Time) (*Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	hb, err := s.hostBucket(host)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	ab, err := s.accountBucket(account)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}

	key := queueKey{account: account, host: host}
	q := s.queues[key]
	if q == nil {
		q = &queueState{}
		s.queues[key] = q
	}
	w := &waiter{}
	q.waiters = append(q.waiters, w)

	stopCtxWatch := s.watchContext(ctx)
	defer stopCtxWatch()

	for {
		if err := ctx.Err(); err != nil {
			s.removeWaiter(q, w)
			s.mu.Unlock()
			return nil, err
		}

		now := s.clock.Now()
		isHead := len(q.waiters) > 0 && q.waiters[0] == w
		if isHead && !now.Before(q.cooldownUntil) {
			hb.advance(now)
			ab.advance(now)
			if hb.tokens >= 1 && ab.tokens >= 1 {
				// Grant first, then compare against the deadline: a request
				// that becomes issuable exactly at the deadline is granted.
				hb.take()
				ab.take()
				s.removeWaiter(q, w)
				s.mu.Unlock()
				return &Lease{s: s, hostBucket: hb, accountBucket: ab, key: key}, nil
			}
		}

		if !deadline.IsZero() && !s.earliestGrant(q, hb, ab, now).Before(deadline) {
			s.removeWaiter(q, w)
			s.mu.Unlock()
			return nil, ErrDeadlineExceeded
		}

		s.wait(q, w, hb, ab, deadline, !deadline.IsZero())
	}
}

// wait blocks until the next instant at which the waiter's position might
// change, or another goroutine broadcasts a state change (refund, cancel,
// cooldown).
//
// FakeClock mode never relies on timer-goroutine scheduling. With an
// explicit deadline the call owns time: it advances the clock itself and
// returns so the grant loop re-evaluates. Without one it sleeps on the
// condition until notify bumps wakeSeq, so a broadcast that races ahead of
// cond.Wait cannot be lost.
//
// Real-clock mode arms an injected timer whose firing broadcasts the
// condition. The scheduler lock is never held while Advance runs, avoiding
// lock-order inversion against firing timer goroutines.
func (s *Scheduler) wait(q *queueState, w *waiter, hb, ab *bucket, deadline time.Time, hasDeadline bool) {
	now := s.clock.Now()
	next := s.earliestGrant(q, hb, ab, now)
	if !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
		next = deadline
	}

	if fc, ok := s.clock.(*FakeClock); ok {
		if hasDeadline {
			d := time.Duration(0)
			if !next.IsZero() {
				if delta := next.Sub(now); delta > 0 {
					d = delta
				}
			}
			if d > 0 {
				s.mu.Unlock()
				fc.Advance(d)
				s.mu.Lock()
			}
			return
		}

		// Blocking fake-clock wait. Every state change that matters (clock
		// movement, refund, cancellation, cooldown) bumps wakeSeq through
		// notify; sleeping on a captured sequence means a broadcast that
		// races ahead of cond.Wait is observed on the next notification
		// instead of being lost. Context cancellation is re-checked by the
		// grant loop.
		seq := s.wakeSeq
		for s.wakeSeq == seq {
			s.cond.Wait()
		}
		return
	}

	s.scheduleWake(w, next)
	defer s.clearWake(w)
	s.cond.Wait()
}

// scheduleWake arms a timer that broadcasts the condition when the injected
// clock reaches at, plus a watcher that broadcasts on context cancellation.
// A zero at means only external state changes should wake the waiter.
func (s *Scheduler) scheduleWake(w *waiter, at time.Time) {
	stop := make(chan struct{})
	w.stop = stop
	if !at.IsZero() {
		d := at.Sub(s.clock.Now())
		if d < 0 {
			d = 0
		}
		timer := s.clock.NewTimer(d)
		w.timer = timer
		go func() {
			select {
			case <-timer.C():
				s.mu.Lock()
				s.notify()
				s.mu.Unlock()
			case <-stop:
			}
		}()
	}
}

// clearWake disarms the timer and releases its goroutine. It runs with the
// scheduler lock held, matching the cond.Wait wake-up contract.
func (s *Scheduler) clearWake(w *waiter) {
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.stop != nil {
		close(w.stop)
		w.stop = nil
	}
}

// earliestGrant reports the earliest clock instant at which w could be
// granted given its queue position, the cooldown, and both bucket refills.
func (s *Scheduler) earliestGrant(q *queueState, hb, ab *bucket, now time.Time) time.Time {
	next := now
	if q.cooldownUntil.After(next) {
		next = q.cooldownUntil
	}
	hb.advance(now)
	ab.advance(now)
	if hb.tokens < 1 {
		if t := now.Add(hb.nextFull(now)); t.After(next) {
			next = t
		}
	}
	if ab.tokens < 1 {
		if t := now.Add(ab.nextFull(now)); t.After(next) {
			next = t
		}
	}
	return next
}

// removeWaiter detaches w from its queue and wakes other goroutines: the next
// waiter may now be head, and refunds may have freed tokens.
func (s *Scheduler) removeWaiter(q *queueState, w *waiter) {
	for i, cand := range q.waiters {
		if cand == w {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			break
		}
	}
	s.notify()
}

// notify wakes every parked waiter and records the wake-up. Bumping wakeSeq
// on every notification lets fake-clock waiters detect a broadcast even when
// it is emitted just before they enter cond.Wait.
func (s *Scheduler) notify() {
	s.wakeSeq++
	s.cond.Broadcast()
}

// watchContext broadcasts the condition when ctx is done. The returned
// function releases the watcher and is safe to call once.
func (s *Scheduler) watchContext(ctx context.Context) func() {
	stop := make(chan struct{})
	done := ctx.Done()
	if done == nil {
		close(stop)
		return func() {}
	}
	go func() {
		select {
		case <-done:
			s.mu.Lock()
			s.notify()
			s.mu.Unlock()
		case <-stop:
		}
	}()
	return func() { close(stop) }
}

// Cooldown applies a Retry-After: the queue for (account, host) blocks new
// requests until now+d. Other queues, including the same account against other
// hosts and other accounts against this host, keep running. A non-positive d
// is ignored.
func (s *Scheduler) Cooldown(account, host string, d time.Duration) {
	if d <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := queueKey{account: account, host: host}
	q := s.queues[key]
	if q == nil {
		q = &queueState{}
		s.queues[key] = q
	}
	until := s.clock.Now().Add(d)
	if until.After(q.cooldownUntil) {
		q.cooldownUntil = until
	}
	s.notify()
}

// CooldownUntil is Cooldown with an absolute instant.
func (s *Scheduler) CooldownUntil(account, host string, until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := queueKey{account: account, host: host}
	q := s.queues[key]
	if q == nil {
		q = &queueState{}
		s.queues[key] = q
	}
	if until.After(q.cooldownUntil) {
		q.cooldownUntil = until
	}
	s.notify()
}

// hostTokens exposes a bucket balance for tests.
func (s *Scheduler) hostTokens(host string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.hosts[host]
	if b == nil {
		return 0
	}
	return b.available(s.clock.Now())
}

// accountTokens exposes a bucket balance for tests.
func (s *Scheduler) accountTokens(account string) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.accounts[account]
	if b == nil {
		return 0
	}
	return b.available(s.clock.Now())
}

// queueDepth reports how many requests are waiting in a queue (test helper).
func (s *Scheduler) queueDepth(account, host string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queues[queueKey{account: account, host: host}]
	if q == nil {
		return 0
	}
	return len(q.waiters)
}
