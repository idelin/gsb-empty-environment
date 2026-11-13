package netsec

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultMaxAge applies when an advertisement carries no ma parameter.
	DefaultMaxAge = 86400 * time.Second
	// MaxMaxAge caps any advertised ma value.
	MaxMaxAge = 7 * 24 * time.Hour
	// MaxEntriesPerOrigin bounds the alternatives kept for one origin.
	MaxEntriesPerOrigin = 8
	// HandshakeFailurePenalty disables an alternative after a failed handshake.
	HandshakeFailurePenalty = 5 * time.Minute
)

// Origin identifies the scheme, host and port of the server a response came
// from and that future requests are directed to.
type Origin struct {
	Scheme string
	Host   string
	Port   uint16
}

func (o Origin) key() string {
	return o.Scheme + "://" + o.Host + ":" + strconv.Itoa(int(o.Port))
}

// Entry is a single cached alternative service for an origin.
type Entry struct {
	Protocol string // "h2" or "h3"
	Host     string // empty means the origin host
	Port     uint16
	Expires  time.Time
	Persist  bool
	// DisabledUntil is set after a handshake failure with this endpoint.
	DisabledUntil time.Time
}

// Endpoint returns the host:port to dial, defaulting the host to the origin's.
func (e *Entry) Endpoint(origin Origin) string {
	host := e.Host
	if host == "" {
		host = origin.Host
	}
	return net.JoinHostPort(host, strconv.Itoa(int(e.Port)))
}

func (e *Entry) usable(now time.Time) bool {
	return now.Before(e.Expires) && !now.Before(e.DisabledUntil)
}

// Cache stores Alt-Svc alternatives per origin for connection selection.
// It is safe for concurrent use.
type Cache struct {
	mu       sync.Mutex
	byOrigin map[string][]*Entry
	now      func() time.Time
}

// NewCache returns an empty Alt-Svc cache.
func NewCache() *Cache {
	return &Cache{byOrigin: make(map[string][]*Entry), now: time.Now}
}

// Observe processes Alt-Svc header field values from a response received for
// origin. Advertisements are only honored when the origin scheme is https;
// anything learned over cleartext is ignored.
func (c *Cache) Observe(origin Origin, headerValues []string) {
	if origin.Scheme != "https" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, hv := range headerValues {
		clear, alts, err := parseAltSvc(hv, c.now())
		if err != nil {
			continue
		}
		if clear {
			delete(c.byOrigin, origin.key())
			continue
		}
		for i := range alts {
			c.add(origin, alts[i])
		}
	}
}

// add inserts or replaces an entry and enforces the per-origin bound by
// evicting the entries that expire soonest.
func (c *Cache) add(origin Origin, e *Entry) {
	key := origin.key()
	list := c.byOrigin[key]
	for i, ex := range list {
		if ex.Protocol == e.Protocol && ex.Host == e.Host && ex.Port == e.Port {
			list[i] = e
			return
		}
	}
	list = append(list, e)
	for len(list) > MaxEntriesPerOrigin {
		soonest := 0
		for i := range list {
			if list[i].Expires.Before(list[soonest].Expires) {
				soonest = i
			}
		}
		list = append(list[:soonest], list[soonest+1:]...)
	}
	c.byOrigin[key] = list
}

// Alternatives returns the currently usable entries for origin: not expired
// and not disabled by a recent handshake failure.
func (c *Cache) Alternatives(origin Origin) []*Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	var out []*Entry
	for _, e := range c.byOrigin[origin.key()] {
		if e.usable(now) {
			out = append(out, e)
		}
	}
	return out
}

// RecordHandshakeFailure disables the matching entry for
// HandshakeFailurePenalty after a failed handshake with the endpoint.
func (c *Cache) RecordHandshakeFailure(origin Origin, protocol, host string, port uint16) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.byOrigin[origin.key()] {
		if e.Protocol == protocol && e.Host == host && e.Port == port {
			e.DisabledUntil = c.now().Add(HandshakeFailurePenalty)
			return
		}
	}
}

