package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// MaxExportLength bounds a single export. It matches the HKDF-expand limit
// used by DefaultSource: 255 blocks of SHA-256 output.
const MaxExportLength = 255 * sha256.Size

// reservedLabelPrefix is reserved by RFC 5705 section 4 and must never be
// handed to applications by this facade.
const reservedLabelPrefix = "EXPORTER"

var (
	ErrNotFrozen       = errors.New("netsec: handshake, certificate, SNI routing and ALPN decisions are not frozen")
	ErrStaleGeneration = errors.New("netsec: connection generation changed; the result belongs to a stale identity")
	ErrEmptyLabel      = errors.New("netsec: label must not be empty")
	ErrReservedLabel   = errors.New("netsec: label collides with the reserved EXPORTER prefix")
	ErrBadLength       = errors.New("netsec: requested export length out of range")
	ErrValueLength     = errors.New("netsec: committed value length does not match the ticket request")
)

// Digest is the immutable summary of one handshake transcript. The reverse
// proxy computes it once per handshake attempt and never mutates it.
type Digest [32]byte

// Event is a connection event that invalidates every previously frozen
// decision and starts a new generation.
type Event int

const (
	EventZeroRTT Event = iota + 1
	EventHelloRetryRequest
	EventECHRejected
	EventResumption
	EventCertReload
)

func (e Event) String() string {
	switch e {
	case EventZeroRTT:
		return "0-RTT"
	case EventHelloRetryRequest:
		return "HelloRetryRequest"
	case EventECHRejected:
		return "ECH rejected"
	case EventResumption:
		return "session resumption"
	case EventCertReload:
		return "certificate hot reload"
	}
	return "unknown"
}

// Identity is the frozen set of handshake decisions an exported value is
// bound to. CertID is the final authenticated certificate identity (for
// example a stable hash of the leaf), PSKIdentity is the resumption PSK
// identity and is empty for full handshakes.
type Identity struct {
	ECHAccepted  bool
	ECHInnerName string
	ECHOuterName string
	CertID       string
	ALPN         string
	PSKIdentity  string
}

// Vhost is the final routed virtual host: the ECH inner name when ECH was
// accepted, otherwise the outer (plain SNI) name.
func (id Identity) Vhost() string {
	if id.ECHAccepted && id.ECHInnerName != "" {
		return id.ECHInnerName
	}
	return id.ECHOuterName
}

// Source derives raw keying material. A production proxy wires this to
// tls.ConnectionState.ExportKeyingMaterial; the default derives
// deterministically from the handshake digest and the frozen identity so a
// value can never be replayed under a different identity.
type Source func(d Digest, id Identity, label string, ctx []byte, length int) ([]byte, error)

// DefaultSource is an HKDF-expand style derivation keyed by the handshake
// digest over a canonical encoding of the identity, label and context.
func DefaultSource(d Digest, id Identity, label string, ctx []byte, length int) ([]byte, error) {
	if err := validateRequest(label, length); err != nil {
		return nil, err
	}
	info := marshalInfo(id, label, ctx)
	var out, prev []byte
	for counter := 1; len(out) < length; counter++ {
		mac := hmac.New(sha256.New, d[:])
		mac.Write(prev)
		mac.Write(info)
		mac.Write([]byte{byte(counter)})
		prev = mac.Sum(nil)
		out = append(out, prev...)
	}
	return out[:length], nil
}

func marshalInfo(id Identity, label string, ctx []byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("netsec/exporter/v1")
	writeField := func(b []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(b)))
		buf.Write(n[:])
		buf.Write(b)
	}
	writeField([]byte(id.Vhost()))
	if id.ECHAccepted {
		buf.WriteByte(1)
	} else {
		buf.WriteByte(0)
	}
	writeField([]byte(id.ECHInnerName))
	writeField([]byte(id.ECHOuterName))
	writeField([]byte(id.CertID))
	writeField([]byte(id.ALPN))
	writeField([]byte(id.PSKIdentity))
	writeField([]byte(label))
	if ctx == nil {
		buf.WriteByte(0)
	} else {
		buf.WriteByte(1)
		writeField(ctx)
	}
	return buf.Bytes()
}

// cacheKey separates exports per final virtual host, handshake digest, raw
// label bytes, context presence/length/content and requested length. A nil
// context and an empty non-nil context never share an entry.
type cacheKey struct {
	vhost   string
	digest  Digest
	label   string
	ctxNil  bool
	ctxLen  int
	ctxHash [32]byte
	length  int
}

func makeKey(id Identity, d Digest, label string, ctx []byte, length int) cacheKey {
	k := cacheKey{
		vhost:  id.Vhost(),
		digest: d,
		label:  label,
		ctxNil: ctx == nil,
		length: length,
	}
	if ctx != nil {
		k.ctxLen = len(ctx)
		k.ctxHash = sha256.Sum256(ctx)
	}
	return k
}

