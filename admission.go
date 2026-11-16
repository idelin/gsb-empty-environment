package netsec

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// SpeedOfLight is used to convert a DS-TWR time of flight into metres.
const SpeedOfLight = 299792458.0

// defaultClockSkew applies when SessionConfig.MaxClockSkew is left zero.
const defaultClockSkew = 50 * time.Millisecond

// Sentinel errors returned by the admission package. Callers should use
// errors.Is to match them; wrapped errors carry the offending values.
var (
	ErrInvalidConfig      = errors.New("netsec: invalid session configuration")
	ErrNoActiveSession    = errors.New("netsec: no active ranging session")
	ErrSessionMismatch    = errors.New("netsec: result is not bound to the current session")
	ErrIllegalTimestamp   = errors.New("netsec: illegal ranging timestamp")
	ErrDuplicateSTS       = errors.New("netsec: duplicate STS index")
	ErrSTSRollback        = errors.New("netsec: STS index rollback")
	ErrSTSQuality         = errors.New("netsec: STS quality marker failed")
	ErrReplyDelayExceeded = errors.New("netsec: reply delay exceeds session limit")
	ErrInvalidTiming      = errors.New("netsec: invalid DS-TWR timing values")
	ErrNegativeDistance   = errors.New("netsec: negative ranging distance")
)

// SessionConfig declares the security parameters of one ranging session.
type SessionConfig struct {
	// SessionID is the identifier every admitted result must carry.
	SessionID string
	// MaxReplyDelay bounds Da and Db; a result with either reply time above
	// this value is rejected so an attacker cannot inflate reply latency.
	MaxReplyDelay time.Duration
	// MaxClockSkew tolerates small clock differences when validating
	// timestamps. Zero means defaultClockSkew.
	MaxClockSkew time.Duration
}

// RangingResult is a single DS-TWR measurement submitted for admission.
//
// Ra and Rb are the round-trip times measured at the two devices, while Da
// and Db are their respective reply times.
type RangingResult struct {
	SessionID string
	STSIndex  uint64
	STSOK     bool
	Timestamp time.Time
	Ra        time.Duration
	Rb        time.Duration
	Da        time.Duration
	Db        time.Duration
}

// VerifiedRanging is returned for a result that passed every admission rule.
type VerifiedRanging struct {
	SessionID string
	STSIndex  uint64
	Timestamp time.Time
	ToF       time.Duration
	Distance  float64 // metres
}

// Session enforces admission rules for one ranging session. It is safe for
// concurrent use. Rejected results never advance the STS sequence state.
type Session struct {
	cfg       SessionConfig
	createdAt time.Time
	now       func() time.Time

	mu      sync.Mutex
	lastSTS uint64
	hasSTS  bool
}

// NewSession creates a session anchored to the real wall clock.
func NewSession(cfg SessionConfig) (*Session, error) {
	return newSessionAt(cfg, time.Now)
}

func newSessionAt(cfg SessionConfig, now func() time.Time) (*Session, error) {
	if cfg.SessionID == "" {
		return nil, fmt.Errorf("%w: empty session id", ErrInvalidConfig)
	}
	if cfg.MaxReplyDelay <= 0 {
		return nil, fmt.Errorf("%w: MaxReplyDelay must be positive", ErrInvalidConfig)
	}
	if cfg.MaxClockSkew < 0 {
		return nil, fmt.Errorf("%w: negative MaxClockSkew", ErrInvalidConfig)
	}
	if cfg.MaxClockSkew == 0 {
		cfg.MaxClockSkew = defaultClockSkew
	}
	return &Session{
		cfg:       cfg,
		createdAt: now(),
		now:       now,
	}, nil
}

// ID returns the session identifier.
func (s *Session) ID() string { return s.cfg.SessionID }

// CreatedAt returns the session creation time.
func (s *Session) CreatedAt() time.Time { return s.createdAt }

// LastSTS returns the highest admitted STS index and whether any result has
// been admitted yet.
func (s *Session) LastSTS() (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSTS, s.hasSTS
}

