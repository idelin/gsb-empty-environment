package netsec

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// SpeedOfLightMetersPerSecond is the propagation constant used to convert
// DS-TWR time of flight into a metric distance.
const SpeedOfLightMetersPerSecond = 299792458.0

// defaultMaxClockSkew is applied when SessionConfig.MaxClockSkew is zero.
const defaultMaxClockSkew = time.Second

// Admission errors. Wrap details with fmt.Errorf("%w: ...", ErrX) so callers
// can classify rejections with errors.Is.
var (
	// ErrUnknownSession means the result carries a session ID that has no
	// active session in the manager.
	ErrUnknownSession = errors.New("netsec: unknown or closed ranging session")

	// ErrExpiredSession means the session declared a deadline that has passed.
	ErrExpiredSession = errors.New("netsec: ranging session expired")

	// ErrBadTimestamp means the result timestamp is missing or outside the
	// clock-skew tolerance declared by the session (e.g. a replayed, stale or
	// future-dated response).
	ErrBadTimestamp = errors.New("netsec: illegal or stale result timestamp")

	// ErrSTSDuplicate means the STS index equals the last accepted index.
	ErrSTSDuplicate = errors.New("netsec: duplicate STS index, possible replay")

	// ErrSTSRollback means the STS index is lower than the last accepted index.
	ErrSTSRollback = errors.New("netsec: STS index rollback, possible replay")

	// ErrReplyDelay means the responder reply delay exceeds the session limit.
	ErrReplyDelay = errors.New("netsec: reply delay exceeds session limit")

	// ErrSTSQuality means the STS quality marker on the result failed.
	ErrSTSQuality = errors.New("netsec: STS quality marker failed")

	// ErrNegativeRange means the DS-TWR computation yielded a negative
	// time of flight / distance.
	ErrNegativeRange = errors.New("netsec: negative computed range")

	// ErrInvalidTiming means one of the timing inputs is negative or their
	// sum is zero, making the DS-TWR formula undefined.
	ErrInvalidTiming = errors.New("netsec: invalid timing measurements")
)

// SessionConfig declares the admission policy of one ranging session.
type SessionConfig struct {
	// SessionID is the identifier every accepted result must carry.
	SessionID string

	// MaxReplyDelay is the upper bound accepted for RangingResult.ReplyDelay.
	MaxReplyDelay time.Duration

	// MaxClockSkew is the maximum absolute difference between the manager
	// clock and the result timestamp. Zero selects defaultMaxClockSkew.
	MaxClockSkew time.Duration

	// Deadline, when non-zero, rejects every result observed after it.
	Deadline time.Time
}

// RangingResult is one DS-TWR ranging measurement reported to the door
// service. Ra and Rb are round-trip times, Da and Db are reply times, all
// expressed as durations.
type RangingResult struct {
	// SessionID must match an active session.
	SessionID string

	// STSIndex is the Scrambled Timestamp Sequence index; it must be strictly
	// increasing within a session.
	STSIndex uint64

	// STSQualityOK reports whether the STS integrity check passed.
	STSQualityOK bool

	// Ra, Rb are the two round-trip times used by DS-TWR.
	Ra time.Duration
	Rb time.Duration

	// Da, Db are the two reply (turnaround) times used by DS-TWR.
	Da time.Duration
	Db time.Duration

	// ReplyDelay is the observed responder reply latency for this exchange.
	ReplyDelay time.Duration

	// Timestamp is when the result was produced; it is validated against the
	// manager clock using the session clock-skew tolerance.
	Timestamp time.Time
}

// session holds the mutable replay-protection state of one active session.
type session struct {
	cfg       SessionConfig
	lastIndex uint64
	haveIndex bool
}

// Manager owns the active ranging sessions and serializes admission so that
// STS index checks are safe under concurrent reporters.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
	now      func() time.Time
}

// Option customizes a Manager at construction.
type Option func(*Manager)

