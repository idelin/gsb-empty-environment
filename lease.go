// Package authbackoff provides per-account attempt leases that keep password
// guessing backoff effective when several requests for the same account run
// concurrently.
//
// The classic weakness is a read-check-then-write sequence in the login
// handler: two parallel requests both observe "not locked yet" and both run a
// password check. Manager.Begin performs the lock check and the attempt
// reservation inside one critical section, so at most one attempt can slip
// through while an account is locked.
package authbackoff

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Outcome classifies how a reserved attempt finished.
type Outcome int

const (
	// AuthFailure means the credential check completed and the secret was
	// wrong. Only this outcome grows the failure counter.
	AuthFailure Outcome = iota + 1

	// AuthSuccess means the credential check completed and the secret was
	// correct. It clears the penalty state unless a newer attempt already
	// reported an outcome.
	AuthSuccess

	// SystemFault means the check could not be completed because of an
	// infrastructure problem (downstream error, panic, ...). It neither
	// penalizes the account holder nor advances the attempt ordering.
	SystemFault

	// Timeout means the check exceeded its deadline. Like SystemFault it
	// leaves the penalty state untouched.
	Timeout
)

// Clock is the time source used by Manager. Production code uses the wall
// clock; tests inject a fake one.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Config tunes a Manager. The zero value is replaced by safe defaults in
// NewManager.
type Config struct {
	// BaseBackoff is the lock window after the first recorded failure.
	BaseBackoff time.Duration

	// MaxBackoff caps the exponentially growing lock window.
	MaxBackoff time.Duration

	// MaxFailures, when positive, switches an account to the maximum lock
	// window once its failure counter reaches the threshold. A successful
	// attempt or an explicit reset is then required to clear it. Zero means
	// the threshold is never enforced.
	MaxFailures uint64

	// Clock overrides the time source. nil selects the wall clock.
	Clock Clock
}

var (
	// ErrAccountLocked is returned by Begin while the account is inside a
	// backoff window.
	ErrAccountLocked = errors.New("authbackoff: account locked")

	// ErrLeaseFinished is returned when a lease is ended more than once.
	ErrLeaseFinished = errors.New("authbackoff: lease already finished")
)

// Manager hands out attempt leases keyed by account identifier. A Manager is
// safe for concurrent use.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	accounts map[string]*account
}

// account holds the mutable penalty state of one account. All fields are
// guarded by Manager.mu.
type account struct {
	// failures is the saturated counter of recorded AuthFailure outcomes.
	failures uint64

	// lockedUntil is the end of the current backoff window. The zero time
	// means the account is unlocked.
	lockedUntil time.Time

	// seq is the last reserved attempt sequence. Every Begin hands out a
	// strictly greater sequence while the counter is below MaxUint64.
	seq uint64

	// lastAppliedSeq is the high-water mark of attempts whose outcome was
	// AuthFailure or AuthSuccess. Faults and timeouts do not advance it.
	lastAppliedSeq uint64
}

// Lease is one reserved attempt. The caller runs the credential check and
// must call End exactly once with the outcome.
type Lease struct {
	mgr  *Manager
	key  string
	seq  uint64
	done bool
}

// NewManager constructs a Manager, replacing zero-valued Config fields with
// safe defaults.
func NewManager(cfg Config) *Manager {
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = time.Second
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 30 * time.Minute
	}
	if cfg.MaxBackoff < cfg.BaseBackoff {
		cfg.MaxBackoff = cfg.BaseBackoff
	}
	if cfg.Clock == nil {
		cfg.Clock = systemClock{}
	}
	return &Manager{
		cfg:      cfg,
		accounts: make(map[string]*account),
	}
}

// Begin atomically checks the lock state and reserves an attempt for
// accountID. When the account is locked it returns ErrAccountLocked together
// with the unlock time and no lease. Otherwise it returns a new lease and a
// zero time.
func (m *Manager) Begin(accountID string) (*Lease, time.Time, error) {
	now := m.cfg.Clock.Now()

	m.mu.Lock()
	defer m.mu.Unlock()

	acct := m.accounts[accountID]
	if acct == nil {
		acct = &account{}
		m.accounts[accountID] = acct
	}

	// A clock that jumps backwards can only make now smaller, so a lock that
	// was in force stays in force.
	if now.Before(acct.lockedUntil) {
		return nil, acct.lockedUntil, ErrAccountLocked
	}

	if acct.seq < math.MaxUint64 {
		acct.seq++
	}
	return &Lease{mgr: m, key: accountID, seq: acct.seq}, time.Time{}, nil
}

