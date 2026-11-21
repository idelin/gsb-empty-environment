// Package revocation implements a revocation query component for certificate
// verification. Revocation responses are cached per issuer, bounded by their
// validity window and a maximum cache age. Stale responses are rejected, and
// unreachable backends fail closed by policy.
package revocation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Status is the revocation verdict for a single certificate serial number.
type Status int

const (
	// StatusGood means the issuer knows the serial and it is not revoked.
	StatusGood Status = iota
	// StatusRevoked means the issuer has revoked the serial.
	StatusRevoked
	// StatusNotIssued means the serial is unknown to the issuer.
	StatusNotIssued
)

func (s Status) String() string {
	switch s {
	case StatusGood:
		return "good"
	case StatusRevoked:
		return "revoked"
	case StatusNotIssued:
		return "not-issued"
	default:
		return "unknown"
	}
}

// ErrStaleResponse is returned when a fetched or cached revocation response
// is older than the allowed freshness window.
var ErrStaleResponse = errors.New("revocation: response exceeds freshness limit")

// ErrUnavailable is returned when the revocation backend cannot be reached
// and the configured policy fails closed.
var ErrUnavailable = errors.New("revocation: backend unreachable, failing closed")

// Entry describes one serial number known to an issuer.
type Entry struct {
	Serial    string
	Revoked   bool
	RevokedAt time.Time
}

// Response is a signed revocation list (or equivalent) published by an issuer.
type Response struct {
	Issuer     string
	ThisUpdate time.Time
	NextUpdate time.Time
	Entries    map[string]Entry
}

// Fetcher retrieves the current revocation response for an issuer.
type Fetcher interface {
	Fetch(ctx context.Context, issuer string) (*Response, error)
}

// FetcherFunc adapts a function to the Fetcher interface.
type FetcherFunc func(ctx context.Context, issuer string) (*Response, error)

// Fetch implements Fetcher.
func (f FetcherFunc) Fetch(ctx context.Context, issuer string) (*Response, error) {
	return f(ctx, issuer)
}

// Config controls caching, freshness, and failure policy.
type Config struct {
	// MaxCacheAge bounds how long a fetched response may be served from
	// cache, regardless of its NextUpdate.
	MaxCacheAge time.Duration
	// MaxResponseAge bounds how old ThisUpdate may be before a response is
	// rejected as stale.
	MaxResponseAge time.Duration
	// ClockSkew tolerates small clock differences around NextUpdate.
	ClockSkew time.Duration
	// FailClosed denies verification when the backend is unreachable and no
	// valid cached response exists.
	FailClosed bool
	// Now supplies the current time; defaults to time.Now.
	Now func() time.Time
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.MaxCacheAge <= 0 {
		out.MaxCacheAge = time.Hour
	}
	if out.MaxResponseAge <= 0 {
		out.MaxResponseAge = 24 * time.Hour
	}
	if out.Now == nil {
		out.Now = time.Now
	}
	return out
}

type cachedResponse struct {
	resp      *Response
	fetchedAt time.Time
}

// Checker answers revocation queries with a per-issuer cache.
type Checker struct {
	cfg     Config
	fetcher Fetcher

	mu    sync.Mutex
	cache map[string]*cachedResponse
}

// NewChecker builds a Checker backed by fetcher.
func NewChecker(fetcher Fetcher, cfg Config) *Checker {
	return &Checker{
		cfg:     cfg.withDefaults(),
		fetcher: fetcher,
		cache:   make(map[string]*cachedResponse),
	}
}

// Check resolves the revocation status of serial for issuer.
func (c *Checker) Check(ctx context.Context, issuer, serial string) (Status, error) {
	resp, err := c.responseFor(ctx, issuer)
	if err != nil {
		return StatusNotIssued, err
	}
	entry, ok := resp.Entries[serial]
	if !ok {
		return StatusNotIssued, nil
	}
	if entry.Revoked {
		return StatusRevoked, nil
	}
	return StatusGood, nil
}

// responseFor returns a valid cached response or fetches a fresh one.
func (c *Checker) responseFor(ctx context.Context, issuer string) (*Response, error) {
	now := c.cfg.Now()

	c.mu.Lock()
	if cached, ok := c.cache[issuer]; ok && c.usable(cached, now) {
		resp := cached.resp
		c.mu.Unlock()
		return resp, nil
	}
	delete(c.cache, issuer)
	c.mu.Unlock()

	resp, err := c.fetcher.Fetch(ctx, issuer)
	if err != nil {
		if c.cfg.FailClosed {
			return nil, fmt.Errorf("%w: issuer %q: %v", ErrUnavailable, issuer, err)
		}
		return nil, fmt.Errorf("revocation: fetch failed for issuer %q: %w", issuer, err)
	}
	if err := c.validateFresh(resp, now); err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cache[issuer] = &cachedResponse{resp: resp, fetchedAt: now}
	c.mu.Unlock()
	return resp, nil
}

// usable reports whether a cached response is still within its validity
// window, cache age limit, and freshness limit.
func (c *Checker) usable(cached *cachedResponse, now time.Time) bool {
	if now.After(cached.resp.NextUpdate.Add(c.cfg.ClockSkew)) {
		return false
	}
	if now.Sub(cached.fetchedAt) > c.cfg.MaxCacheAge {
		return false
	}
	return c.validateFresh(cached.resp, now) == nil
}

// validateFresh rejects responses whose ThisUpdate is older than the
// configured freshness limit.
func (c *Checker) validateFresh(resp *Response, now time.Time) error {
	if resp == nil {
		return errors.New("revocation: nil response")
	}
	if now.Sub(resp.ThisUpdate) > c.cfg.MaxResponseAge {
		return fmt.Errorf("%w: issuer %q thisUpdate %s", ErrStaleResponse, resp.Issuer, resp.ThisUpdate)
	}
	return nil
}