var errInvalidAltSvc = errors.New("netsec: invalid Alt-Svc value")

// parseAltSvc parses one Alt-Svc header field value. It reports clear == true
// for the value "clear". Entry expiry is computed from now.
func parseAltSvc(value string, now time.Time) (clear bool, entries []*Entry, err error) {
	if strings.TrimSpace(value) == "clear" {
		return true, nil, nil
	}
	for _, item := range splitAltSvcItems(value) {
		e, err := parseAltSvcItem(item, now)
		if err != nil {
			return false, nil, err
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return false, nil, errInvalidAltSvc
	}
	return false, entries, nil
}

// splitAltSvcItems splits on commas that are not inside a quoted-string.
func splitAltSvcItems(s string) []string {
	var items []string
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case inQuote && ch == '\\' && i+1 < len(s):
			b.WriteByte(ch)
			b.WriteByte(s[i+1])
			i++
		case ch == '"':
			inQuote = !inQuote
			b.WriteByte(ch)
		case ch == ',' && !inQuote:
			items = append(items, b.String())
			b.Reset()
		default:
			b.WriteByte(ch)
		}
	}
	items = append(items, b.String())
	return items
}

func parseAltSvcItem(item string, now time.Time) (*Entry, error) {
	parts := strings.Split(item, ";")
	kv := strings.SplitN(strings.TrimSpace(parts[0]), "=", 2)
	if len(kv) != 2 {
		return nil, errInvalidAltSvc
	}
	protocol := strings.TrimSpace(kv[0])
	if protocol != "h2" && protocol != "h3" {
		return nil, fmt.Errorf("netsec: unsupported Alt-Svc protocol %q", protocol)
	}
	authority, err := unquote(strings.TrimSpace(kv[1]))
	if err != nil {
		return nil, err
	}
	host, port, err := parseAuthority(authority)
	if err != nil {
		return nil, err
	}

	ma := DefaultMaxAge
	persist := false
	for _, p := range parts[1:] {
		pkv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(pkv) != 2 {
			return nil, errInvalidAltSvc
		}
		v, err := unquote(strings.TrimSpace(pkv[1]))
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(strings.TrimSpace(pkv[0])) {
		case "ma":
			n, err := strconv.ParseUint(v, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("netsec: invalid ma value %q", v)
			}
			ma = time.Duration(n) * time.Second
		case "persist":
			persist = v == "1"
		}
	}
	if ma > MaxMaxAge {
		ma = MaxMaxAge
	}
	return &Entry{
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Expires:  now.Add(ma),
		Persist:  persist,
	}, nil
}

// parseAuthority parses an alt-authority such as ":443" or "alt.example:443".
// The host part may be empty; the port is required.
func parseAuthority(authority string) (host string, port uint16, err error) {
	host, portStr, err := net.SplitHostPort(authority)
	if err != nil {
		return "", 0, fmt.Errorf("netsec: invalid alt-authority %q", authority)
	}
	n, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil || n == 0 {
		return "", 0, fmt.Errorf("netsec: invalid port %q", portStr)
	}
	return host, uint16(n), nil
}

// unquote removes surrounding quotes and resolves backslash escapes. Bare
// tokens are returned unchanged.
func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' {
		if strings.ContainsAny(s, `"\\`) {
			return "", errInvalidAltSvc
		}
		return s, nil
	}
	if s[len(s)-1] != '"' {
		return "", errInvalidAltSvc
	}
	var b strings.Builder
	body := s[1 : len(s)-1]
	for i := 0; i < len(body); i++ {
		if body[i] == '\\' {
			i++
			if i >= len(body) {
				return "", errInvalidAltSvc
			}
		}
		b.WriteByte(body[i])
	}
	return b.String(), nil
}