// Sequence returns the attempt's reservation sequence, useful for logging and
// correlation.
func (l *Lease) Sequence() uint64 { return l.seq }

// End records the attempt outcome and releases the reservation. It returns
// ErrLeaseFinished on a repeated call and an error for an unknown outcome.
func (l *Lease) End(outcome Outcome) error {
	switch outcome {
	case AuthFailure, AuthSuccess, SystemFault, Timeout:
	default:
		return fmt.Errorf("authbackoff: unknown outcome %d", outcome)
	}

	now := l.mgr.cfg.Clock.Now()

	l.mgr.mu.Lock()
	defer l.mgr.mu.Unlock()

	if l.done {
		return ErrLeaseFinished
	}
	acct := l.mgr.accounts[l.key]
	l.done = true

	switch outcome {
	case SystemFault, Timeout:
		// The account holder is not penalized for infrastructure problems and
		// the attempt ordering is not advanced, so an in-flight older success
		// remains eligible to clear the state later.
		return nil

	case AuthFailure:
		// Failures always count, even when an older attempt finishes late:
		// a confirmed wrong password is evidence an attacker is still trying.
		if acct.failures < math.MaxUint64 {
			acct.failures++
		}
		acct.lockedUntil = l.mgr.nextUnlock(acct, now)
		if l.seq > acct.lastAppliedSeq {
			acct.lastAppliedSeq = l.seq
		}

	case AuthSuccess:
		if l.seq < acct.lastAppliedSeq {
			// A newer attempt already reported a failure. This success is a
			// stale response to an earlier attempt and must not erase the
			// failure that started after it.
			break
		}
		acct.failures = 0
		acct.lockedUntil = time.Time{}
		acct.lastAppliedSeq = l.seq
	}
	return nil
}

// nextUnlock computes the end of the backoff window after a fresh failure.
// The window never shrinks: if the clock moved backwards or a longer window
// was already in force, the existing lockedUntil is retained.
func (m *Manager) nextUnlock(acct *account, now time.Time) time.Time {
	window := m.backoffWindow(acct.failures)
	unlockAt := now.Add(window)
	if unlockAt.Before(acct.lockedUntil) {
		return acct.lockedUntil
	}
	return unlockAt
}

// backoffWindow returns the lock window for a given failure count. Growth is
// exponential (base, 2*base, 4*base, ...) up to MaxBackoff; once
// MaxFailures is configured and reached, the maximum window always applies.
func (m *Manager) backoffWindow(failures uint64) time.Duration {
	switch {
	case failures == 0:
		return 0
	case m.cfg.MaxFailures > 0 && failures >= m.cfg.MaxFailures:
		return m.cfg.MaxBackoff
	case failures-1 > 60:
		return m.cfg.MaxBackoff
	}

	window := m.cfg.BaseBackoff << (failures - 1)
	if window <= 0 || window > m.cfg.MaxBackoff {
		return m.cfg.MaxBackoff
	}
	return window
}

// Failures returns the current failure counter for accountID.
func (m *Manager) Failures(accountID string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if acct := m.accounts[accountID]; acct != nil {
		return acct.failures
	}
	return 0
}

// LockedUntil returns the end of the current backoff window, or the zero time
// when the account is unlocked.
func (m *Manager) LockedUntil(accountID string) time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	if acct := m.accounts[accountID]; acct != nil {
		return acct.lockedUntil
	}
	return time.Time{}
}

// RetryAfter reports how long accountID remains locked relative to now, or
// zero when it is unlocked.
func (m *Manager) RetryAfter(accountID string, now time.Time) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	acct := m.accounts[accountID]
	if acct == nil || !now.Before(acct.lockedUntil) {
		return 0
	}
	return acct.lockedUntil.Sub(now)
}
