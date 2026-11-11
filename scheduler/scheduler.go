// Package scheduler dispatches expensive credential verification for a login
// host.
//
// It isolates verification cost along three axes:
//
//   - Per-tenant concurrency caps, so one tenant cannot monopolize workers.
//   - A global pool of memory tokens, so queued and running verifications are
//     bounded host-wide.
//   - A fixed dummy record for unknown accounts, so requests for
//     non-existent accounts pay the same expensive verification and occupy the
//     same resources as real accounts and cannot be used to enumerate users or
//     exhaust the worker pool for free.
//
// Cancellation is split into two distinct phases. A request canceled while
// still queued aborts immediately without starting any verification and
// without consuming slots. Once admission succeeds the verification callback
// runs uninterruptibly: the caller's context is detached so the expensive
// check always completes, and the request observes its terminal result only
// after the uniform minimum delay. Every externally visible authentication
// outcome is therefore delayed to at least MinDelay and reported as the same
// error, eliminating timing and error-based account enumeration.
package scheduler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrDenied is the single error returned for every failed authentication
// outcome: unknown account, wrong secret, lookup failure, or a verifier error.
// Callers deliberately cannot distinguish these cases.
var ErrDenied = errors.New("scheduler: authentication denied")

// TenantID identifies a tenant whose traffic is capped independently.
type TenantID string

// AccountID identifies an account within a tenant.
type AccountID string

// Account is a record resolved by the host's Lookup callback. The Scheduler
// treats the Dummy record like any other account.
type Account struct {
	// ID is the resolved account identifier.
	ID AccountID
	// Record holds host-specific account data; it is opaque to the scheduler.
	Record any
}

// LookupFunc resolves an account for a tenant. It runs while the request
// holds its tenant and global admission slots.
type LookupFunc func(ctx context.Context, tenant TenantID, account AccountID) (*Account, error)

// VerifyFunc performs the expensive secret check against the resolved account.
// It runs uninterruptibly: the context passed here is never canceled by the
// scheduler, so callbacks must not rely on cancellation to bound their work.
type VerifyFunc func(ctx context.Context, account *Account, secret []byte) (bool, error)

// Config configures a Scheduler.
type Config struct {
	// PerTenantCap is the maximum number of admitted verifications per tenant.
	PerTenantCap int
	// GlobalTokens is the host-wide pool of memory tokens shared by every
	// admitted verification across all tenants.
	GlobalTokens int
	// MinDelay is the uniform minimum wall time every authentication result is
	// held before being returned to the caller.
	MinDelay time.Duration
	// Lookup resolves accounts.
	Lookup LookupFunc
	// Verify performs the expensive secret check.
	Verify VerifyFunc
	// Dummy is the fixed record used for unknown accounts.
	Dummy *Account
	// Clock defaults to RealClock when nil; tests inject a FakeClock.
	Clock Clock
}

// Scheduler dispatches credential verification with tenant isolation, global
// memory-token limits, dummy-record defense and uniform timing.
type Scheduler struct {
	cfg        Config
	globalSem  *fairSemaphore
	tenantsMu  sync.Mutex
	tenants    map[TenantID]*tenantState
	secretLive atomic.Int64
	// lastScrubbed records whether the most recently released secret copy was
	// fully zeroed. It exists for tests of sensitive-buffer cleanup.
	lastScrubbed atomic.Bool
}

type tenantState struct {
	sem *fairSemaphore
}

// New validates the configuration and returns a ready Scheduler.
func New(cfg Config) (*Scheduler, error) {
	if cfg.PerTenantCap <= 0 {
		return nil, errors.New("scheduler: PerTenantCap must be positive")
	}
	if cfg.GlobalTokens <= 0 {
		return nil, errors.New("scheduler: GlobalTokens must be positive")
	}
	if cfg.MinDelay < 0 {
		return nil, errors.New("scheduler: MinDelay must not be negative")
	}
	if cfg.Lookup == nil || cfg.Verify == nil {
		return nil, errors.New("scheduler: Lookup and Verify callbacks are required")
	}
	if cfg.Dummy == nil {
		return nil, errors.New("scheduler: Dummy account is required")
	}
	if cfg.Clock == nil {
		cfg.Clock = RealClock{}
	}

	return &Scheduler{
		cfg:       cfg,
		globalSem: newFairSemaphore(cfg.GlobalTokens),
		tenants:   make(map[TenantID]*tenantState),
	}, nil
}

