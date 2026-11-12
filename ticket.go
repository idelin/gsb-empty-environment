package taskpkg

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Frame types carried by the upgraded protocol. A frame on the wire is a
// one-byte type, a two-byte big-endian body length, then the body.
const (
	FrameTypeData       byte = 0
	FrameTypeManagement byte = 1
)

var (
	ErrTicketForged     = errors.New("upgrade ticket: signature mismatch")
	ErrTicketExpired    = errors.New("upgrade ticket: expired")
	ErrStaleGeneration  = errors.New("upgrade ticket: stale connection generation")
	ErrDigestMismatch   = errors.New("upgrade ticket: request digest mismatch")
	ErrSubprotoMismatch = errors.New("upgrade ticket: subprotocol mismatch")
)

// Ticket is a capability granted after HTTP authentication of an upgrade
// request. It binds the digest of the original request, the negotiated
// subprotocol, the authenticated subject, the permitted frame types and the
// connection generation, and is HMAC-signed so any substitution of these
// fields is detected.
type Ticket struct {
	RequestDigest [32]byte
	Subprotocol   string
	Subject       string
	AllowedFrames []byte
	Generation    uint64
	IssuedAt      time.Time
	TTL           time.Duration

	sig []byte
}

// Allows reports whether the ticket permits frames of the given type.
func (t *Ticket) Allows(frameType byte) bool {
	for _, f := range t.AllowedFrames {
		if f == frameType {
			return true
		}
	}
	return false
}

func appendLengthPrefixed(buf []byte, s string) []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(len(s)))
	buf = append(buf, n[:]...)
	return append(buf, s...)
}

func (t *Ticket) unsignedBytes() []byte {
	buf := make([]byte, 0, 64)
	buf = append(buf, t.RequestDigest[:]...)
	buf = appendLengthPrefixed(buf, t.Subprotocol)
	buf = appendLengthPrefixed(buf, t.Subject)
	var fixed [8]byte
	binary.BigEndian.PutUint64(fixed[:], t.Generation)
	buf = append(buf, fixed[:]...)
	binary.BigEndian.PutUint64(fixed[:], uint64(t.IssuedAt.UnixNano()))
	buf = append(buf, fixed[:]...)
	binary.BigEndian.PutUint64(fixed[:], uint64(t.TTL))
	buf = append(buf, fixed[:]...)
	frames := append([]byte(nil), t.AllowedFrames...)
	sort.Slice(frames, func(i, j int) bool { return frames[i] < frames[j] })
	buf = appendLengthPrefixed(buf, string(frames))
	return buf
}

// Issuer signs and validates tickets. It tracks a monotonically increasing
// generation per connection so that tickets from a previous upgrade cycle on
// a reused connection are rejected.
type Issuer struct {
	secret []byte

	mu          sync.Mutex
	generations map[uint64]uint64
}

// NewIssuer returns an Issuer using secret as the HMAC key.
func NewIssuer(secret []byte) *Issuer {
	if len(secret) == 0 {
		panic("upgrade issuer requires a non-empty secret")
	}
	return &Issuer{
		secret:      append([]byte(nil), secret...),
		generations: make(map[uint64]uint64),
	}
}

func (i *Issuer) generation(connID uint64) uint64 {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.generations[connID]
}

// Advance bumps the connection generation, invalidating every ticket issued
// for the connection so far. It must be called when an upgrade aborts or the
// connection is recycled for a new upgrade cycle.
func (i *Issuer) Advance(connID uint64) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.generations[connID]++
}

func (i *Issuer) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, i.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

// Issue creates a signed ticket bound to the current connection generation.
func (i *Issuer) Issue(connID uint64, digest [32]byte, subject, subprotocol string, allowedFrames []byte, ttl time.Duration) *Ticket {
	t := &Ticket{
		RequestDigest: digest,
		Subprotocol:   subprotocol,
		Subject:       subject,
		AllowedFrames: append([]byte(nil), allowedFrames...),
		Generation:    i.generation(connID),
		IssuedAt:      time.Now(),
		TTL:           ttl,
	}
	t.sig = i.sign(t.unsignedBytes())
	return t
}

// Validate checks the ticket's signature, freshness and generation, and its
// binding to the digest of the original request and the negotiated
// subprotocol.
func (i *Issuer) Validate(t *Ticket, connID uint64, digest [32]byte, subprotocol string) error {
	if t == nil || !hmac.Equal(t.sig, i.sign(t.unsignedBytes())) {
		return ErrTicketForged
	}
	if t.TTL > 0 && time.Since(t.IssuedAt) > t.TTL {
		return ErrTicketExpired
	}
	if t.Generation != i.generation(connID) {
		return ErrStaleGeneration
	}
	if t.RequestDigest != digest {
		return ErrDigestMismatch
	}
	if t.Subprotocol != subprotocol {
		return ErrSubprotoMismatch
	}
	return nil
}

// DigestRequest hashes the canonical form of the original HTTP upgrade
// request so the ticket stays bound to exactly what was authenticated.
func DigestRequest(req *http.Request) [32]byte {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00", req.Method, req.Host, req.URL.RequestURI())
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		vals := append([]string(nil), req.Header[k]...)
		sort.Strings(vals)
		fmt.Fprintf(h, "%s:%s\x00", strings.ToLower(k), strings.Join(vals, ","))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
