// Package ikecookie implements IKE_SA_INIT admission control for an IKEv2
// responder (RFC 7296 section 2.6) against spoofed-source flooding.
//
// While the number of half-open IKE SAs is below the configured threshold the
// responder processes every IKE_SA_INIT normally. Once the threshold is
// reached, requests that do not carry a COOKIE notification are answered with
// a COOKIE notification only: the responder performs no Diffie-Hellman
// computation and allocates no half-open state for them. A retransmitted
// IKE_SA_INIT carrying a COOKIE that validates is processed, provided the
// initiator nonce Ni, source address and initiator SPI (SPIi) are identical to
// the values the cookie was issued for.
//
// A cookie is HMAC-SHA-256 keyed by a rotating server secret over the framed
// tuple (Ni, source IP, SPIi). On every rotation the previous secret is kept
// for one additional period so in-flight cookies stay valid; only the current
// secret is used to issue new cookies.
package ikecookie

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"time"
)

// Default tuning knobs.
const (
	// DefaultHalfOpenLimit is the threshold at which cookie challenges begin.
	DefaultHalfOpenLimit = 200
	// DefaultHalfOpenTTL bounds the lifetime of an unresolved half-open entry.
	DefaultHalfOpenTTL = 30 * time.Second
	// DefaultSweepInterval is how often expired half-open entries are reaped.
	DefaultSweepInterval = 5 * time.Second
	// DefaultRotationInterval is how often the cookie secret rotates.
	DefaultRotationInterval = 5 * time.Minute
	// secretSize is the byte length of a fresh HMAC secret.
	secretSize = 32
)

// Decision is the admission verdict for an IKE_SA_INIT request.
type Decision int

const (
	// DecisionInvalid means the request itself was malformed or unprocessable
	// (for example a nil source address) and must be dropped without a reply.
	DecisionInvalid Decision = iota
	// DecisionAllow means the request may be processed, including the DH
	// exchange. A half-open entry has been reserved; call Release when the SA
	// completes or the exchange is abandoned.
	DecisionAllow
	// DecisionCookie means the responder must answer with a COOKIE
	// notification only and must not run DH or keep half-open state.
	DecisionCookie
	// DecisionDrop means a COOKIE was presented but failed validation. The
	// request is silently discarded per RFC 7296.
	DecisionDrop
)

// SPIi is the 8-byte IKEv2 initiator security parameter index.
type SPIi [8]byte

// InitRequest is the IKE_SA_INIT information the gate needs. The gateway is
// responsible for parsing the wire message and extracting these fields.
type InitRequest struct {
	// SourceIP is the packet source address.
	SourceIP netip.Addr
	// SPIi is the initiator SPI from the IKE header.
	SPIi SPIi
	// Ni is the raw initiator nonce payload.
	Ni []byte
	// Cookie is the COOKIE notification payload, empty when absent.
	Cookie []byte
}

// AdmitResult is the outcome of Admit.
type AdmitResult struct {
	// Decision is the verdict.
	Decision Decision
	// Cookie carries the challenge value when Decision == DecisionCookie.
	Cookie []byte
	// Release removes the half-open entry reserved by an Allow decision. It is
	// nil for every other decision. Calling it is safe and idempotent.
	Release func()
}

// Option customizes an Admitter.
type Option func(*Admitter)

// WithHalfOpenLimit sets the half-open threshold (must be positive).
func WithHalfOpenLimit(n int) Option {
	return func(a *Admitter) { a.limit = n }
}

// WithHalfOpenTTL sets how long an unreleased half-open entry lives.
func WithHalfOpenTTL(d time.Duration) Option {
	return func(a *Admitter) { a.halfOpenTTL = d }
}

// WithSweepInterval sets the half-open reaper interval.
func WithSweepInterval(d time.Duration) Option {
	return func(a *Admitter) { a.sweepInterval = d }
}

// WithRotationInterval sets the secret rotation period. A non-positive value
// disables automatic rotation; secrets can still be rotated manually.
func WithRotationInterval(d time.Duration) Option {
	return func(a *Admitter) { a.rotationInterval = d }
}

// Admitter gates IKE_SA_INIT processing. It is safe for concurrent use.
type Admitter struct {
	mu sync.Mutex

	limit            int
	halfOpenTTL      time.Duration
	sweepInterval    time.Duration
	rotationInterval time.Duration

	currentSecret []byte
	prevSecret    []byte
	rotatedAt     time.Time

	table map[string]*halfEntry

	stop    chan struct{}
	stopped chan struct{}
	now     func() time.Time
}

type halfEntry struct {
	expiresAt time.Time
}

