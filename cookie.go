package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	// ClientCookieLen is the required size of a DNS Client Cookie.
	ClientCookieLen = 8

	// ServerCookieLen is the size of the Server Cookie emitted by this package.
	ServerCookieLen = 16

	// ServerCookieVersion is the only Server Cookie version accepted here.
	ServerCookieVersion = 1

	// CookieOptionCode is the EDNS option code assigned to DNS Cookies (RFC 7873).
	CookieOptionCode = 10

	// BadCookieRCode is the extended DNS RCODE for BADCOOKIE (23).
	BadCookieRCode = 23

	// MaxCookieAge is the maximum age of a Server Cookie timestamp.
	MaxCookieAge = time.Hour

	// MaxCookieFuture is the maximum tolerated clock skew for a Server Cookie
	// timestamp.
	MaxCookieFuture = 5 * time.Minute
)

const (
	serverTimestampOffset = 4
	serverMACOffset       = 8
	serverMACLen          = 8
)

var (
	// ErrClientCookieLength indicates that a Client Cookie was not 8 bytes.
	ErrClientCookieLength = errors.New("dns cookie: client cookie must be 8 bytes")

	// ErrServerCookieLength indicates that a Server Cookie was not 16 bytes.
	ErrServerCookieLength = errors.New("dns cookie: server cookie must be 16 bytes")

	// ErrServerCookieVersion indicates that a Server Cookie has an unknown
	// version.
	ErrServerCookieVersion = errors.New("dns cookie: unsupported server cookie version")

	// ErrServerCookieReserved indicates that non-zero reserved bytes were
	// found in a Server Cookie.
	ErrServerCookieReserved = errors.New("dns cookie: server cookie reserved bytes are non-zero")

	// ErrServerCookieMAC indicates that a Server Cookie was not produced with
	// the configured key and Client IP.
	ErrServerCookieMAC = errors.New("dns cookie: invalid server cookie MAC")

	// ErrServerCookieExpired indicates that a Server Cookie is too old.
	ErrServerCookieExpired = errors.New("dns cookie: server cookie timestamp is too old")

	// ErrServerCookieFuture indicates that a Server Cookie timestamp is too
	// far in the future.
	ErrServerCookieFuture = errors.New("dns cookie: server cookie timestamp is in the future")

	// ErrCookieKey indicates that an empty HMAC key was supplied.
	ErrCookieKey = errors.New("dns cookie: HMAC key must not be empty")

	// ErrClientIP indicates that the supplied client IP was invalid.
	ErrClientIP = errors.New("dns cookie: invalid client IP")
)

// Clock supplies the current time. It is injectable so cookie expiry and
// 32-bit timestamp wraparound can be tested deterministically.
type Clock func() time.Time

// CookieGateOption configures a CookieGate.
type CookieGateOption func(*CookieGate)

// WithClock sets the clock used to create and validate Server Cookies.
func WithClock(clock Clock) CookieGateOption {
	return func(g *CookieGate) {
		if clock != nil {
			g.now = clock
		}
	}
}

// WithNow is an alias for WithClock.
func WithNow(clock Clock) CookieGateOption {
	return WithClock(clock)
}

// CookieGate creates and validates DNS Cookies bound to a client address.
type CookieGate struct {
	key []byte
	now Clock
}

// NewCookieGate returns a gate using key for HMAC-SHA256. The key should be a
// long-lived random secret, at least 32 bytes long. It is copied, not retained
// by reference.
func NewCookieGate(key []byte, options ...CookieGateOption) (*CookieGate, error) {
	if len(key) == 0 {
		return nil, ErrCookieKey
	}
	g := &CookieGate{
		key: append([]byte(nil), key...),
		now: time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(g)
		}
	}
	return g, nil
}

// GenerateServerCookie creates a Server Cookie using the gate clock.
func (g *CookieGate) GenerateServerCookie(clientCookie []byte, clientIP net.IP) ([]byte, error) {
	return GenerateServerCookie(g.key, clientCookie, clientIP, g.now())
}

// GenerateServerCookieAt creates a Server Cookie using the supplied time.
func (g *CookieGate) GenerateServerCookieAt(clientCookie []byte, clientIP net.IP, now time.Time) ([]byte, error) {
	return GenerateServerCookie(g.key, clientCookie, clientIP, now)
}

// ValidateServerCookie validates a Server Cookie using the gate clock.
func (g *CookieGate) ValidateServerCookie(clientCookie, serverCookie []byte, clientIP net.IP) error {
	return ValidateServerCookie(g.key, clientCookie, serverCookie, clientIP, g.now())
}

