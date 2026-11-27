package sctpauth

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"sort"
	"time"
)

const (
	cookieVersion = byte(1)
	cookieMagic   = "SCTP-AUTH-COOKIE/"
	hmacSize      = 32
	nonceSize     = 32

	defaultCookieLifespan = 60 * time.Second
	replaySweepThreshold  = 4096
)

// StateCookie is the authenticated, stateless context that lets the server
// reconstruct an association without retaining any handshake memory.
type StateCookie struct {
	// InitiateTag is the peer's Initiate Tag; it becomes the Verification Tag
	// the peer must place on every subsequent chunk.
	InitiateTag uint32
	// MyTag is the server's own Initiate Tag carried in INIT-ACK.
	MyTag uint32
	// InitiateTSN is the peer's initial TSN from INIT.
	InitiateTSN uint32
	// MyTSN is the server's initial TSN advertised in INIT-ACK.
	MyTSN uint32
	// PeerAddrs is the canonical, sorted address list bound to the cookie.
	PeerAddrs []string
	// CreationTime is when the cookie was issued.
	CreationTime time.Time
	// Lifespan bounds how long the cookie remains valid.
	Lifespan time.Duration
	// Nonce makes every cookie single use and unpredictable.
	Nonce [nonceSize]byte
}

// HandshakeParams carries the values negotiated during INIT/INIT-ACK that the
// cookie must bind together.
type HandshakeParams struct {
	InitiateTag uint32
	MyTag       uint32
	InitiateTSN uint32
	MyTSN       uint32
	PeerAddrs   []string
}

// canonicalAddr normalizes an IP literal. Unparseable values are lower-cased
// and retained verbatim so that a malformed address can never match a valid
// one by accident.
func canonicalAddr(addr string) string {
	parsed, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	return parsed.String()
}