// NewAdmitter builds an Admitter with sensible defaults and starts its
// background janitor (half-open reaping and optional secret rotation). Call
// Close to release it.
func NewAdmitter(opts ...Option) (*Admitter, error) {
	initial, err := newSecret()
	if err != nil {
		return nil, err
	}
	a := &Admitter{
		limit:            DefaultHalfOpenLimit,
		halfOpenTTL:      DefaultHalfOpenTTL,
		sweepInterval:    DefaultSweepInterval,
		rotationInterval: DefaultRotationInterval,
		currentSecret:    initial,
		rotatedAt:        time.Now(),
		table:            make(map[string]*halfEntry),
		stop:             make(chan struct{}),
		stopped:          make(chan struct{}),
		now:              time.Now,
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.limit <= 0 {
		return nil, errors.New("ikecookie: half-open limit must be positive")
	}
	if a.halfOpenTTL <= 0 {
		return nil, errors.New("ikecookie: half-open TTL must be positive")
	}
	if a.sweepInterval <= 0 {
		return nil, errors.New("ikecookie: sweep interval must be positive")
	}

	go a.runJanitor()
	return a, nil
}

// runJanitor owns all timed work on a single goroutine: secret rotation and
// reaping of expired half-open entries.
func (a *Admitter) runJanitor() {
	defer close(a.stopped)

	var rotate <-chan time.Time
	if a.rotationInterval > 0 {
		ticker := time.NewTicker(a.rotationInterval)
		rotate = ticker.C
		defer ticker.Stop()
	}
	sweeper := time.NewTicker(a.sweepInterval)
	defer sweeper.Stop()

	for {
		select {
		case <-a.stop:
			return
		case <-rotate:
			_ = a.RotateSecret()
		case <-sweeper.C:
			a.sweep()
		}
	}
}

// Close stops the background janitor. It is safe to call once.
func (a *Admitter) Close() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	<-a.stopped
}

// Admit applies the IKE_SA_INIT admission policy to one request.
func (a *Admitter) Admit(req InitRequest) AdmitResult {
	if !req.SourceIP.IsValid() {
		return AdmitResult{Decision: DecisionInvalid}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// A request carrying a cookie is never answered with another challenge:
	// either it validates and is processed, or it is discarded.
	if len(req.Cookie) > 0 {
		if !a.cookieValidLocked(req) {
			return AdmitResult{Decision: DecisionDrop}
		}
		return a.allowLocked(req)
	}

	if len(a.table) >= a.limit {
		return AdmitResult{
			Decision: DecisionCookie,
			Cookie:   a.computeCookieLocked(a.currentSecret, req),
		}
	}

	return a.allowLocked(req)
}

// allowLocked reserves a half-open slot and returns an Allow verdict with a
// Release closure. A retransmission keyed identically refreshes the entry.
func (a *Admitter) allowLocked(req InitRequest) AdmitResult {
	key := halfKey(req.SourceIP, req.SPIi)
	a.table[key] = &halfEntry{expiresAt: a.now().Add(a.halfOpenTTL)}

	var once sync.Once
	release := func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		once.Do(func() { delete(a.table, key) })
	}
	return AdmitResult{Decision: DecisionAllow, Release: release}
}

// cookieValidLocked reports whether the presented cookie matches either the
// current secret or the previous one, using constant-time comparison.
func (a *Admitter) cookieValidLocked(req InitRequest) bool {
	if hmac.Equal(req.Cookie, a.computeCookieLocked(a.currentSecret, req)) {
		return true
	}
	if len(a.prevSecret) > 0 &&
		hmac.Equal(req.Cookie, a.computeCookieLocked(a.prevSecret, req)) {
		return true
	}
	return false
}

// computeCookieLocked computes HMAC-SHA-256(secret, Ni || sourceIP || SPIi).
// The tuple is length-framed so distinct inputs cannot collide.
func (a *Admitter) computeCookieLocked(secret []byte, req InitRequest) []byte {
	ip := req.SourceIP
	var version byte
	var rawIP []byte
	if ip.Is4() {
		version = 4
		b := ip.As4()
		rawIP = b[:]
	} else {
		version = 6
		b := ip.As16()
		rawIP = b[:]
	}

	mac := hmac.New(sha256.New, secret)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(req.Ni)))
	mac.Write(lenBuf[:])
	mac.Write(req.Ni)
	mac.Write([]byte{version})
	mac.Write(rawIP)
	mac.Write(req.SPIi[:])
	return mac.Sum(nil)
}

// RotateSecret generates a fresh signing secret and demotes the current one to
// "previous", where it remains valid for one additional rotation period.
func (a *Admitter) RotateSecret() error {
	fresh, err := newSecret()
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prevSecret = a.currentSecret
	a.currentSecret = fresh
	a.rotatedAt = a.now()
	return nil
}

// HalfOpen returns the current number of reserved half-open entries.
func (a *Admitter) HalfOpen() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.table)
}

// sweep removes half-open entries whose TTL has elapsed.
func (a *Admitter) sweep() {
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	for key, entry := range a.table {
		if !entry.expiresAt.After(now) {
			delete(a.table, key)
		}
	}
}

// halfKey builds the canonical half-open table key from the source address and
// initiator SPI.
func halfKey(ip netip.Addr, spii SPIi) string {
	var raw []byte
	if ip.Is4() {
		b := ip.As4()
		raw = b[:]
	} else {
		b := ip.As16()
		raw = b[:]
	}
	return string(raw) + string(spii[:])
}

// newSecret returns 32 cryptographically random bytes.
func newSecret() ([]byte, error) {
	secret := make([]byte, secretSize)
	if _, err := rand.Read(secret); err != nil {
		return nil, err
	}
	return secret, nil
}
