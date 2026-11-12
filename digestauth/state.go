// Package digestauth maintains server-side state for HTTP Digest access
// authentication on a concurrent gateway.
//
// A nonce is sealed with an HMAC so that its expiry and the service Policy it
// was issued for cannot be tampered with, while the State keeps the live
// request counters. Counters are tracked per (nonce, cnonce) pair: a sliding
// bitmap window accepts out-of-order nc values but rejects duplicates, and the
// signature Verifier is always invoked before any counter is consumed, so a
// forged response can never burn a request count.
package digestauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// Sentinel errors returned by Authenticate. Callers should use errors.Is.
var (
	// ErrInvalidNonce means the nonce is malformed or its HMAC does not match.
	ErrInvalidNonce = errors.New("digestauth: malformed or forged nonce")
	// ErrUnknownNonce means the nonce is well-formed but was never issued by
	// this State or has already been forgotten (for example after a restart).
	ErrUnknownNonce = errors.New("digestauth: nonce is not known to this service")
	// ErrExpiredNonce means the nonce has passed its sealed validity period.
	ErrExpiredNonce = errors.New("digestauth: nonce has expired")
	// ErrPolicyMismatch means the nonce was bound to a different service Policy.
	ErrPolicyMismatch = errors.New("digestauth: nonce is not bound to the presented service policy")
	// ErrMissingCNonce means the request did not carry a client nonce.
	ErrMissingCNonce = errors.New("digestauth: cnonce is required for quality-of-protection")
	// ErrInvalidCount means nc is zero or otherwise not a legal counter value.
	ErrInvalidCount = errors.New("digestauth: request counter nc is invalid")
	// ErrReplay means the nc value was already registered for the same
	// (nonce, cnonce) pair and is therefore a replay.
	ErrReplay = errors.New("digestauth: duplicated request counter")
	// ErrStaleNonce means nc is older than the accepted out-of-order window.
	ErrStaleNonce = errors.New("digestauth: request counter falls outside the out-of-order window")
	// ErrCounterWrapped means the counter reached MaxCount (or the 32-bit
	// limit); the client must obtain a fresh nonce.
	ErrCounterWrapped = errors.New("digestauth: nonce counter wrapped and the nonce must be replaced")
	// ErrBadSignature wraps a Verifier rejection. No counter is consumed when
	// this error is returned.
	ErrBadSignature = errors.New("digestauth: response signature verification failed")
)

const (
	defaultWindow    = 64
	defaultTTL       = 5 * time.Minute
	defaultSweep     = time.Minute
	maxUint32Counter = math.MaxUint32
)

// Policy describes the service parameters a nonce is cryptographically bound
// to. The same normalized Policy must be presented when authenticating.
type Policy struct {
	// Realm is the protection space (RFC 2617 realm).
	Realm string
	// QOP is "auth" or "auth-int". Empty normalizes to "auth".
	QOP string
	// Algorithm is the digest algorithm label, e.g. "MD5" or "SHA-256".
	Algorithm string
	// MaxCount caps nc for a nonce. Zero means the full uint32 range. Once nc
	// reaches MaxCount every further request fails with ErrCounterWrapped.
	MaxCount uint32
	// Window is the number of most-recent nc values retained per cnonce for
	// out-of-order delivery. Zero defaults to 64.
	Window int
	// TTL is the nonce validity period. Zero defaults to five minutes.
	TTL time.Duration
}

func (p Policy) normalized() Policy {
	if p.QOP == "" {
		p.QOP = "auth"
	}
	if p.Algorithm == "" {
		p.Algorithm = "MD5"
	}
	if p.MaxCount == 0 {
		p.MaxCount = maxUint32Counter
	}
	if p.Window <= 0 {
		p.Window = defaultWindow
	}
	if p.TTL <= 0 {
		p.TTL = defaultTTL
	}
	return p
}

// Request carries the fields of one authenticated gateway request.
type Request struct {
	Policy   Policy
	Username string
	Nonce    string
	CNonce   string
	// NC is the parsed nonce count (see ParseNC). It must be non-zero.
	NC uint32
	// Method is the HTTP request method.
	Method string
	// URI is the request target (digest URI parameter).
	URI string
	// BodySHA is the entity-body digest, required/used for qop=auth-int.
	BodySHA string
	// Response is the client digest response handed to the Verifier.
	Response string
}

// Evidence is handed to the Verifier callback. The callback is expected to
// confirm username, realm, method, target (URI), body digest and the response
// signature against its credential store.
type Evidence struct {
	Realm     string
	Username  string
	Algorithm string
	QOP       string
	Nonce     string
	CNonce    string
	NC        uint32
	Method    string
	URI       string
	BodySHA   string
	Response  string
}

// Verifier checks the response signature. Return nil to accept the signature;
// any non-nil error rejects the request without touching the counter state.
type Verifier interface {
	Verify(ctx context.Context, e Evidence) error
}

// VerifierFunc adapts a plain function to the Verifier interface.
type VerifierFunc func(ctx context.Context, e Evidence) error

// Verify implements Verifier.
func (f VerifierFunc) Verify(ctx context.Context, e Evidence) error {
	return f(ctx, e)
}

// Option customizes a State.
type Option func(*State)

