// Package sigcache verifies signed download URLs for a resource host.
//
// # Security model
//
// A signature binds the object digest, the audience and an absolute expiry
// instant. The cache deliberately caches only the (expensive) signature
// verification result; it never caches an authorization decision. Every
// delivery re-evaluates expiry against the full trusted time interval, so a
// cached entry can never keep a URL authorized after its deadline. The HTTP
// Age and Date headers are treated as untrusted diagnostics and never feed
// the expiry decision. When the trusted clock cannot produce a definite
// interval that lies entirely before the deadline, authorization is refused.
package sigcache

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AlgorithmHMACSHA256 is the only supported signature algorithm.
const AlgorithmHMACSHA256 = "HMAC-SHA256"

// Sentinel denial reasons. Use errors.Is to classify a returned error.
var (
	// ErrMalformedToken means the token cannot be parsed into its parts.
	ErrMalformedToken = errors.New("sigcache: malformed signed token")
	// ErrUnknownKey means no live key exists for the token key id.
	ErrUnknownKey = errors.New("sigcache: unknown or revoked signing key")
	// ErrUnsupportedAlgorithm means the token names an unsupported algorithm.
	ErrUnsupportedAlgorithm = errors.New("sigcache: unsupported signature algorithm")
	// ErrBadSignature means the HMAC does not match the canonical payload.
	ErrBadSignature = errors.New("sigcache: signature verification failed")
	// ErrAudienceMismatch means the token audience does not equal the request audience.
	ErrAudienceMismatch = errors.New("sigcache: audience mismatch")
	// ErrDigestMismatch means the token digest does not equal the requested object digest.
	ErrDigestMismatch = errors.New("sigcache: object digest mismatch")
	// ErrIndefiniteClock means the trusted clock returned an invalid interval.
	ErrIndefiniteClock = errors.New("sigcache: trusted clock interval is indefinite")
	// ErrExpired means some possible current instant is at or past the deadline.
	ErrExpired = errors.New("sigcache: signed URL has expired")
)

// Denial is the error returned for every refused authorization. It carries
// the classified reason plus untrusted diagnostics collected from the request.
type Denial struct {
	Reason      error
	Diagnostics Diagnostics
}

func (d *Denial) Error() string { return d.Reason.Error() }
func (d *Denial) Unwrap() error { return d.Reason }

// Diagnostics holds parsed Age/Date header values. These values originate
// from intermediate caches and are never trusted: they are present only to
// help operators explain a decision.
type Diagnostics struct {
	AgeHeader      string
	AgeSeconds     int64
	AgeParseError  error
	DateHeader     string
	DateParsed     time.Time
	DateParseError error
	// CacheHit reports whether the signature result was served from cache.
	CacheHit bool
}

// Request is the material a resource host presents for each delivery.
type Request struct {
	// Token is the raw token taken from the signed URL.
	Token string
	// Audience is the host/audience the caller believes it is serving.
	Audience string
	// ObjectDigest is the digest of the object about to be delivered.
	ObjectDigest string
	// AgeHeader and DateHeader are the raw, untrusted HTTP headers.
	AgeHeader  string
	DateHeader string
}

// Receipt is returned on a successful authorization.
type Receipt struct {
	KeyID    string
	Audience string
	Digest   string
	// Expiry is the absolute deadline encoded in the signature.
	Expiry time.Time
	// CacheHit reports whether the signature result was reused from cache.
	CacheHit bool
}

// FromHTTPRequest builds a Request from an HTTP delivery, copying the raw
// Age and Date headers for diagnostic use only.
func FromHTTPRequest(token, audience, objectDigest string, h http.Header) Request {
	return Request{
		Token:        token,
		Audience:     audience,
		ObjectDigest: objectDigest,
		AgeHeader:    h.Get("Age"),
		DateHeader:   h.Get("Date"),
	}
}

// Sign produces a token binding digest, audience and an absolute expiry with
// HMAC-SHA256 under key. The expiry is encoded as an absolute Unix instant,
// never as a duration, so intermediate caches cannot extend it via Age.
func Sign(keyID string, key []byte, audience, digest string, expiry time.Time) (string, error) {
	if keyID == "" {
		return "", errors.New("sigcache: empty key id")
	}
	if len(key) == 0 {
		return "", errors.New("sigcache: empty HMAC key")
	}
	if audience == "" {
		return "", errors.New("sigcache: empty audience")
	}
	if digest == "" {
		return "", errors.New("sigcache: empty object digest")
	}
	if strings.ContainsAny(audience, "\n\r") || strings.ContainsAny(digest, "\n\r") ||
		strings.ContainsAny(keyID, "\n\r") {
		return "", errors.New("sigcache: field contains newline")
	}
	canonical := canonicalPayload(keyID, audience, digest, expiry.Unix())
	mac := hmac.New(sha256.New, key)
	mac.Write(canonical)
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString(canonical) + "." + sig, nil
}