// ValidateServerCookieAt validates a Server Cookie using the supplied time.
func (g *CookieGate) ValidateServerCookieAt(clientCookie, serverCookie []byte, clientIP net.IP, now time.Time) error {
	return ValidateServerCookie(g.key, clientCookie, serverCookie, clientIP, now)
}

// GenerateServerCookie creates the canonical 16-byte Server Cookie:
//
//	byte 0       version (1)
//	bytes 1..3   reserved (zero)
//	bytes 4..7   Unix timestamp, big-endian uint32
//	bytes 8..15  HMAC-SHA256(key, clientCookie || bytes 0..7 || client IP)[:8]
//
// IPv4 addresses are encoded as 4 bytes and IPv6 addresses as 16 bytes.
func GenerateServerCookie(key, clientCookie []byte, clientIP net.IP, now time.Time) ([]byte, error) {
	if len(key) == 0 {
		return nil, ErrCookieKey
	}
	if len(clientCookie) != ClientCookieLen {
		return nil, ErrClientCookieLength
	}
	ip, err := ipBytes(clientIP)
	if err != nil {
		return nil, err
	}

	cookie := make([]byte, ServerCookieLen)
	cookie[0] = ServerCookieVersion
	binary.BigEndian.PutUint32(cookie[serverTimestampOffset:], uint32(now.Unix()))
	writeServerMAC(cookie, key, clientCookie, ip)
	return cookie, nil
}

// NewServerCookie is an alias for GenerateServerCookie.
func NewServerCookie(key, clientCookie []byte, clientIP net.IP, now time.Time) ([]byte, error) {
	return GenerateServerCookie(key, clientCookie, clientIP, now)
}

// ValidateServerCookie verifies the version, reserved bytes, HMAC, and
// timestamp of a Server Cookie. Timestamp comparison uses 32-bit serial
// arithmetic so it remains correct across the Unix-time wraparound.
func ValidateServerCookie(key, clientCookie, serverCookie []byte, clientIP net.IP, now time.Time) error {
	if len(key) == 0 {
		return ErrCookieKey
	}
	if len(clientCookie) != ClientCookieLen {
		return ErrClientCookieLength
	}
	if len(serverCookie) != ServerCookieLen {
		return ErrServerCookieLength
	}
	if serverCookie[0] != ServerCookieVersion {
		return ErrServerCookieVersion
	}
	if serverCookie[1] != 0 || serverCookie[2] != 0 || serverCookie[3] != 0 {
		return ErrServerCookieReserved
	}
	ip, err := ipBytes(clientIP)
	if err != nil {
		return err
	}

	expected := make([]byte, ServerCookieLen)
	copy(expected[:serverMACOffset], serverCookie[:serverMACOffset])
	writeServerMAC(expected, key, clientCookie, ip)
	if subtle.ConstantTimeCompare(expected[serverMACOffset:], serverCookie[serverMACOffset:]) != 1 {
		return ErrServerCookieMAC
	}

	timestamp := binary.BigEndian.Uint32(serverCookie[serverTimestampOffset:])
	age := serialDelta(uint32(now.Unix()), timestamp)
	if age > int64(MaxCookieAge/time.Second) {
		return fmt.Errorf("%w: age %ds", ErrServerCookieExpired, age)
	}
	if age < -int64(MaxCookieFuture/time.Second) {
		return fmt.Errorf("%w: offset %ds", ErrServerCookieFuture, age)
	}
	return nil
}

// VerifyServerCookie is an alias for ValidateServerCookie.
func VerifyServerCookie(key, clientCookie, serverCookie []byte, clientIP net.IP, now time.Time) error {
	return ValidateServerCookie(key, clientCookie, serverCookie, clientIP, now)
}

// SerialDelta returns a - b as a signed 32-bit serial-number difference, as
// defined by RFC 1982. It is exported for callers that need the same timestamp
// comparison rule.
func SerialDelta(a, b uint32) int64 {
	return serialDelta(a, b)
}

func serialDelta(a, b uint32) int64 {
	difference := int64(uint32(a - b))
	if difference >= 1<<31 {
		difference -= 1 << 32
	}
	return difference
}

func writeServerMAC(cookie, key, clientCookie, ip []byte) {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(clientCookie)
	_, _ = mac.Write(cookie[:serverMACOffset])
	_, _ = mac.Write(ip)
	sum := mac.Sum(nil)
	copy(cookie[serverMACOffset:], sum[:serverMACLen])
}

func ipBytes(ip net.IP) ([]byte, error) {
	if v4 := ip.To4(); v4 != nil {
		return v4, nil
	}
	if v6 := ip.To16(); v6 != nil {
		return v6, nil
	}
	return nil, ErrClientIP
}