// WithClock injects a clock, mainly useful for deterministic tests.
func WithClock(clock func() time.Time) Option {
	return func(s *State) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithSweepInterval sets the background expiry sweep interval. A zero or
// negative interval disables the background goroutine (lazy expiry checks and
// explicit Sweep calls still apply).
func WithSweepInterval(d time.Duration) Option {
	return func(s *State) {
		s.sweepInterval = d
	}
}

type nonceEntry struct {
	policy    Policy
	expires   time.Time
	windowCap int
	maxCount  uint32
	sessions  map[string]*cnonceSession
	lastUsed  time.Time
}

type cnonceSession struct {
	window *replayWindow
}

// State holds the live nonce/counter state for a gateway instance. It is safe
// for concurrent use.
type State struct {
	mu            sync.Mutex
	nonces        map[string]*nonceEntry
	secret        []byte
	clock         func() time.Time
	sweepInterval time.Duration
	stopOnce      sync.Once
	stopCh        chan struct{}
	doneCh        chan struct{}
}

// NewState creates a State. secret must be a persistent server-side HMAC key
// shared by every gateway replica that needs to validate the same nonces.
func NewState(secret []byte, opts ...Option) *State {
	key := append([]byte(nil), secret...)
	s := &State{
		nonces:        make(map[string]*nonceEntry),
		secret:        key,
		clock:         time.Now,
		sweepInterval: defaultSweep,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.sweepInterval > 0 {
		s.stopCh = make(chan struct{})
		s.doneCh = make(chan struct{})
		go s.sweepLoop()
	}
	return s
}

// Stop terminates the background sweep goroutine, if any.
func (s *State) Stop() {
	s.stopOnce.Do(func() {
		if s.stopCh != nil {
			close(s.stopCh)
			<-s.doneCh
		}
	})
}

// Issue mints a nonce bound to p (including its TTL) and registers its state.
func (s *State) Issue(p Policy) (string, error) {
	policy := p.normalized()
	now := s.clock()
	expires := now.Add(policy.TTL)
	nonce, err := sealNonce(s.secret, policyTag(s.secret, policy), expires)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	s.nonces[nonce] = &nonceEntry{
		policy:    policy,
		expires:   expires,
		windowCap: policy.Window,
		maxCount:  policy.MaxCount,
		sessions:  make(map[string]*cnonceSession),
		lastUsed:  now,
	}
	s.mu.Unlock()
	return nonce, nil
}

// Authenticate validates one request. It first verifies the nonce envelope and
// its policy/expiry binding, then runs the signature Verifier, and only after
// the signature is accepted does it atomically register the nc value for the
// (nonce, cnonce) pair.
func (s *State) Authenticate(ctx context.Context, verifier Verifier, req Request) error {
	if verifier == nil {
		return errors.New("digestauth: verifier is required")
	}
	if req.Nonce == "" {
		return ErrInvalidNonce
	}
	if req.CNonce == "" {
		return ErrMissingCNonce
	}
	if req.NC == 0 {
		return ErrInvalidCount
	}

	policy := req.Policy.normalized()

	expires, sealedTag, err := openNonce(s.secret, req.Nonce)
	if err != nil {
		return ErrInvalidNonce
	}
	now := s.clock()
	wantTag := policyTag(s.secret, policy)
	if subtle.ConstantTimeCompare(sealedTag[:], wantTag[:]) != 1 {
		return ErrPolicyMismatch
	}

	// Liveness check before the expiry comparison and the (potentially slow)
	// verifier. This also lazily forgets entries whose sealed TTL elapsed.
	if err := s.checkLive(req.Nonce, now); err != nil {
		return err
	}
	if !now.Before(expires) {
		return ErrExpiredNonce
	}

	evidence := Evidence{
		Realm:     policy.Realm,
		Username:  req.Username,
		Algorithm: policy.Algorithm,
		QOP:       policy.QOP,
		Nonce:     req.Nonce,
		CNonce:    req.CNonce,
		NC:        req.NC,
		Method:    req.Method,
		URI:       req.URI,
		BodySHA:   req.BodySHA,
		Response:  req.Response,
	}
	// The verifier runs outside the state lock: invalid signatures never
	// create a session or consume an nc value.
	if err := verifier.Verify(ctx, evidence); err != nil {
		return fmt.Errorf("%w: %v", ErrBadSignature, err)
	}

	return s.register(req.Nonce, req.CNonce, req.NC, now)
}

// checkLive confirms the nonce state still exists and has not expired. Expired
// entries are removed opportunistically.
func (s *State) checkLive(nonce string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.nonces[nonce]
	if !ok {
		return ErrUnknownNonce
	}
	if !now.Before(entry.expires) {
		delete(s.nonces, nonce)
		return ErrExpiredNonce
	}
	return nil
}

// register atomically records nc for the (nonce, cnonce) pair. The nonce is
// re-checked because a sweep could have expired it while the verifier ran.
func (s *State) register(nonce, cnonce string, nc uint32, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.nonces[nonce]
	if !ok {
		return ErrUnknownNonce
	}
	if !now.Before(entry.expires) {
		delete(s.nonces, nonce)
		return ErrExpiredNonce
	}

	session, ok := entry.sessions[cnonce]
	if !ok {
		session = &cnonceSession{window: newReplayWindow(entry.windowCap)}
		entry.sessions[cnonce] = session
	}
	if err := session.window.observe(nc, entry.maxCount); err != nil {
		return err
	}
	entry.lastUsed = now
	return nil
}

// Sweep removes expired nonce state.
func (s *State) Sweep() {
	now := s.clock()
	s.mu.Lock()
	for nonce, entry := range s.nonces {
		if !now.Before(entry.expires) {
			delete(s.nonces, nonce)
		}
	}
	s.mu.Unlock()
}

func (s *State) sweepLoop() {
	ticker := time.NewTicker(s.sweepInterval)
	defer ticker.Stop()
	defer close(s.doneCh)
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.Sweep()
		}
	}
}
