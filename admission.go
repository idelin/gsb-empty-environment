package netsec

import (
	"crypto/rand"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// TimingProfile controls RAKP response-latency equalisation. Every RAKP2 and
// RAKP3 response is padded so that it completes in the same bucket measured
// from the start of that step. A zero or negative Bucket disables padding.
type TimingProfile struct {
	Bucket time.Duration
}

// DefaultTimingProfile pads RAKP responses to a 100ms bucket.
var DefaultTimingProfile = TimingProfile{Bucket: 100 * time.Millisecond}

// Config configures an Admission.
type Config struct {
	// AllowedSuites lists the cipher suite numbers the caller registers. The
	// numbers must have standard definitions or appear in CustomSuites.
	AllowedSuites []uint8

	// CustomSuites supplies and registers non-standard or overridden suite
	// definitions.
	CustomSuites []Suite

	// Users maps usernames to passwords.
	Users map[string][]byte

	// Clock is used for all timing decisions and sleeps. nil uses SystemClock.
	Clock Clock

	// Timing controls response equalisation. nil uses DefaultTimingProfile. A
	// non-nil zero value disables padding.
	Timing *TimingProfile

	// Throttle controls per-source failure throttling. nil uses
	// DefaultThrottleConfig.
	Throttle *ThrottleConfig

	// Random supplies server random values, the BMC GUID and the dummy secret.
	// nil uses crypto/rand.Reader.
	Random io.Reader

	// BMCGUID is the 16-byte GUID returned in RAKP2. nil generates one.
	BMCGUID []byte
}

// Admission is an importable RMCP+ session-suite admission gate. It validates
// suite policy, throttles failing sources, and runs the RAKP authentication
// state machine with equal-shape responses for unknown users and bad
// passwords.
type Admission struct {
	mu     sync.RWMutex
	suites map[uint8]Suite
	users  map[string][]byte

	clock    Clock
	timing   TimingProfile
	throttle *FailureThrottle

	randomMu sync.Mutex
	random   io.Reader

	bmcGUID     []byte
	dummySecret []byte
}

// NewAdmission registers standard cipher suite numbers and returns an
// admission gate. Use NewAdmissionWithConfig for custom suites and users.
func NewAdmission(allowedSuites ...uint8) (*Admission, error) {
	return NewAdmissionWithConfig(Config{AllowedSuites: allowedSuites})
}

// NewAdmissionWithConfig creates an admission gate from a Config.
func NewAdmissionWithConfig(config Config) (*Admission, error) {
	a := &Admission{
		suites: make(map[uint8]Suite),
		users:  make(map[string][]byte),
		clock:  SystemClock{},
		timing: DefaultTimingProfile,
		random: rand.Reader,
	}

	if config.Clock != nil {
		a.clock = config.Clock
	}
	if config.Timing != nil {
		a.timing = *config.Timing
	}
	throttleConfig := DefaultThrottleConfig
	if config.Throttle != nil {
		throttleConfig = *config.Throttle
	}
	a.throttle = NewFailureThrottle(throttleConfig)
	if config.Random != nil {
		a.random = config.Random
	}

	for _, suite := range config.CustomSuites {
		a.suites[suite.Number] = suite
	}
	for _, number := range config.AllowedSuites {
		suite, ok := a.suites[number]
		if !ok {
			suite, ok = StandardSuite(number)
		}
		if !ok {
			return nil, fmt.Errorf("%w: %d", ErrSuiteNotRegistered, number)
		}
		a.suites[number] = suite
	}
	for username, password := range config.Users {
		a.users[username] = append([]byte(nil), password...)
	}

	var err error
	a.bmcGUID = append([]byte(nil), config.BMCGUID...)
	if len(a.bmcGUID) == 0 {
		a.bmcGUID, err = a.randomBytes(16)
		if err != nil {
			return nil, fmt.Errorf("netsec: generate BMC GUID: %w", err)
		}
	} else if len(a.bmcGUID) != 16 {
		return nil, fmt.Errorf("%w: BMC GUID must be 16 bytes", ErrInvalidRequest)
	}

	a.dummySecret, err = a.randomBytes(32)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate dummy secret: %w", err)
	}
	return a, nil
}

// RegisterSuite registers or replaces a suite definition. The policy is
// applied at admission time, so an invalid definition can be registered but
// will never be admitted.
func (a *Admission) RegisterSuite(suite Suite) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.suites[suite.Number] = suite
	return nil
}

// AllowSuite registers a standard suite number.
func (a *Admission) AllowSuite(number uint8) error {
	suite, ok := StandardSuite(number)
	if !ok {
		return fmt.Errorf("%w: %d", ErrSuiteNotRegistered, number)
	}
	return a.RegisterSuite(suite)
}

// RegisterUser adds or replaces a username/password pair.
func (a *Admission) RegisterUser(username string, password []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users[username] = append([]byte(nil), password...)
}

// AdmitSuite checks a requested suite number and returns its definition.
func (a *Admission) AdmitSuite(number uint8) (Suite, error) {
	a.mu.RLock()
	suite, registered := a.suites[number]
	a.mu.RUnlock()
	if registered {
		if err := suite.Validate(); err != nil {
			return Suite{}, err
		}
		return suite, nil
	}

	// A standard suite that the caller did not register is still checked for
	// the forbidden algorithms, so cipher suite 0 is reported as an
	// authentication-none violation rather than only as unregistered.
	if standard, ok := StandardSuite(number); ok {
		if err := standard.Validate(); err != nil {
			return Suite{}, fmt.Errorf("%w: suite %d is also forbidden: %w", ErrSuiteNotRegistered, number, err)
		}
	}
	return Suite{}, fmt.Errorf("%w: %d", ErrSuiteNotRegistered, number)
}

// CheckSuite is the error-only form of AdmitSuite.
func (a *Admission) CheckSuite(number uint8) error {
	_, err := a.AdmitSuite(number)
	return err
}

// RegisteredSuites returns registered suite numbers in ascending order.
func (a *Admission) RegisteredSuites() []uint8 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]uint8, 0, len(a.suites))
	for number := range a.suites {
		out = append(out, number)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// SourceBlocked reports whether the source is currently throttled.
func (a *Admission) SourceBlocked(source string) bool {
	return a.throttle.Blocked(normalizeSource(source), a.clock.Now())
}

// FailureCount returns the current RAKP failure count for a source.
func (a *Admission) FailureCount(source string) int {
	return a.throttle.FailureCount(normalizeSource(source), a.clock.Now())
}

func (a *Admission) lookupUser(username string) ([]byte, bool) {
	a.mu.RLock()
	password, ok := a.users[username]
	a.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return append([]byte(nil), password...), true
}

func (a *Admission) randomBytes(size int) ([]byte, error) {
	a.randomMu.Lock()
	defer a.randomMu.Unlock()
	buf := make([]byte, size)
	if _, err := io.ReadFull(a.random, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

func (a *Admission) padAuthTiming(start time.Time) {
	if a.timing.Bucket <= 0 {
		return
	}
	target := start.Add(a.timing.Bucket)
	now := a.clock.Now()
	if now.Before(target) {
		a.clock.Sleep(target.Sub(now))
	}
}
