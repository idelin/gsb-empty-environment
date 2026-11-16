package netsec

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// ProtocolH2 is the HTTP/2 ALPN identifier accepted by the cache.
	ProtocolH2 = "h2"
	// ProtocolH3 is the HTTP/3 ALPN identifier accepted by the cache.
	ProtocolH3 = "h3"

	// DefaultMaxAge is used when an advertisement omits the ma parameter.
	DefaultMaxAge = 24 * time.Hour
	// MaxMaxAge is the longest lifetime accepted from an advertisement.
	MaxMaxAge = 7 * 24 * time.Hour
	// FailureCooldown is how long an endpoint is disabled after a failed
	// alternate-endpoint TLS handshake.
	FailureCooldown = 5 * time.Minute
	// MaxEntriesPerOrigin is the maximum number of alternatives retained for
	// one origin.
	MaxEntriesPerOrigin = 8
)

// Entry is one usable alternative service.
//
// Host is always an absolute hostname. If an advertisement omits the host, the
// origin's hostname is copied into Host.
type Entry struct {
	Protocol      string
	Host          string
	Port          int
	Expires       time.Time
	Persist       bool
	DisabledUntil time.Time
}

// Address returns the dial address for the alternative endpoint.
func (e Entry) Address() string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// Expired reports whether the entry has reached its advertised lifetime.
func (e Entry) Expired(now time.Time) bool {
	return !e.Expires.After(now)
}

// Disabled reports whether a recent handshake failure still disables the entry.
func (e Entry) Disabled(now time.Time) bool {
	return e.DisabledUntil.After(now)
}

// Usable reports whether the entry is neither expired nor disabled.
func (e Entry) Usable(now time.Time) bool {
	return !e.Expired(now) && !e.Disabled(now)
}

// Origin identifies the HTTPS origin that owns a set of advertisements.
type Origin struct {
	Host string
	Port int
}

// NewOrigin validates and normalizes an origin.
func NewOrigin(host string, port int) (Origin, error) {
	normalized, err := normalizeHost(host)
	if err != nil {
		return Origin{}, err
	}
	if port < 1 || port > 65535 {
		return Origin{}, fmt.Errorf("altsvc: invalid port %d", port)
	}
	return Origin{Host: normalized, Port: port}, nil
}

// String returns the canonical host:port form of an origin.
func (o Origin) String() string {
	return net.JoinHostPort(o.Host, strconv.Itoa(o.Port))
}

// ParseOrigin accepts a bare host, host:port, or HTTPS URL and returns a
// canonical HTTPS origin. A missing port means 443.
func ParseOrigin(raw string) (Origin, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Origin{}, errors.New("altsvc: empty origin")
	}

	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil {
			return Origin{}, fmt.Errorf("altsvc: invalid origin: %w", err)
		}
		if !strings.EqualFold(u.Scheme, "https") {
			return Origin{}, fmt.Errorf("altsvc: origin scheme %q is not https", u.Scheme)
		}
		port := 443
		if p := u.Port(); p != "" {
			port, err = strconv.Atoi(p)
			if err != nil {
				return Origin{}, fmt.Errorf("altsvc: invalid origin port: %w", err)
			}
		}
		return NewOrigin(u.Hostname(), port)
	}

	if host, portText, err := net.SplitHostPort(raw); err == nil {
		port, err := strconv.Atoi(portText)
		if err != nil {
			return Origin{}, fmt.Errorf("altsvc: invalid origin port: %w", err)
		}
		return NewOrigin(host, port)
	}

	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		return NewOrigin(raw[1:len(raw)-1], 443)
	}
	return NewOrigin(raw, 443)
}

// Option configures a Cache.
type Option func(*Cache)

// WithClock replaces the cache clock. It is primarily useful for deterministic
// expiry tests.
func WithClock(now func() time.Time) Option {
	return func(c *Cache) {
		if now != nil {
			c.now = now
		}
	}
}

// Cache is a concurrency-safe Alt-Svc cache keyed by HTTPS origin.
type Cache struct {
	mu      sync.Mutex
	origins map[string]*originEntries
	now     func() time.Time
	nextSeq uint64
}

type originEntries struct {
	entries []*cacheEntry
}

type cacheEntry struct {
	Entry
	seq uint64
}

func (c *Cache) nowTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Cache) ensureLocked() {
	if c.origins == nil {
		c.origins = make(map[string]*originEntries)
	}
}