// WithClock injects a clock source, primarily for deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// NewManager creates an empty session manager.
func NewManager(opts ...Option) *Manager {
	m := &Manager{
		sessions: make(map[string]*session),
		now:      time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// OpenSession registers a session. Reopening an existing ID resets its STS
// sequence, which is intended for explicit session rotation.
func (m *Manager) OpenSession(cfg SessionConfig) error {
	if cfg.SessionID == "" {
		return errors.New("netsec: session id must not be empty")
	}
	if cfg.MaxReplyDelay < 0 {
		return errors.New("netsec: max reply delay must not be negative")
	}
	if cfg.MaxClockSkew < 0 {
		return errors.New("netsec: max clock skew must not be negative")
	}
	if cfg.MaxClockSkew == 0 {
		cfg.MaxClockSkew = defaultMaxClockSkew
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[cfg.SessionID] = &session{cfg: cfg}
	return nil
}

// CloseSession removes a session; subsequent results for it are rejected.
func (m *Manager) CloseSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionID)
}

// LastSTSIndex returns the last accepted STS index for a session. The bool
// is false when the session is unknown or has accepted no result yet.
func (m *Manager) LastSTSIndex(sessionID string) (uint64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[sessionID]
	if !ok || !sess.haveIndex {
		return 0, false
	}
	return sess.lastIndex, true
}

// Admit applies the full admission policy to a ranging result. On success it
// returns the DS-TWR distance in meters and advances the session STS index;
// on rejection it returns zero distance and a sentinel error classifiable
// with errors.Is. Rejected results never mutate session state.
func (m *Manager) Admit(r RangingResult) (float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()

	sess, ok := m.sessions[r.SessionID]
	if !ok {
		return 0, ErrUnknownSession
	}
	if !sess.cfg.Deadline.IsZero() && now.After(sess.cfg.Deadline) {
		return 0, ErrExpiredSession
	}

	if r.Timestamp.IsZero() {
		return 0, fmt.Errorf("%w: missing timestamp", ErrBadTimestamp)
	}
	skew := now.Sub(r.Timestamp)
	if skew < 0 {
		skew = -skew
	}
	if skew > sess.cfg.MaxClockSkew {
		return 0, fmt.Errorf("%w: drift %s exceeds tolerance %s",
			ErrBadTimestamp, skew, sess.cfg.MaxClockSkew)
	}

	if sess.haveIndex {
		switch {
		case r.STSIndex == sess.lastIndex:
			return 0, fmt.Errorf("%w: index %d reused", ErrSTSDuplicate, r.STSIndex)
		case r.STSIndex < sess.lastIndex:
			return 0, fmt.Errorf("%w: index %d below last %d",
				ErrSTSRollback, r.STSIndex, sess.lastIndex)
		}
	}

	if !r.STSQualityOK {
		return 0, fmt.Errorf("%w: session %s index %d",
			ErrSTSQuality, r.SessionID, r.STSIndex)
	}

	if r.ReplyDelay < 0 {
		return 0, fmt.Errorf("%w: negative reply delay %s",
			ErrInvalidTiming, r.ReplyDelay)
	}
	if r.ReplyDelay > sess.cfg.MaxReplyDelay {
		return 0, fmt.Errorf("%w: %s > %s",
			ErrReplyDelay, r.ReplyDelay, sess.cfg.MaxReplyDelay)
	}

	distance, err := ComputeDistance(r.Ra, r.Rb, r.Da, r.Db)
	if err != nil {
		return 0, err
	}

	sess.lastIndex = r.STSIndex
	sess.haveIndex = true
	return distance, nil
}

// ComputeDistance evaluates the DS-TWR equation
//
//	ToF = (Ra*Rb - Da*Db) / (Ra + Rb + Da + Db)
//
// and returns the propagated distance in meters. Negative timing inputs or a
// zero denominator yield ErrInvalidTiming; a negative ToF yields
// ErrNegativeRange.
func ComputeDistance(ra, rb, da, db time.Duration) (float64, error) {
	if ra < 0 || rb < 0 || da < 0 || db < 0 {
		return 0, fmt.Errorf("%w: Ra=%s Rb=%s Da=%s Db=%s",
			ErrInvalidTiming, ra, rb, da, db)
	}

	// Work in float64 nanoseconds to avoid integer overflow on the products.
	raf, rbf, daf, dbf := float64(ra), float64(rb), float64(da), float64(db)
	denominator := raf + rbf + daf + dbf
	if denominator <= 0 {
		return 0, fmt.Errorf("%w: zero timing sum", ErrInvalidTiming)
	}

	tofNanoseconds := (raf*rbf - daf*dbf) / denominator
	if tofNanoseconds < 0 || math.IsNaN(tofNanoseconds) {
		return 0, fmt.Errorf("%w: ToF=%dns", ErrNegativeRange, int64(tofNanoseconds))
	}

	tofSeconds := tofNanoseconds / float64(time.Second)
	return tofSeconds * SpeedOfLightMetersPerSecond, nil
}
