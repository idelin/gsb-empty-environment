package dnscookie

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

const (
	// ClientCookieLen is the fixed length of an RFC 7873 Client Cookie.
	ClientCookieLen = 8
	// ServerCookieLen is the length of the RFC 9018 interoperable
	// Server Cookie (version || reserved || timestamp || hash).
	ServerCookieLen = 16

	// CookieVersion is the only Server Cookie version defined by
	// RFC 9018 Section 5.2.2.
	CookieVersion = 1

	// MaxFutureSkew is how far a Server Cookie timestamp may lie in
	// the future before it is rejected (RFC 9018 Section 5.2.2.1
	// recommends 5 minutes).
	MaxFutureSkew = 5 * time.Minute
	// MaxPastSkew is how old a Server Cookie timestamp may be before
	// it is rejected (RFC 9018 Section 5.2.2.1 recommends 1 hour).
	MaxPastSkew = time.Hour
)

var (
	ErrCookieTooShort      = errors.New("dnscookie: server cookie is not 16 bytes")
	ErrCookieVersion       = errors.New("dnscookie: unsupported server cookie version")
	ErrCookieReserved      = errors.New("dnscookie: reserved bytes are not zero")
	ErrCookieFuture        = errors.New("dnscookie: server cookie timestamp too far in the future")
	ErrCookieExpired       = errors.New("dnscookie: server cookie timestamp too old")
	ErrCookieHash          = errors.New("dnscookie: server cookie hash mismatch")
	ErrClientCookieLength  = errors.New("dnscookie: client cookie is not 8 bytes")
	ErrCookieIPUnspecified = errors.New("dnscookie: client IP is unspecified")
)

// KeyRing holds the server secrets used to mint and verify Server
// Cookies. It supports seamless rotation: cookies minted with the
// previous secret remain verifiable until they age out of the
// timestamp window.
type KeyRing struct {
	mu          sync.RWMutex
	current     [16]byte
	previous    [16]byte
	hasPrevious bool

	// Now supplies the current time; replaceable in tests.
	Now func() time.Time
}

// NewKeyRing returns a KeyRing using secret as the current secret.
func NewKeyRing(secret [16]byte) *KeyRing {
	return &KeyRing{current: secret, Now: time.Now}
}

// Rotate installs newSecret as the current secret and demotes the
// existing secret to the previous (verification-only) slot.
func (k *KeyRing) Rotate(newSecret [16]byte) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.previous = k.current
	k.hasPrevious = true
	k.current = newSecret
}

func (k *KeyRing) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

// NewClientCookie returns a fresh random 8-byte Client Cookie.
func NewClientCookie() ([]byte, error) {
	c := make([]byte, ClientCookieLen)
	if _, err := rand.Read(c); err != nil {
		return nil, fmt.Errorf("dnscookie: %w", err)
	}
	return c, nil
}

// ipBytes normalizes the client IP to its 4-byte (IPv4) or 16-byte
// (IPv6) form as required by RFC 9018 Section 5.2.2.
func ipBytes(ip netip.Addr) ([]byte, error) {
	ip = ip.Unmap()
	switch {
	case ip.Is4():
		b := ip.As4()
		return b[:], nil
	case ip.Is6():
		b := ip.As16()
		return b[:], nil
	default:
		return nil, ErrCookieIPUnspecified
	}
}

// hashInput builds clientCookie || version || reserved || timestamp ||
// clientIP exactly as specified by RFC 9018 Section 5.2.2.
func hashInput(clientCookie []byte, version byte, reserved [3]byte, timestamp uint32, ip []byte) []byte {
	buf := make([]byte, 0, len(clientCookie)+8+len(ip))
	buf = append(buf, clientCookie...)
	buf = append(buf, version)
	buf = append(buf, reserved[:]...)
	var ts [4]byte
	binary.BigEndian.PutUint32(ts[:], timestamp)
	buf = append(buf, ts[:]...)
	buf = append(buf, ip...)
	return buf
}

// NewServerCookie mints a 16-byte RFC 9018 Server Cookie for the given
// Client Cookie and client IP using the ring's current secret.
func (k *KeyRing) NewServerCookie(clientCookie []byte, clientIP netip.Addr) ([]byte, error) {
	if len(clientCookie) != ClientCookieLen {
		return nil, ErrClientCookieLength
	}
	ip, err := ipBytes(clientIP)
	if err != nil {
		return nil, err
	}
	k.mu.RLock()
	secret := k.current
	k.mu.RUnlock()

	ts := uint32(k.now().Unix())
	input := hashInput(clientCookie, CookieVersion, [3]byte{}, ts, ip)
	sum := sipHash24(&secret, input)

	cookie := make([]byte, ServerCookieLen)
	cookie[0] = CookieVersion
	// cookie[1:4] reserved, already zero.
	binary.BigEndian.PutUint32(cookie[4:8], ts)
	// The hash is stored little-endian, matching the SipHash reference
	// output byte order.
	binary.LittleEndian.PutUint64(cookie[8:16], sum)
	return cookie, nil
}

// VerifyServerCookie validates a 16-byte RFC 9018 Server Cookie
// against the Client Cookie and source IP it was issued for. It
// accepts cookies minted with the current or the previous secret
// (rotation window) and enforces the timestamp anti-replay window.
func (k *KeyRing) VerifyServerCookie(serverCookie, clientCookie []byte, clientIP netip.Addr) error {
	if len(serverCookie) != ServerCookieLen {
		return ErrCookieTooShort
	}
	if len(clientCookie) != ClientCookieLen {
		return ErrClientCookieLength
	}
	if serverCookie[0] != CookieVersion {
		return ErrCookieVersion
	}
	if serverCookie[1] != 0 || serverCookie[2] != 0 || serverCookie[3] != 0 {
		return ErrCookieReserved
	}
	ip, err := ipBytes(clientIP)
	if err != nil {
		return err
	}

	ts := int64(binary.BigEndian.Uint32(serverCookie[4:8]))
	now := k.now().Unix()
	if ts > now+int64(MaxFutureSkew/time.Second) {
		return ErrCookieFuture
	}
	if ts < now-int64(MaxPastSkew/time.Second) {
		return ErrCookieExpired
	}

	input := hashInput(clientCookie, serverCookie[0], [3]byte{}, binary.BigEndian.Uint32(serverCookie[4:8]), ip)
	want := binary.LittleEndian.Uint64(serverCookie[8:16])

	k.mu.RLock()
	secrets := [][16]byte{k.current}
	if k.hasPrevious {
		secrets = append(secrets, k.previous)
	}
	k.mu.RUnlock()

	for i := range secrets {
		if sipHash24(&secrets[i], input) == want {
			return nil
		}
	}
	return ErrCookieHash
}

// CheckServerCookieFormat performs the stateless format validation a
// client can apply to a received Server Cookie: exact length, version
// 1 and zeroed reserved bytes. The hash cannot be verified without the
// server secret.
func CheckServerCookieFormat(serverCookie []byte) error {
	if len(serverCookie) != ServerCookieLen {
		return ErrCookieTooShort
	}
	if serverCookie[0] != CookieVersion {
		return ErrCookieVersion
	}
	if serverCookie[1] != 0 || serverCookie[2] != 0 || serverCookie[3] != 0 {
		return ErrCookieReserved
	}
	return nil
}