// canonicalAddrSet returns a de-duplicated, sorted list of canonical addresses.
func canonicalAddrSet(addrs []string) []string {
	seen := make(map[string]struct{}, len(addrs))
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		c := canonicalAddr(addr)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// marshalBody serializes every authenticated cookie field. The output is the
// exact byte string covered by the HMAC.
func marshalBody(c *StateCookie) []byte {
	body := make([]byte, 0, 128)
	var buf [8]byte

	body = append(body, cookieVersion)

	binary.BigEndian.PutUint32(buf[:4], c.InitiateTag)
	body = append(body, buf[:4]...)
	binary.BigEndian.PutUint32(buf[:4], c.MyTag)
	body = append(body, buf[:4]...)
	binary.BigEndian.PutUint32(buf[:4], c.InitiateTSN)
	body = append(body, buf[:4]...)
	binary.BigEndian.PutUint32(buf[:4], c.MyTSN)
	body = append(body, buf[:4]...)

	binary.BigEndian.PutUint64(buf[:], uint64(c.CreationTime.UnixNano()))
	body = append(body, buf[:]...)
	binary.BigEndian.PutUint64(buf[:], uint64(c.Lifespan.Nanoseconds()))
	body = append(body, buf[:]...)

	binary.BigEndian.PutUint32(buf[:4], uint32(len(c.PeerAddrs)))
	body = append(body, buf[:4]...)
	for _, addr := range c.PeerAddrs {
		binary.BigEndian.PutUint32(buf[:4], uint32(len(addr)))
		body = append(body, buf[:4]...)
		body = append(body, addr...)
	}

	body = append(body, c.Nonce[:]...)
	return body
}

// parseBody decodes a marshaled cookie body, rejecting any truncation.
func parseBody(raw []byte) (*StateCookie, error) {
	cur := newCursor(raw)
	cookie := &StateCookie{}

	version, ok := cur.byte()
	if !ok || version != cookieVersion {
		return nil, ErrMalformedCookie
	}
	if cookie.InitiateTag, ok = cur.uint32(); !ok {
		return nil, ErrMalformedCookie
	}
	if cookie.MyTag, ok = cur.uint32(); !ok {
		return nil, ErrMalformedCookie
	}
	if cookie.InitiateTSN, ok = cur.uint32(); !ok {
		return nil, ErrMalformedCookie
	}
	if cookie.MyTSN, ok = cur.uint32(); !ok {
		return nil, ErrMalformedCookie
	}
	createdNano, ok := cur.uint64()
	if !ok {
		return nil, ErrMalformedCookie
	}
	lifespanNano, ok := cur.uint64()
	if !ok {
		return nil, ErrMalformedCookie
	}
	cookie.CreationTime = time.Unix(0, int64(createdNano))
	cookie.Lifespan = time.Duration(int64(lifespanNano))

	addrCount, ok := cur.uint32()
	if !ok {
		return nil, ErrMalformedCookie
	}
	cookie.PeerAddrs = make([]string, 0, addrCount)
	for i := uint32(0); i < addrCount; i++ {
		length, ok := cur.uint32()
		if !ok {
			return nil, ErrMalformedCookie
		}
		addr, ok := cur.bytes(int(length))
		if !ok {
			return nil, ErrMalformedCookie
		}
		cookie.PeerAddrs = append(cookie.PeerAddrs, string(addr))
	}

	nonce, ok := cur.bytes(nonceSize)
	if !ok || cur.remaining() != 0 {
		return nil, ErrMalformedCookie
	}
	copy(cookie.Nonce[:], nonce)
	return cookie, nil
}

// cursor is a bounds-checked big-endian reader.
type cursor struct {
	data []byte
	pos  int
}

func newCursor(data []byte) *cursor { return &cursor{data: data} }

func (c *cursor) remaining() int { return len(c.data) - c.pos }

func (c *cursor) byte() (byte, bool) {
	if c.remaining() < 1 {
		return 0, false
	}
	v := c.data[c.pos]
	c.pos++
	return v, true
}

func (c *cursor) uint32() (uint32, bool) {
	if c.remaining() < 4 {
		return 0, false
	}
	v := binary.BigEndian.Uint32(c.data[c.pos:])
	c.pos += 4
	return v, true
}

func (c *cursor) uint64() (uint64, bool) {
	if c.remaining() < 8 {
		return 0, false
	}
	v := binary.BigEndian.Uint64(c.data[c.pos:])
	c.pos += 8
	return v, true
}

func (c *cursor) bytes(n int) ([]byte, bool) {
	if n < 0 || c.remaining() < n {
		return nil, false
	}
	v := c.data[c.pos : c.pos+n]
	c.pos += n
	return v, true
}

// CookieSigner issues stateless INIT-ACK State Cookies.
type CookieSigner struct {
	keys     *KeyRing
	lifespan time.Duration
	now      func() time.Time
}

// NewCookieSigner builds a signer using the supplied rotating key ring.
func NewCookieSigner(keys *KeyRing) *CookieSigner {
	return &CookieSigner{
		keys:     keys,
		lifespan: defaultCookieLifespan,
		now:      time.Now,
	}
}

// withClock returns a copy whose clock is overridden, used for deterministic
// tests without exposing clock plumbing on the public type.
func (s *CookieSigner) withClock(now func() time.Time) *CookieSigner {
	copy := *s
	copy.now = now
	return &copy
}

// SetLifespan overrides the default cookie validity window.
func (s *CookieSigner) SetLifespan(lifespan time.Duration) {
	if lifespan > 0 {
		s.lifespan = lifespan
	}
}

// Issue validates the negotiated parameters and returns both the structured
// cookie and its signed, wire-ready byte form for the INIT-ACK chunk.
func (s *CookieSigner) Issue(params HandshakeParams) (*StateCookie, []byte, error) {
	if params.InitiateTag == 0 || params.MyTag == 0 {
		return nil, nil, ErrForbiddenTag
	}

	cookie := &StateCookie{
		InitiateTag:  params.InitiateTag,
		MyTag:        params.MyTag,
		InitiateTSN:  params.InitiateTSN,
		MyTSN:        params.MyTSN,
		PeerAddrs:    canonicalAddrSet(params.PeerAddrs),
		CreationTime: s.now().UTC(),
		Lifespan:     s.lifespan,
		Nonce:        s.keys.Nonce(),
	}

	body := marshalBody(cookie)
	wire := make([]byte, 0, len(cookieMagic)+1+len(body)+hmacSize)
	wire = append(wire, cookieMagic...)
	wire = append(wire, cookieVersion)
	wire = append(wire, body...)
	wire = append(wire, s.keys.sign(body)...)
	return cookie, wire, nil
}

// CookieEcho models a received COOKIE-ECHO together with the transport header
// context needed to authenticate its origin.
type CookieEcho struct {
	SourceAddr      string
	VerificationTag uint32
	Cookie          []byte
}

// CookieVerifier authenticates COOKIE-ECHO chunks and enforces single use.
type CookieVerifier struct {
	keys    *KeyRing
	now     func() time.Time
	seen    map[string]time.Time
	sweptAt int
}

// NewCookieVerifier builds a verifier over the same rotating key ring.
func NewCookieVerifier(keys *KeyRing) *CookieVerifier {
	return &CookieVerifier{
		keys: keys,
		now:  time.Now,
		seen: make(map[string]time.Time),
	}
}

func (v *CookieVerifier) withClock(now func() time.Time) *CookieVerifier {
	copy := *v
	copy.now = now
	copy.seen = make(map[string]time.Time)
	return &copy
}

// Verify authenticates a COOKIE-ECHO and returns the recovered state. It
// rejects forgeries, tampering, expiry, illegal tags, cross-source replay and
// every reuse of a cookie nonce.
func (v *CookieVerifier) Verify(echo CookieEcho) (*StateCookie, error) {
	if echo.VerificationTag == 0 {
		return nil, ErrForbiddenTag
	}

	headerLen := len(cookieMagic) + 1
	if len(echo.Cookie) < headerLen+hmacSize {
		return nil, ErrMalformedCookie
	}
	if string(echo.Cookie[:len(cookieMagic)]) != cookieMagic {
		return nil, ErrMalformedCookie
	}
	if echo.Cookie[len(cookieMagic)] != cookieVersion {
		return nil, ErrMalformedCookie
	}

	signedEnd := len(echo.Cookie) - hmacSize
	body := echo.Cookie[headerLen:signedEnd]
	tag := echo.Cookie[signedEnd:]
	if !v.keys.authenticate(body, tag) {
		return nil, ErrCookieSignature
	}

	cookie, err := parseBody(body)
	if err != nil {
		return nil, err
	}
	if cookie.InitiateTag == 0 || cookie.MyTag == 0 {
		return nil, ErrForbiddenTag
	}
	if echo.VerificationTag != cookie.InitiateTag {
		return nil, ErrVerificationTag
	}

	expiry := cookie.CreationTime.Add(cookie.Lifespan)
	if !v.now().Before(expiry) {
		return nil, ErrCookieExpired
	}

	source := canonicalAddr(echo.SourceAddr)
	if !addressBound(cookie.PeerAddrs, source) {
		return nil, ErrSourceAddress
	}

	nonceKey := hex.EncodeToString(cookie.Nonce[:])
	now := v.now()
	v.sweepExpired(now)
	if usedUntil, reused := v.seen[nonceKey]; reused && now.Before(usedUntil) {
		return nil, ErrCookieReplay
	}
	v.seen[nonceKey] = expiry

	return cookie, nil
}

// addressBound reports whether addr is a member of the bound address set.
func addressBound(addrs []string, addr string) bool {
	for _, bound := range addrs {
		if bound == addr {
			return true
		}
	}
	return false
}

// sweepExpired drops consumed nonces whose cookies have aged out.
func (v *CookieVerifier) sweepExpired(now time.Time) {
	v.sweptAt++
	if v.sweptAt < replaySweepThreshold && len(v.seen) < replaySweepThreshold {
		return
	}
	for nonce, expiry := range v.seen {
		if !now.Before(expiry) {
			delete(v.seen, nonce)
		}
	}
	v.sweptAt = 0
}