// New creates an empty cache.
func New(opts ...Option) *Cache {
	c := &Cache{
		origins: make(map[string]*originEntries),
		now:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// NewCache is an alias for New.
func NewCache(opts ...Option) *Cache {
	return New(opts...)
}

// Store parses and stores all Alt-Svc header values in resp. It returns the
// number of advertisements that were stored or refreshed. Responses that were
// not received over TLS are ignored, so Alt-Svc headers injected into a
// plaintext response cannot populate the cache.
func (c *Cache) Store(resp *http.Response) int {
	origin, ok := originFromResponse(resp)
	if !ok {
		return 0
	}

	values := resp.Header.Values("Alt-Svc")
	if len(values) == 0 {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.ensureLocked()
	now := c.nowTime()
	key := origin.String()
	stored := 0
	for _, value := range values {
		for _, token := range splitOutsideQuotes(value, ',') {
			token = strings.TrimSpace(token)
			if token == "" {
				continue
			}
			if strings.EqualFold(strings.Trim(token, `"`), "clear") {
				delete(c.origins, key)
				continue
			}

			entry, ok := parseAlternative(origin, token, now)
			if !ok {
				continue
			}
			if !entry.Expires.After(now) {
				c.removeEndpointLocked(key, entry.Protocol, entry.Host, entry.Port)
				continue
			}
			c.addLocked(key, entry, now)
			stored++
		}
	}
	return stored
}

// StoreResponse is an alias for Store.
func (c *Cache) StoreResponse(resp *http.Response) int {
	return c.Store(resp)
}

// Add inserts or refreshes an alternative endpoint. An empty host means the
// origin host. A maxAge of zero removes an existing matching endpoint.
func (c *Cache) Add(origin, protocol, host string, port int, maxAge time.Duration, persist bool) bool {
	o, err := ParseOrigin(origin)
	if err != nil {
		return false
	}
	protocol, ok := normalizeProtocol(protocol)
	if !ok {
		return false
	}
	if host == "" {
		host = o.Host
	}
	host, err = normalizeHost(host)
	if err != nil {
		return false
	}
	if port < 1 || port > 65535 || maxAge < 0 {
		return false
	}
	if maxAge > MaxMaxAge {
		maxAge = MaxMaxAge
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := o.String()
	now := c.nowTime()
	if maxAge == 0 {
		c.removeEndpointLocked(key, protocol, host, port)
		return false
	}
	c.addLocked(key, Entry{
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Expires:  now.Add(maxAge),
		Persist:  persist,
	}, now)
	return true
}

// Lookup returns a snapshot of unexpired entries for origin. Disabled entries
// are included so callers can inspect DisabledUntil; use Candidates or Select
// to obtain endpoints that may actually be used.
func (c *Cache) Lookup(origin string) []Entry {
	o, err := ParseOrigin(origin)
	if err != nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := o.String()
	list := c.origins[key]
	if list == nil {
		return nil
	}
	c.pruneLocked(key, list, c.nowTime())
	if len(list.entries) == 0 {
		return nil
	}
	out := make([]Entry, len(list.entries))
	for i, entry := range list.entries {
		out[i] = entry.Entry
	}
	return out
}

// Entries is an alias for Lookup.
func (c *Cache) Entries(origin string) []Entry {
	return c.Lookup(origin)
}

// Candidates returns unexpired entries that are not in a failure cooldown.
func (c *Cache) Candidates(origin string) []Entry {
	o, err := ParseOrigin(origin)
	if err != nil {
		return nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := o.String()
	list := c.origins[key]
	if list == nil {
		return nil
	}
	now := c.nowTime()
	c.pruneLocked(key, list, now)
	out := make([]Entry, 0, len(list.entries))
	for _, entry := range list.entries {
		if entry.Usable(now) {
			out = append(out, entry.Entry)
		}
	}
	return out
}

// Select returns the first alternative that is currently usable. Entries are
// kept in insertion order, so callers can mark a failure and select again.
func (c *Cache) Select(origin string) (Entry, bool) {
	candidates := c.Candidates(origin)
	if len(candidates) == 0 {
		return Entry{}, false
	}
	return candidates[0], true
}

// Clear removes every cached alternative for origin.
func (c *Cache) Clear(origin string) int {
	o, err := ParseOrigin(origin)
	if err != nil {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	key := o.String()
	list := c.origins[key]
	if list == nil {
		return 0
	}
	n := len(list.entries)
	delete(c.origins, key)
	return n
}

// MarkFailure disables the matching endpoint for FailureCooldown. It returns
// whether the endpoint was found.
func (c *Cache) MarkFailure(origin string, entry Entry) bool {
	if entry.Host == "" {
		o, err := ParseOrigin(origin)
		if err != nil {
			return false
		}
		entry.Host = o.Host
	}
	return c.MarkFailed(origin, entry.Protocol, entry.Host, entry.Port)
}

// MarkHandshakeFailure is an alias for MarkFailure.
func (c *Cache) MarkHandshakeFailure(origin string, entry Entry) bool {
	return c.MarkFailure(origin, entry)
}

// MarkFailed disables the identified endpoint for FailureCooldown.
func (c *Cache) MarkFailed(origin, protocol, host string, port int) bool {
	o, err := ParseOrigin(origin)
	if err != nil {
		return false
	}
	protocol, ok := normalizeProtocol(protocol)
	if !ok {
		return false
	}
	if host == "" {
		host = o.Host
	}
	host, err = normalizeHost(host)
	if err != nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	list := c.origins[o.String()]
	if list == nil {
		return false
	}
	now := c.nowTime()
	for _, entry := range list.entries {
		if entry.Protocol == protocol && entry.Host == host && entry.Port == port {
			entry.DisabledUntil = now.Add(FailureCooldown)
			return true
		}
	}
	return false
}

func (c *Cache) addLocked(key string, next Entry, now time.Time) {
	c.ensureLocked()
	list := c.origins[key]
	if list == nil {
		list = &originEntries{}
		c.origins[key] = list
	}

	kept := make([]*cacheEntry, 0, len(list.entries)+1)
	seq := uint64(0)
	for _, existing := range list.entries {
		if existing.Expired(now) {
			continue
		}
		if existing.Protocol == next.Protocol &&
			existing.Host == next.Host &&
			existing.Port == next.Port {
			seq = existing.seq
			if existing.DisabledUntil.After(next.DisabledUntil) {
				next.DisabledUntil = existing.DisabledUntil
			}
			continue
		}
		kept = append(kept, existing)
	}

	if seq == 0 {
		c.nextSeq++
		seq = c.nextSeq
	}
	kept = append(kept, &cacheEntry{Entry: next, seq: seq})
	list.entries = kept

	for len(list.entries) > MaxEntriesPerOrigin {
		evict := 0
		for i := 1; i < len(list.entries); i++ {
			candidate := list.entries[i]
			current := list.entries[evict]
			if candidate.Expires.Before(current.Expires) ||
				(candidate.Expires.Equal(current.Expires) && candidate.seq < current.seq) {
				evict = i
			}
		}
		list.entries = append(list.entries[:evict], list.entries[evict+1:]...)
	}
}

func (c *Cache) pruneLocked(key string, list *originEntries, now time.Time) {
	kept := list.entries[:0]
	for _, entry := range list.entries {
		if !entry.Expired(now) {
			kept = append(kept, entry)
		}
	}
	list.entries = kept
	if len(list.entries) == 0 {
		delete(c.origins, key)
	}
}

func (c *Cache) removeEndpointLocked(key, protocol, host string, port int) {
	list := c.origins[key]
	if list == nil {
		return
	}
	kept := list.entries[:0]
	for _, entry := range list.entries {
		if entry.Protocol != protocol || entry.Host != host || entry.Port != port {
			kept = append(kept, entry)
		}
	}
	list.entries = kept
	if len(list.entries) == 0 {
		delete(c.origins, key)
	}
}

func originFromResponse(resp *http.Response) (Origin, bool) {
	if resp == nil || resp.TLS == nil {
		return Origin{}, false
	}
	if resp.Request == nil {
		return Origin{}, false
	}

	scheme := ""
	host := ""
	portText := ""
	if u := resp.Request.URL; u != nil {
		scheme = u.Scheme
		host = u.Hostname()
		portText = u.Port()
	}
	if scheme != "" && !strings.EqualFold(scheme, "https") {
		return Origin{}, false
	}
	if host == "" {
		if h, p, err := net.SplitHostPort(resp.Request.Host); err == nil {
			host, portText = h, p
		} else {
			host = resp.Request.Host
		}
	}
	if host == "" {
		return Origin{}, false
	}
	port := 443
	if portText != "" {
		var err error
		port, err = strconv.Atoi(portText)
		if err != nil {
			return Origin{}, false
		}
	}
	origin, err := NewOrigin(host, port)
	if err != nil {
		return Origin{}, false
	}
	return origin, true
}

func normalizeProtocol(protocol string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case ProtocolH2:
		return ProtocolH2, true
	case ProtocolH3:
		return ProtocolH3, true
	default:
		return "", false
	}
}

func normalizeHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return "", errors.New("altsvc: empty host")
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), nil
	}
	if len(host) > 253 {
		return "", errors.New("altsvc: host is too long")
	}
	if strings.ContainsAny(host, " \t\r\n/:\\@?#[]%") {
		return "", fmt.Errorf("altsvc: invalid host %q", host)
	}
	for _, r := range host {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '.' || r == '_' {
			continue
		}
		return "", fmt.Errorf("altsvc: invalid host %q", host)
	}
	if strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") ||
		strings.Contains(host, "..") {
		return "", fmt.Errorf("altsvc: invalid host %q", host)
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return "", fmt.Errorf("altsvc: invalid host %q", host)
		}
	}
	return strings.ToLower(host), nil
}

// DialTLSContext establishes a TLS connection to address while verifying the
// peer certificate against the original origin hostname. The alternative
// endpoint's hostname is used only for DNS resolution and is never used as the
// TLS verification name.
func DialTLSContext(ctx context.Context, network, address, origin string, base *tls.Config) (net.Conn, error) {
	config, err := TLSConfigForOrigin(origin, base)
	if err != nil {
		return nil, err
	}
	dialer := &tls.Dialer{Config: config}
	return dialer.DialContext(ctx, network, address)
}
