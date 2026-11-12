package netsec

import (
	"errors"
	"sync"
	"time"
)

// SpeedOfLightMetersPerNanosecond is the vacuum propagation speed of UWB
// radio waves: ~299,792,458 m/s expressed as meters per nanosecond.
const SpeedOfLightMetersPerNanosecond = 0.299792458

// futureSkew tolerates small clock differences when validating timestamps.
const futureSkew = 50 * time.Millisecond

var (
	// ErrSessionUnknown is returned when a result references a session that
	// was never registered with the admission service.
	ErrSessionUnknown = errors.New("netsec: unknown or mismatched session id")
	// ErrSessionClosed is returned when a result arrives for a closed session.
	ErrSessionClosed = errors.New("netsec: session already closed")
	// ErrSessionReused is returned when a session id is registered twice.
	ErrSessionReused = errors.New("netsec: session id already registered")
	// ErrInvalidSession is returned when a session is created with invalid fields.
	ErrInvalidSession = errors.New("netsec: invalid session configuration")
	// ErrStaleIndex is returned when the STS index is not strictly increasing.
	// It covers both replayed (duplicate) and rolled-back indices.
	ErrStaleIndex = errors.New("netsec: sts index must be strictly increasing within a session")
	// ErrQualityFailed is returned when the STS quality marker is not OK.
	ErrQualityFailed = errors.New("netsec: sts quality marker failed")
	// ErrReplyDelay is returned when a reply delay exceeds the session limit.
	ErrReplyDelay = errors.New("netsec: reply delay exceeds session limit")
	// ErrNegativeDistance is returned when DS-TWR yields a negative ToF.
	ErrNegativeDistance = errors.New("netsec: computed distance is negative")
	// ErrInvalidTiming is returned when timing values are non-finite, negative,
	// or produce an unusable DS-TWR denominator.
	ErrInvalidTiming = errors.New("netsec: invalid ranging timing values")
	// ErrInvalidTimestamp is returned when a result timestamp is zero, in the
	// future beyond clock skew, or outside the session lifetime.
	ErrInvalidTimestamp = errors.New("netsec: invalid result timestamp")
)

// Session describes one bounded UWB ranging exchange. All duration fields use
// the same unit (typically nanoseconds) as the timing values in RangingResult.
type Session struct {
	ID            string
	MaxReplyDelay time.Duration
	StartTime     time.Time
	Deadline      time.Time

	mu       sync.Mutex
	closed   bool
	lastSTS  uint64
	hasIndex bool
}

// Service keeps the admission state for every active door-access session.
// It is safe for concurrent use.
type Service struct {
	mu       sync.RWMutex
	sessions map[string]*Session

	now func() time.Time
}

// NewService creates an admission service backed by the system clock.
func NewService() *Service {
	return &Service{
		sessions: make(map[string]*Session),
		now:      time.Now,
	}
}

// newServiceWithClock builds a service with an injectable clock, used in tests.
func newServiceWithClock(now func() time.Time) *Service {
	return &Service{sessions: make(map[string]*Session), now: now}
}

