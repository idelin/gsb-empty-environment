package netsec

import (
	"crypto"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"
)

const requestContextLength = 32

// Request is an outstanding RFC 9261 authenticator request. Its fields are
// intentionally opaque: the verifier must redeem the request returned by
// IssueRequest so that the exact signed bytes and pending state cannot be
// forged.
type Request struct {
	ledger  *requestLedger
	id      string
	wire    []byte
	context []byte
	role    Role
}

// Context returns a copy of certificate_request_context.
func (r Request) Context() []byte {
	return append([]byte(nil), r.context...)
}

// Message returns a copy of the request encoded as a TLS handshake message,
// ready to send to the authenticator producer.
func (r Request) Message() []byte {
	return append([]byte(nil), r.wire...)
}

// Role returns the endpoint that must create the authenticator in response to
// this request.
func (r Request) Role() Role {
	return r.role
}

func (r Request) pending(now time.Time) error {
	if r.ledger == nil {
		return ErrRequestNotFound
	}
	return r.ledger.check(r.id, now, false)
}

func (r Request) redeem(now time.Time) error {
	if r.ledger == nil {
		return ErrRequestNotFound
	}
	return r.ledger.check(r.id, now, true)
}

type requestEntry struct {
	context []byte
	wire    []byte
	role    Role
	issued  time.Time
	used    bool
}

type requestLedger struct {
	mu      sync.Mutex
	entries map[string]*requestEntry
	ttl     time.Duration
	rng     io.Reader
	now     func() time.Time
	hash    crypto.Hash
}

func newRequestLedger(ttl time.Duration, rng io.Reader, now func() time.Time, hash crypto.Hash) *requestLedger {
	return &requestLedger{
		entries: make(map[string]*requestEntry),
		ttl:     ttl,
		rng:     rng,
		now:     now,
		hash:    hash,
	}
}

func (l *requestLedger) issue(role Role) (Request, error) {
	messageType, err := role.requestMessageType()
	if err != nil {
		return Request{}, err
	}

	for attempts := 0; attempts < 4; attempts++ {
		context := make([]byte, requestContextLength)
		if _, err := io.ReadFull(l.rng, context); err != nil {
			return Request{}, fmt.Errorf("netsec: generate request context: %w", err)
		}
		wire, err := encodeAuthenticatorRequest(messageType, context)
		if err != nil {
			return Request{}, err
		}
		id := string(context)

		l.mu.Lock()
		if _, exists := l.entries[id]; exists {
			l.mu.Unlock()
			continue
		}
		entry := &requestEntry{
			context: append([]byte(nil), context...),
			wire:    wire,
			role:    role,
			issued:  l.now(),
		}
		l.entries[id] = entry
		l.mu.Unlock()

		return Request{
			ledger:  l,
			id:      id,
			wire:    append([]byte(nil), wire...),
			context: append([]byte(nil), context...),
			role:    role,
		}, nil
	}
	return Request{}, fmt.Errorf("netsec: could not allocate a unique request context")
}

func (l *requestLedger) check(id string, now time.Time, consume bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.entries[id]
	if !ok {
		return ErrRequestNotFound
	}
	if entry.used {
		return ErrRequestUsed
	}
	if l.ttl > 0 && now.Sub(entry.issued) > l.ttl {
		return ErrRequestExpired
	}
	if consume {
		entry.used = true
	}
	return nil
}

func encodeAuthenticatorRequest(messageType byte, context []byte) ([]byte, error) {
	if len(context) > 255 {
		return nil, fmt.Errorf("%w: request context too long", ErrMalformed)
	}

	extensions := make([]byte, 0, 32)
	extensions = append(extensions, 0x00, 0x0d) // signature_algorithms
	schemes := []SignatureScheme{
		SignatureEd25519,
		SignatureECDSASecp256r1SHA256,
		SignatureECDSASecp384r1SHA384,
		SignatureECDSASecp521r1SHA512,
		SignatureRSAPSSRSAESHA256,
		SignatureRSAPSSRSAESHA384,
		SignatureRSAPSSRSAESHA512,
		SignatureRSAPSSPSSSHA256,
		SignatureRSAPSSPSSSHA384,
		SignatureRSAPSSPSSSHA512,
	}
	schemeBytes := make([]byte, 0, 2+2*len(schemes))
	schemeBytes = binary.BigEndian.AppendUint16(schemeBytes, uint16(2*len(schemes)))
	for _, scheme := range schemes {
		schemeBytes = binary.BigEndian.AppendUint16(schemeBytes, uint16(scheme))
	}
	extensions = binary.BigEndian.AppendUint16(extensions, uint16(len(schemeBytes)))
	extensions = append(extensions, schemeBytes...)

	body := make([]byte, 0, 1+len(context)+2+len(extensions))
	body = append(body, byte(len(context)))
	body = append(body, context...)
	body = binary.BigEndian.AppendUint16(body, uint16(len(extensions)))
	body = append(body, extensions...)
	return marshalHandshake(messageType, body)
}

// Exporter obtains connection-bound key material. *tls.ConnectionState
// implements this interface.
type Exporter interface {
	ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error)
}

// ExporterFunc adapts a function to Exporter.
type ExporterFunc func(label string, context []byte, length int) ([]byte, error)

// ExportKeyingMaterial implements Exporter.
func (f ExporterFunc) ExportKeyingMaterial(label string, context []byte, length int) ([]byte, error) {
	return f(label, context, length)
}

var defaultRNG io.Reader = rand.Reader