// Admit validates a ranging result against every admission rule and returns
// the verified time of flight and distance on success.
func (s *Session) Admit(result RangingResult) (VerifiedRanging, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if result.SessionID != s.cfg.SessionID {
		return VerifiedRanging{}, fmt.Errorf(
			"%w: result session %q, current session %q",
			ErrSessionMismatch, result.SessionID, s.cfg.SessionID)
	}

	if err := s.validateTimestamp(result.Timestamp); err != nil {
		return VerifiedRanging{}, err
	}

	if s.hasSTS {
		switch {
		case result.STSIndex == s.lastSTS:
			return VerifiedRanging{}, fmt.Errorf(
				"%w: index %d reused", ErrDuplicateSTS, result.STSIndex)
		case result.STSIndex < s.lastSTS:
			return VerifiedRanging{}, fmt.Errorf(
				"%w: index %d follows %d", ErrSTSRollback, result.STSIndex, s.lastSTS)
		}
	}

	if !result.STSOK {
		return VerifiedRanging{}, fmt.Errorf(
			"%w: index %d", ErrSTSQuality, result.STSIndex)
	}

	if result.Ra < 0 || result.Rb < 0 || result.Da < 0 || result.Db < 0 {
		return VerifiedRanging{}, fmt.Errorf(
			"%w: negative timing value", ErrInvalidTiming)
	}

	if result.Da > s.cfg.MaxReplyDelay || result.Db > s.cfg.MaxReplyDelay {
		return VerifiedRanging{}, fmt.Errorf(
			"%w: Da=%s Db=%s limit=%s",
			ErrReplyDelayExceeded, result.Da, result.Db, s.cfg.MaxReplyDelay)
	}

	tof, distance, err := dsTWR(result.Ra, result.Rb, result.Da, result.Db)
	if err != nil {
		return VerifiedRanging{}, err
	}
	if distance < 0 {
		return VerifiedRanging{}, fmt.Errorf(
			"%w: ToF=%s", ErrNegativeDistance, tof)
	}

	s.hasSTS = true
	s.lastSTS = result.STSIndex

	return VerifiedRanging{
		SessionID: s.cfg.SessionID,
		STSIndex:  result.STSIndex,
		Timestamp: result.Timestamp,
		ToF:       tof,
		Distance:  distance,
	}, nil
}

// validateTimestamp rejects zero, future (beyond clock skew), and pre-session
// timestamps, which indicate forged or replayed measurements.
func (s *Session) validateTimestamp(ts time.Time) error {
	if ts.IsZero() {
		return fmt.Errorf("%w: zero timestamp", ErrIllegalTimestamp)
	}
	now := s.now()
	if ts.After(now.Add(s.cfg.MaxClockSkew)) {
		return fmt.Errorf("%w: timestamp %s is more than %s in the future",
			ErrIllegalTimestamp, ts, s.cfg.MaxClockSkew)
	}
	if ts.Before(s.createdAt.Add(-s.cfg.MaxClockSkew)) {
		return fmt.Errorf("%w: timestamp %s predates session created at %s",
			ErrIllegalTimestamp, ts, s.createdAt)
	}
	return nil
}

// dsTWR implements the double-sided two-way ranging equation
//
//	ToF = (Ra*Rb - Da*Db) / (Ra + Rb + Da + Db)
//
// with every duration expressed in nanoseconds, then converts ToF into metres.
func dsTWR(ra, rb, da, db time.Duration) (time.Duration, float64, error) {
	raf := float64(ra)
	rbf := float64(rb)
	daf := float64(da)
	dbf := float64(db)

	denominator := raf + rbf + daf + dbf
	if denominator <= 0 {
		return 0, 0, fmt.Errorf("%w: zero round-trip window", ErrInvalidTiming)
	}

	tofNanos := (raf*rbf - daf*dbf) / denominator
	if math.IsNaN(tofNanos) || math.IsInf(tofNanos, 0) {
		return 0, 0, fmt.Errorf("%w: non-finite time of flight", ErrInvalidTiming)
	}

	tof := time.Duration(tofNanos)
	distance := tofNanos * 1e-9 * SpeedOfLight
	return tof, distance, nil
}

// Service is the door-access entry point. It holds the current ranging
// session; rotating to a new session immediately invalidates results that
// still carry an old session identifier, defeating replayed responses.
type Service struct {
	now func() time.Time

	mu      sync.RWMutex
	current *Session
}

// NewService creates a service anchored to the real wall clock.
func NewService() *Service {
	return &Service{now: time.Now}
}

func newServiceWithClock(now func() time.Time) *Service {
	return &Service{now: now}
}

// StartSession installs a new current session, replacing any previous one.
func (svc *Service) StartSession(cfg SessionConfig) (*Session, error) {
	session, err := newSessionAt(cfg, svc.now)
	if err != nil {
		return nil, err
	}
	svc.mu.Lock()
	svc.current = session
	svc.mu.Unlock()
	return session, nil
}

// CurrentSessionID returns the active session id, or "" when none is active.
func (svc *Service) CurrentSessionID() string {
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.current == nil {
		return ""
	}
	return svc.current.ID()
}

// Admit validates a result against the current session.
func (svc *Service) Admit(result RangingResult) (VerifiedRanging, error) {
	svc.mu.RLock()
	session := svc.current
	svc.mu.RUnlock()
	if session == nil {
		return VerifiedRanging{}, ErrNoActiveSession
	}
	return session.Admit(result)
}