// Register adds a new session. Reusing an id or supplying an invalid session
// is rejected.
func (s *Service) Register(sess *Session) error {
	if sess == nil || sess.ID == "" || sess.MaxReplyDelay <= 0 ||
		sess.StartTime.IsZero() || sess.Deadline.IsZero() ||
		!sess.Deadline.After(sess.StartTime) {
		return ErrInvalidSession
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessions[sess.ID]; exists {
		return ErrSessionReused
	}
	sess.closed = false
	sess.hasIndex = false
	s.sessions[sess.ID] = sess
	return nil
}

// Close terminates a session so that any later replayed result is rejected.
func (s *Service) Close(id string) error {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	s.mu.RUnlock()
	if !ok {
		return ErrSessionUnknown
	}

	sess.mu.Lock()
	sess.closed = true
	sess.mu.Unlock()
	return nil
}

// RangingResult is one DS-TWR measurement submitted for admission.
//
// Ra/Rb are the round-trip times and Da/Db the reply times of the two
// participants, expressed in the same duration unit. ReplyDelay is the
// observed response latency of the remote UWB node; inflating it is the
// distance-faking attack this package must reject.
type RangingResult struct {
	SessionID    string
	STSIndex     uint64
	STSQualityOK bool
	Timestamp    time.Time
	ReplyDelay   time.Duration
	Ra           time.Duration
	Rb           time.Duration
	Da           time.Duration
	Db           time.Duration
}

// Decision is the outcome of an accepted ranging result.
type Decision struct {
	SessionID string
	STSIndex  uint64
	// TimeOfFlight is the DS-TWR propagation time.
	TimeOfFlight time.Duration
	// DistanceMeters is TimeOfFlight multiplied by the speed of light.
	DistanceMeters float64
	AcceptedAt     time.Time
}

// Admit validates a ranging result against its session and, on success,
// records the STS index as the new anti-replay high-water mark.
func (s *Service) Admit(r RangingResult) (*Decision, error) {
	s.mu.RLock()
	sess, ok := s.sessions[r.SessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrSessionUnknown
	}
	return sess.admit(r, s.now())
}

func (sess *Session) admit(r RangingResult, now time.Time) (*Decision, error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if sess.closed {
		return nil, ErrSessionClosed
	}

	// Anti-replay: the STS index must be strictly increasing. A duplicate is
	// a replay; a smaller value is an index rollback. Both are rejected before
	// any expensive work.
	if sess.hasIndex && r.STSIndex <= sess.lastSTS {
		return nil, ErrStaleIndex
	}

	if !r.STSQualityOK {
		return nil, ErrQualityFailed
	}

	if err := sess.validateTimestamp(r.Timestamp, now); err != nil {
		return nil, err
	}

	if err := validateTiming(r, sess.MaxReplyDelay); err != nil {
		return nil, err
	}

	tof, err := dsTWRTimeOfFlight(r.Ra, r.Rb, r.Da, r.Db)
	if err != nil {
		return nil, err
	}
	if tof < 0 {
		return nil, ErrNegativeDistance
	}

	// Commit the high-water mark only after every check passes.
	sess.lastSTS = r.STSIndex
	sess.hasIndex = true

	return &Decision{
		SessionID:      sess.ID,
		STSIndex:       r.STSIndex,
		TimeOfFlight:   tof,
		DistanceMeters: float64(tof.Nanoseconds()) * SpeedOfLightMetersPerNanosecond,
		AcceptedAt:     now,
	}, nil
}

func (sess *Session) validateTimestamp(ts, now time.Time) error {
	if ts.IsZero() {
		return ErrInvalidTimestamp
	}
	if ts.Before(sess.StartTime) {
		return ErrInvalidTimestamp
	}
	if !sess.Deadline.IsZero() && ts.After(sess.Deadline) {
		return ErrInvalidTimestamp
	}
	// A timestamp far in the future implies a forged or unsynchronized device.
	if ts.After(now.Add(futureSkew)) {
		return ErrInvalidTimestamp
	}
	return nil
}

func validateTiming(r RangingResult, maxReply time.Duration) error {
	if r.ReplyDelay < 0 || r.Ra < 0 || r.Rb < 0 || r.Da < 0 || r.Db < 0 {
		return ErrInvalidTiming
	}
	// The declared bound applies to every observable reply latency, including
	// the DS-TWR reply slots Da/Db that an attacker can stretch.
	if r.ReplyDelay > maxReply || r.Da > maxReply || r.Db > maxReply {
		return ErrReplyDelay
	}
	return nil
}

// dsTWRTimeOfFlight implements the double-sided two-way ranging estimator:
//
//	ToF = (Ra*Rb - Da*Db) / (Ra + Rb + Da + Db)
//
// A non-positive denominator makes the estimator undefined. A negative
// numerator yields a negative ToF, which the caller rejects.
func dsTWRTimeOfFlight(ra, rb, da, db time.Duration) (time.Duration, error) {
	denom := ra + rb + da + db
	if denom <= 0 {
		return 0, ErrInvalidTiming
	}

	raF, rbF, daF, dbF := float64(ra), float64(rb), float64(da), float64(db)
	num := raF*rbF - daF*dbF
	tofSeconds := num / float64(denom)
	return time.Duration(tofSeconds), nil
}