// Facade is a per-connection exporter facade. It is safe for concurrent use.
type Facade struct {
	src Source

	mu     sync.Mutex
	digest Digest
	gen    uint64
	frozen bool
	id     Identity
	cache  map[cacheKey][]byte
}

// NewFacade returns a facade using src, or DefaultSource when src is nil.
func NewFacade(src Source) *Facade {
	if src == nil {
		src = DefaultSource
	}
	return &Facade{src: src, cache: make(map[cacheKey][]byte)}
}

// BeginHandshake installs the immutable digest of a new handshake attempt,
// bumps the generation, drops the frozen identity and clears the cache.
func (f *Facade) BeginHandshake(d Digest) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.digest = d
	f.id = Identity{}
	f.frozen = false
	f.gen++
	f.cache = make(map[cacheKey][]byte)
}

// RecordEvent bumps the generation for an event that invalidates frozen
// decisions (0-RTT, HelloRetryRequest, ECH rejection, resumption, certificate
// hot reload), unfreezes the facade and clears the cache so stale async
// results can never populate it.
func (f *Facade) RecordEvent(ev Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frozen = false
	f.gen++
	f.cache = make(map[cacheKey][]byte)
}

// Freeze marks the handshake, certificate, SNI routing and ALPN decisions as
// final; only then may anything be exported. It may be called again within
// the same generation when the proxy re-decides routing (for example HTTP/2
// connection coalescing across virtual hosts). Any change to the handshake
// or certificate must instead go through RecordEvent or BeginHandshake.
func (f *Facade) Freeze(id Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.id = id
	f.frozen = true
}

// Generation reports the current connection generation.
func (f *Facade) Generation() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gen
}

// Export returns keying material bound to the currently frozen identity,
// serving it from the per-connection cache when possible.
func (f *Facade) Export(label string, ctx []byte, length int) ([]byte, error) {
	if err := validateRequest(label, length); err != nil {
		return nil, err
	}
	f.mu.Lock()
	if !f.frozen {
		f.mu.Unlock()
		return nil, ErrNotFrozen
	}
	key := makeKey(f.id, f.digest, label, ctx, length)
	if v, ok := f.cache[key]; ok {
		f.mu.Unlock()
		return append([]byte(nil), v...), nil
	}
	id, digest, gen := f.id, f.digest, f.gen
	f.mu.Unlock()

	v, err := f.src(digest, id, label, ctx, length)
	if err != nil {
		return nil, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if gen != f.gen {
		return nil, ErrStaleGeneration
	}
	if cached, ok := f.cache[key]; ok {
		return append([]byte(nil), cached...), nil
	}
	f.cache[key] = append([]byte(nil), v...)
	return v, nil
}

// Ticket is a pending asynchronous export bound to one generation and one
// frozen identity.
type Ticket struct {
	gen    uint64
	key    cacheKey
	digest Digest
	id     Identity
	label  string
	ctx    []byte
	length int
}

// BeginExport starts an asynchronous export for the currently frozen
// identity. Derive the value with Derive, then store it with Commit.
func (f *Facade) BeginExport(label string, ctx []byte, length int) (Ticket, error) {
	if err := validateRequest(label, length); err != nil {
		return Ticket{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.frozen {
		return Ticket{}, ErrNotFrozen
	}
	var ctxCopy []byte
	if ctx != nil {
		ctxCopy = append([]byte{}, ctx...)
	}
	return Ticket{
		gen:    f.gen,
		key:    makeKey(f.id, f.digest, label, ctx, length),
		digest: f.digest,
		id:     f.id,
		label:  label,
		ctx:    ctxCopy,
		length: length,
	}, nil
}

// Derive computes the value for a ticket. It does not touch facade state and
// is safe to run on a worker goroutine.
func (f *Facade) Derive(t Ticket) ([]byte, error) {
	return f.src(t.digest, t.id, t.label, t.ctx, t.length)
}

// Commit stores a derived value in the cache. It refuses to populate the
// cache when the connection generation has moved on since BeginExport, so a
// stale async result can never be served under a new identity.
func (f *Facade) Commit(t Ticket, value []byte) error {
	if len(value) != t.length {
		return fmt.Errorf("%w: ticket asked for %d bytes, got %d", ErrValueLength, t.length, len(value))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if t.gen != f.gen {
		return ErrStaleGeneration
	}
	if !f.frozen {
		return ErrNotFrozen
	}
	f.cache[t.key] = append([]byte(nil), value...)
	return nil
}

func validateRequest(label string, length int) error {
	if label == "" {
		return ErrEmptyLabel
	}
	if strings.HasPrefix(label, reservedLabelPrefix) {
		return fmt.Errorf("%w: %q", ErrReservedLabel, label)
	}
	if length <= 0 || length > MaxExportLength {
		return fmt.Errorf("%w: need 1..%d, got %d", ErrBadLength, MaxExportLength, length)
	}
	return nil
}