// tenant returns the lazily created admission state for a tenant.
func (s *Scheduler) tenant(id TenantID) *tenantState {
	s.tenantsMu.Lock()
	defer s.tenantsMu.Unlock()

	ts, ok := s.tenants[id]
	if !ok {
		ts = &tenantState{sem: newFairSemaphore(s.cfg.PerTenantCap)}
		s.tenants[id] = ts
	}
	return ts
}

// Authenticate verifies secret against account within tenant.
//
// A request canceled before admission (while queued on the tenant or global
// semaphore) returns the context error promptly, starts no verification and
// consumes no slots. Once admitted, Lookup and Verify run to completion
// uninterruptibly; the terminal outcome (always ErrDenied on failure) is
// returned no earlier than Config.MinDelay after the call started.
func (s *Scheduler) Authenticate(ctx context.Context, tenant TenantID, account AccountID, secret []byte) error {
	start := s.cfg.Clock.Now()

	// Own a private copy of the sensitive input so the caller's buffer cannot
	// alias scheduler state, and guarantee it is scrubbed on every exit path,
	// including prompt cancellation while still queued.
	workSecret := make([]byte, len(secret))
	copy(workSecret, secret)
	s.secretLive.Add(1)
	defer func() {
		scrubbed := true
		for i := range workSecret {
			workSecret[i] = 0
		}
		for _, b := range workSecret {
			if b != 0 {
				scrubbed = false
			}
		}
		s.lastScrubbed.Store(scrubbed)
		s.secretLive.Add(-1)
	}()

	ts := s.tenant(tenant)

	// --- Queued phase: interruptible, holds no resources on cancellation. ---
	if err := ts.sem.Acquire(ctx); err != nil {
		return err
	}
	defer ts.sem.Release()

	if err := s.globalSem.Acquire(ctx); err != nil {
		return err
	}
	defer s.globalSem.Release()

	// --- Running phase: verification is uninterruptible. The detached context
	// ensures the expensive callback always completes even if the caller walks
	// away; ctx is only observed when waiting for the terminal result. ---
	detached := context.WithoutCancel(ctx)
	type result struct {
		err error
	}
	done := make(chan result, 1)

	go func() {
		resolved, lookupErr := s.cfg.Lookup(detached, tenant, account)
		if lookupErr != nil || resolved == nil {
			// Unknown account or lookup failure: fall through to the fixed
			// dummy record and run a full verification against it.
			resolved = s.cfg.Dummy
		}

		ok, verifyErr := s.cfg.Verify(detached, resolved, workSecret)
		switch {
		case verifyErr != nil || !ok:
			done <- result{err: ErrDenied}
		default:
			done <- result{}
		}
	}()

	var res result
	select {
	case res = <-done:
	case <-ctx.Done():
		// Verification cannot be interrupted; wait for the detached work to
		// finish so resources and the sensitive buffer stay under control.
		res = <-done
	}

	// Uniform minimum delay for every terminal authentication outcome. The
	// wait itself is not interruptible: a caller canceling during the floor
	// still receives the same result at the same time as every other caller.
	if elapsed := s.cfg.Clock.Now().Sub(start); elapsed < s.cfg.MinDelay {
		timer := s.cfg.Clock.NewTimer(s.cfg.MinDelay - elapsed)
		<-timer.Chan()
	}

	return res.err
}

// The helpers below are package-private inspection points used by tests to
// prove resource conservation; production code never calls them.

func (s *Scheduler) globalInflight() int { return s.globalSem.inflight() }

func (s *Scheduler) globalQueued() int { return s.globalSem.queued() }

func (s *Scheduler) tenantInflight(id TenantID) int { return s.tenant(id).sem.inflight() }

func (s *Scheduler) tenantQueued(id TenantID) int { return s.tenant(id).sem.queued() }

func (s *Scheduler) liveSecrets() int64 { return s.secretLive.Load() }