func canonicalPayload(keyID, audience, digest string, expiryUnix int64) []byte {
	return []byte(fmt.Sprintf(
		"%s\n%s\n%d\n%s\n%s",
		AlgorithmHMACSHA256,
		keyID,
		expiryUnix,
		audience,
		digest,
	))
}

type parsedToken struct {
	algorithm string
	keyID     string
	audience  string
	digest    string
	expiry    time.Time
	canonical []byte
	signature []byte
}

func parseToken(token string) (*parsedToken, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, ErrMalformedToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: payload: %v", ErrMalformedToken, err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: signature: %v", ErrMalformedToken, err)
	}
	lines := strings.Split(string(payload), "\n")
	if len(lines) != 5 {
		return nil, ErrMalformedToken
	}
	expiryUnix, err := strconv.ParseInt(lines[2], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%w: expiry: %v", ErrMalformedToken, err)
	}
	return &parsedToken{
		algorithm: lines[0],
		keyID:     lines[1],
		audience:  lines[3],
		digest:    lines[4],
		expiry:    time.Unix(expiryUnix, 0).UTC(),
		canonical: payload,
		signature: signature,
	}, nil
}

// TrustedClock returns the current time as a trusted uncertainty interval.
// The implementation must guarantee that the true current instant lies
// between Earliest and Latest (inclusive bounds are acceptable).
type TrustedClock interface {
	NowInterval() (earliest, latest time.Time, err error)
}

// SystemClock derives an interval from the local wall clock with an explicit
// uncertainty budget. A zero budget means the host trusts the local clock
// exactly; hosts without a synchronized clock should pass a generous budget.
type SystemClock struct {
	Uncertainty time.Duration
}

// NowInterval implements TrustedClock.
func (c SystemClock) NowInterval() (time.Time, time.Time, error) {
	if c.Uncertainty < 0 {
		return time.Time{}, time.Time{}, ErrIndefiniteClock
	}
	now := time.Now().UTC()
	return now.Add(-c.Uncertainty), now.Add(c.Uncertainty), nil
}

type signingKey struct {
	secret  []byte
	version uint64
}

// Keyring holds live HMAC keys. Revoking a key removes it and bumps its
// version so any cached verification result is provably stale.
type Keyring struct {
	mu      sync.RWMutex
	keys    map[string]signingKey
	version uint64
}

// NewKeyring creates an empty keyring.
func NewKeyring() *Keyring {
	return &Keyring{keys: make(map[string]signingKey)}
}

// Add inserts or replaces a key and advances the global version, which
// invalidates every cached result derived from a previous key generation.
func (k *Keyring) Add(keyID string, secret []byte) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.version++
	k.keys[keyID] = signingKey{secret: append([]byte(nil), secret...), version: k.version}
}

// Revoke removes a key. Unknown ids are harmless. The verifier reacts to the
// version bump and sweeps its cache so prior results cannot be reused.
func (k *Keyring) Revoke(keyID string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.keys, keyID)
	k.version++
}

func (k *Keyring) lookup(keyID string) (signingKey, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	entry, ok := k.keys[keyID]
	return entry, ok
}

func (k *Keyring) currentVersion() uint64 {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.version
}

// verifiedEntry stores only the outcome of the signature computation. It
// intentionally contains no authorization or expiry decision.
type verifiedEntry struct {
	keyVersion uint64
	algorithm  string
	keyID      string
	audience   string
	digest     string
	expiry     time.Time
}

// Config configures a Verifier.
type Config struct {
	Keyring *Keyring
	Clock   TrustedClock
	// MaxEntries bounds the verification cache; zero means 1024.
	MaxEntries int
}

// Verifier validates signed URLs with a cache that only memoizes signature
// verification. It is safe for concurrent use.
type Verifier struct {
	keyring    *Keyring
	clock      TrustedClock
	maxEntries int

	mu    sync.Mutex
	cache map[string]verifiedEntry
}

// New builds a Verifier.
func New(cfg Config) (*Verifier, error) {
	if cfg.Keyring == nil {
		return nil, errors.New("sigcache: nil keyring")
	}
	if cfg.Clock == nil {
		return nil, errors.New("sigcache: nil trusted clock")
	}
	maxEntries := cfg.MaxEntries
	if maxEntries <= 0 {
		maxEntries = 1024
	}
	return &Verifier{
		keyring:    cfg.Keyring,
		clock:      cfg.Clock,
		maxEntries: maxEntries,
		cache:      make(map[string]verifiedEntry),
	}, nil
}

// RevokeKey removes a key from the keyring and sweeps every cached result,
// so signatures verified before revocation cannot be reused.
func (v *Verifier) RevokeKey(keyID string) {
	v.keyring.Revoke(keyID)
	v.mu.Lock()
	v.cache = make(map[string]verifiedEntry)
	v.mu.Unlock()
}

