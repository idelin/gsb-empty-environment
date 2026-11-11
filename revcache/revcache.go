// Package revcache provides an embeddable cache for certificate
// revocation status (for example OCSP responses) used by a TLS
// termination service.
//
// A cache entry is identified by three identities together: the digest
// of the issuer public key, the certificate serial number and the
// responder identity. Certificates that share a serial number but are
// issued by different CAs therefore never share a cached conclusion.
//
// Signature verification is not performed by the cache. Callers inject
// a VerifyFunc that fetches and verifies the response and returns the
// conclusion together with its thisUpdate/nextUpdate timestamps. A
// conclusion is valid in the half-open interval [thisUpdate, nextUpdate);
// an unknown status is never treated as valid and is never cached.
//
// Concurrent refreshes for the same key are coalesced: only one
// VerifyFunc call runs, while every waiting Get keeps its own context
// and may cancel independently. A revoked conclusion that is still
// within its validity interval must not be overwritten by a good
// conclusion whose thisUpdate is older.
package revcache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Status is the verified revocation conclusion for a certificate.
type Status int

const (
	// StatusUnknown means no usable conclusion was produced. Its zero
	// value is unknown, and an unknown response is never cached.
	StatusUnknown Status = iota
	// StatusGood means the responder reported the certificate as not revoked.
	StatusGood
	// StatusRevoked means the responder reported the certificate as revoked.
	StatusRevoked
)

// String returns a stable textual representation of the status.
func (s Status) String() string {
	switch s {
	case StatusGood:
		return "good"
	case StatusRevoked:
		return "revoked"
	default:
		return "unknown"
	}
}

// Key identifies a cached revocation conclusion. All three fields take
// part in key equality; callers must feed them in a fixed encoding
// (for example lower-case hex digests and decimal serial numbers).
type Key struct {
	// IssuerKeyDigest is the digest of the DER-encoded issuer public
	// key, for example the lower-case hex of its SHA-256 sum.
	IssuerKeyDigest string
	// Serial is the certificate serial number in stable textual form.
	Serial string
	// Responder identifies the responder that produced the conclusion,
	// for example its OCSP request URL. Conclusions from different
	// responders are never shared.
	Responder string
}

// Response is a verified revocation conclusion with its validity window.
type Response struct {
	// Status must be StatusGood or StatusRevoked for the response to be
	// cacheable; StatusUnknown (the zero value) is rejected.
	Status Status
	// ThisUpdate is the moment at which the responder says the status
	// became current.
	ThisUpdate time.Time
	// NextUpdate is the moment at which the responder says newer
	// information will be available. The cached conclusion expires at
	// this instant.
	NextUpdate time.Time
}

// VerifyFunc fetches and cryptographically verifies the revocation
// status for key. The cache itself never checks signatures.
//
// A returned error means verification failed and nothing is cached;
// returning Response{Status: StatusUnknown} has the same effect. The
// context passed to the callback is never tied to an individual
// waiter's context, so one waiter cancelling does not abort the shared
// refresh.
type VerifyFunc func(ctx context.Context, key Key) (Response, error)

// Clock supplies the current time. Inject a fake implementation in
// tests to drive expiry deterministically.
type Clock interface {
	Now() time.Time
}

// ClockFunc adapts a plain function to the Clock interface.
type ClockFunc func() time.Time

// Now implements Clock.
func (f ClockFunc) Now() time.Time { return f() }

var (
	// ErrUnknown is returned when the verifier produced an unknown
	// status, which must never be treated as a valid conclusion.
	ErrUnknown = errors.New("revcache: unknown revocation status cannot be cached as valid")
	// ErrInvalidResponse is returned when a verified response carries
	// no usable thisUpdate/nextUpdate validity interval.
	ErrInvalidResponse = errors.New("revcache: response has invalid thisUpdate/nextUpdate interval")
	// ErrDiscarded is returned when a refresh is rejected because a
	// still-valid revoked conclusion must not be replaced by an older
	// good conclusion. Get falls back to the retained conclusion when
	// it remains valid, so callers normally observe that instead.
	ErrDiscarded = errors.New("revcache: older good response discarded in favour of a valid revoked conclusion")
)

// Cache is a concurrency-safe revocation status cache. Its zero value
// is not usable; create one with New.
type Cache struct {
	verify VerifyFunc
	clock  Clock

	mu       sync.Mutex
	entries  map[Key]Response
	inflight map[Key]*call
}

// call represents a single in-flight refresh shared by all waiters.
type call struct {
	done chan struct{}

	resp Response
	err  error
	ok   bool
}