// CacheLen reports the number of memoized signature results.
func (v *Verifier) CacheLen() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.cache)
}

// Authorize verifies a delivery request. A non-nil error is always a
// *Denial whose Reason is one of the sentinel errors.
func (v *Verifier) Authorize(req Request) (*Receipt, error) {
	diag := Diagnostics{AgeHeader: req.AgeHeader, DateHeader: req.DateHeader}
	diag.AgeSeconds, diag.AgeParseError = parseAge(req.AgeHeader)
	diag.DateParsed, diag.DateParseError = parseHTTPDate(req.DateHeader)

	deny := func(reason error) (*Receipt, error) {
		return nil, &Denial{Reason: reason, Diagnostics: diag}
	}

	tok, err := parseToken(req.Token)
	if err != nil {
		return deny(err)
	}

	cacheKey := cacheKeyOf(req.Token)
	entry, cacheHit, reason := v.ensureVerified(cacheKey, tok)
	diag.CacheHit = cacheHit
	if entry == (verifiedEntry{}) {
		return deny(reason)
	}

	// Binding checks are re-done on every delivery; the cache never certifies
	// that a signature is valid for this particular audience or object.
	if subtle.ConstantTimeCompare([]byte(entry.audience), []byte(req.Audience)) != 1 {
		return deny(ErrAudienceMismatch)
	}
	if subtle.ConstantTimeCompare([]byte(entry.digest), []byte(req.ObjectDigest)) != 1 {
		return deny(ErrDigestMismatch)
	}

	// Expiry is evaluated fresh against the full trusted interval. For the
	// URL to remain authorized, every possible current instant must be
	// strictly before the absolute deadline. A cached signature cannot
	// influence this check in any way.
	earliest, latest, err := v.clock.NowInterval()
	if err != nil {
		return deny(fmt.Errorf("%w: %v", ErrIndefiniteClock, err))
	}
	if earliest.IsZero() || latest.IsZero() || latest.Before(earliest) {
		return deny(ErrIndefiniteClock)
	}
	if !latest.Before(entry.expiry) {
		return deny(ErrExpired)
	}

	return &Receipt{
		KeyID:    entry.keyID,
		Audience: entry.audience,
		Digest:   entry.digest,
		Expiry:   entry.expiry,
		CacheHit: cacheHit,
	}, nil
}

// ensureVerified returns the memoized signature result after re-validating
// the key generation, or computes and stores a fresh one.
func (v *Verifier) ensureVerified(cacheKey string, tok *parsedToken) (verifiedEntry, bool, error) {
	currentVersion := v.keyring.currentVersion()

	v.mu.Lock()
	entry, ok := v.cache[cacheKey]
	v.mu.Unlock()
	if ok && entry.keyVersion == currentVersion {
		return entry, true, nil
	}

	computed, reason := v.computeVerification(tok)
	if reason != nil {
		return verifiedEntry{}, false, reason
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if existing, ok := v.cache[cacheKey]; ok && existing.keyVersion == v.keyring.currentVersion() {
		return existing, true, nil
	}
	if len(v.cache) >= v.maxEntries {
		v.evictOneLocked()
	}
	v.cache[cacheKey] = computed
	return computed, false, nil
}

// computeVerification performs the real HMAC check against the live key.
func (v *Verifier) computeVerification(tok *parsedToken) (verifiedEntry, error) {
	if tok.algorithm != AlgorithmHMACSHA256 {
		return verifiedEntry{}, ErrUnsupportedAlgorithm
	}
	key, ok := v.keyring.lookup(tok.keyID)
	if !ok {
		return verifiedEntry{}, ErrUnknownKey
	}
	mac := hmac.New(sha256.New, key.secret)
	mac.Write(tok.canonical)
	expected := mac.Sum(nil)
	if subtle.ConstantTimeCompare(expected, tok.signature) != 1 {
		return verifiedEntry{}, ErrBadSignature
	}
	return verifiedEntry{
		keyVersion: key.version,
		algorithm:  tok.algorithm,
		keyID:      tok.keyID,
		audience:   tok.audience,
		digest:     tok.digest,
		expiry:     tok.expiry,
	}, nil
}

// evictOneLocked removes an arbitrary entry when the cache is full. The
// cache only stores cheap recomputable results, so no LRU policy is needed.
func (v *Verifier) evictOneLocked() {
	for key := range v.cache {
		delete(v.cache, key)
		return
	}
}

func cacheKeyOf(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return string(sum[:])
}

// parseAge parses the untrusted Age header (RFC 9111 delta-seconds).
func parseAge(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, err
	}
	if seconds < 0 {
		return seconds, errors.New("sigcache: Age header must be non-negative delta-seconds")
	}
	return seconds, nil
}

// parseHTTPDate parses the untrusted Date header (RFC 7231 IMF-fixdate).
func parseHTTPDate(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC1123, strings.TrimSpace(raw))
}