// New creates a Cache using verify for refreshes and clock for expiry
// decisions. A nil clock defaults to wall-clock time.
func New(verify VerifyFunc, clock Clock) *Cache {
	if verify == nil {
		panic("revcache: New requires a non-nil VerifyFunc")
	}
	if clock == nil {
		clock = ClockFunc(time.Now)
	}
	return &Cache{
		verify:   verify,
		clock:    clock,
		entries:  make(map[Key]Response),
		inflight: make(map[Key]*call),
	}
}

// Get returns the revocation conclusion for key. A valid cached
// conclusion is returned without invoking the verifier. Otherwise a
// refresh is started (or an in-flight one joined) and the caller waits
// on its own context: cancelling ctx makes this Get return
// context.Canceled without disturbing other waiters or the shared
// refresh.
//
// Unknown statuses, verification errors and malformed validity
// intervals are never cached. A still-valid revoked conclusion is
// retained when an older good conclusion arrives out of order.
func (c *Cache) Get(ctx context.Context, key Key) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	now := c.clock.Now()

	c.mu.Lock()
	if prev, ok := c.entries[key]; ok && validAt(prev, now) {
		c.mu.Unlock()
		return prev, nil
	}
	in, joined := c.inflight[key]
	if !joined {
		in = &call{done: make(chan struct{})}
		c.inflight[key] = in
		go c.refresh(key, in)
	}
	c.mu.Unlock()

	select {
	case <-ctx.Done():
		// This waiter leaves independently; the shared call keeps
		// running for every other waiter.
		return Response{}, ctx.Err()
	case <-in.done:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if in.ok {
		return in.resp, nil
	}
	// The refresh did not produce a cacheable conclusion. Keep serving
	// a previously cached one if it is still valid at this instant.
	if prev, ok := c.entries[key]; ok && validAt(prev, c.clock.Now()) {
		return prev, nil
	}
	return Response{}, in.err
}

// refresh runs the single shared verification for a key and merges the
// outcome into the cache. It always settles the associated call so that
// every joined waiter is released.
func (c *Cache) refresh(key Key, in *call) {
	// The shared refresh is detached from any individual waiter's
	// context; cancellation is enforced per waiter in Get.
	resp, err := c.verify(context.Background(), key)
	now := c.clock.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if err == nil {
		switch {
		case resp.Status != StatusGood && resp.Status != StatusRevoked:
			err = ErrUnknown
		case !wellFormed(resp):
			err = ErrInvalidResponse
		default:
			prev, havePrev := c.entries[key]
			if !shouldStore(prev, havePrev, resp, now) {
				err = ErrDiscarded
			} else {
				c.entries[key] = resp
				in.ok = true
				in.resp = resp
			}
		}
	}
	if !in.ok {
		in.err = err
	}
	delete(c.inflight, key)
	close(in.done)
}

// validAt reports whether r is a cacheable conclusion inside its
// validity window at instant now: the window is the half-open interval
// [thisUpdate, nextUpdate).
func validAt(r Response, now time.Time) bool {
	if r.Status != StatusGood && r.Status != StatusRevoked {
		return false
	}
	if r.ThisUpdate.IsZero() || r.NextUpdate.IsZero() {
		return false
	}
	if !r.NextUpdate.After(r.ThisUpdate) {
		return false
	}
	return !now.Before(r.ThisUpdate) && now.Before(r.NextUpdate)
}

// wellFormed reports whether r carries a usable validity interval and a
// cacheable status.
func wellFormed(r Response) bool {
	return (r.Status == StatusGood || r.Status == StatusRevoked) &&
		!r.ThisUpdate.IsZero() &&
		!r.NextUpdate.IsZero() &&
		r.NextUpdate.After(r.ThisUpdate)
}

// shouldStore decides whether fresh may replace the previously cached
// response. The only forbidden transition is replacing a revoked
// conclusion that is still within its validity window with a good
// conclusion whose thisUpdate is older (an out-of-order, stale reply).
func shouldStore(prev Response, havePrev bool, fresh Response, now time.Time) bool {
	if !havePrev {
		return true
	}
	if prev.Status == StatusRevoked &&
		validAt(prev, now) &&
		fresh.Status == StatusGood &&
		fresh.ThisUpdate.Before(prev.ThisUpdate) {
		return false
	}
	return true
}

// Len reports the number of cached conclusions, including expired ones
// that have not yet been overwritten. It is primarily intended for
// diagnostics and tests.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
