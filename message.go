package netsec

import (
	"crypto/ed25519"
	"fmt"
)

// QueryKind distinguishes a full reset query from a serial query.
type QueryKind uint8

const (
	QueryReset  QueryKind = 1
	QuerySerial QueryKind = 2
)

func (k QueryKind) String() string {
	switch k {
	case QueryReset:
		return "reset"
	case QuerySerial:
		return "serial"
	default:
		return "unknown"
	}
}

// Query is a request sent by a router to a cache. Responses echo CacheID,
// ConnectionGeneration and RequestID so that they are bound to this exact
// transport and query.
type Query struct {
	CacheID              CacheID   `json:"cacheID"`
	ConnectionGeneration uint64    `json:"connectionGeneration"`
	RequestID            uint64    `json:"requestID"`
	Kind                 QueryKind `json:"kind"`
	FromSerial           uint32    `json:"fromSerial,omitempty"`
}

func (q Query) Validate() error {
	if q.CacheID == "" {
		return fmt.Errorf("%w: empty cache ID", ErrMalformedMessage)
	}
	if q.RequestID == 0 {
		return fmt.Errorf("%w: zero request ID", ErrMalformedMessage)
	}
	switch q.Kind {
	case QueryReset:
		return nil
	case QuerySerial:
		return nil
	default:
		return fmt.Errorf("%w: unknown query kind %d", ErrMalformedMessage, q.Kind)
	}
}

// MessageKind is the role of one signed response fragment.
type MessageKind uint8

const (
	MessageResetBegin MessageKind = 1
	MessageResetVRP   MessageKind = 2
	MessageResetEnd   MessageKind = 3
	MessageAdd        MessageKind = 4
	MessageRemove     MessageKind = 5
	MessageSerialEnd  MessageKind = 6
)

// Short aliases for callers that prefer "Kind".
const (
	KindResetBegin = MessageResetBegin
	KindResetVRP   = MessageResetVRP
	KindResetEnd   = MessageResetEnd
	KindAdd        = MessageAdd
	KindRemove     = MessageRemove
	KindSerialEnd  = MessageSerialEnd
)

func (k MessageKind) String() string {
	switch k {
	case MessageResetBegin:
		return "reset-begin"
	case MessageResetVRP:
		return "reset-vrp"
	case MessageResetEnd:
		return "reset-end"
	case MessageAdd:
		return "add"
	case MessageRemove:
		return "remove"
	case MessageSerialEnd:
		return "serial-end"
	default:
		return "unknown"
	}
}

func (k MessageKind) isReset() bool {
	return k == MessageResetBegin || k == MessageResetVRP || k == MessageResetEnd
}

func (k MessageKind) isDelta() bool {
	return k == MessageAdd || k == MessageRemove || k == MessageSerialEnd
}

func (k MessageKind) hasVRP() bool {
	return k == MessageResetVRP || k == MessageAdd || k == MessageRemove
}

// SignedMessage is a response fragment. Every field except Signature is
// covered by the Ed25519 signature. Index and Total allow fragments to arrive
// in any order while still being assembled deterministically.
type SignedMessage struct {
	CacheID              CacheID     `json:"cacheID"`
	ConnectionGeneration uint64      `json:"connectionGeneration"`
	RequestID            uint64      `json:"requestID"`
	Kind                 MessageKind `json:"kind"`
	Index                uint32      `json:"index"`
	Total                uint32      `json:"total"`
	Session              SessionID   `json:"session"`
	Serial               uint32      `json:"serial"`
	FromSerial           uint32      `json:"fromSerial,omitempty"`
	ToSerial             uint32      `json:"toSerial,omitempty"`
	VRP                  VRP         `json:"vrp,omitempty"`
	RootDigest           Digest      `json:"rootDigest,omitempty"`
	Signature            []byte      `json:"signature"`
}

func (m SignedMessage) copy() SignedMessage {
	m.Signature = append([]byte(nil), m.Signature...)
	return m
}

// CanonicalPayload is the exact byte stream signed by the cache.
func (m SignedMessage) CanonicalPayload() ([]byte, error) {
	if m.CacheID == "" {
		return nil, fmt.Errorf("%w: empty cache ID", ErrMalformedMessage)
	}
	if !m.Kind.isReset() && !m.Kind.isDelta() {
		return nil, fmt.Errorf("%w: unknown message kind %d", ErrMalformedMessage, m.Kind)
	}
	if m.Total == 0 || m.Index >= m.Total {
		return nil, fmt.Errorf("%w: index %d outside total %d", ErrMalformedMessage, m.Index, m.Total)
	}
	if m.Session.IsZero() {
		return nil, fmt.Errorf("%w: zero session", ErrMalformedMessage)
	}
	if m.Kind.hasVRP() {
		if _, err := canonicalVRP(m.VRP); err != nil {
			return nil, err
		}
	} else if m.VRP != (VRP{}) {
		return nil, fmt.Errorf("%w: VRP field must be empty for %s", ErrMalformedMessage, m.Kind)
	}

	buf := make([]byte, 0, 256)
	buf = append(buf, []byte("netsec/cache-message/v1\x00")...)
	buf = appendBytes(buf, []byte(m.CacheID))
	buf = appendU64(buf, m.ConnectionGeneration)
	buf = appendU64(buf, m.RequestID)
	buf = append(buf, byte(m.Kind))
	buf = appendU32(buf, m.Index)
	buf = appendU32(buf, m.Total)
	buf = append(buf, m.Session[:]...)
	buf = appendU32(buf, m.Serial)
	buf = appendU32(buf, m.FromSerial)
	buf = appendU32(buf, m.ToSerial)
	buf = append(buf, m.RootDigest[:]...)
	if m.Kind.hasVRP() {
		rec, _ := canonicalVRP(m.VRP)
		buf = append(buf, 1)
		buf = encodeVRP(buf, rec)
	} else {
		buf = append(buf, 0)
	}
	return buf, nil
}

// SignMessage signs the canonical response payload.
func SignMessage(privateKey ed25519.PrivateKey, m SignedMessage) (SignedMessage, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return SignedMessage{}, fmt.Errorf("%w: private key is %d bytes", ErrSignature, len(privateKey))
	}
	payload, err := m.CanonicalPayload()
	if err != nil {
		return SignedMessage{}, err
	}
	m.Signature = ed25519.Sign(privateKey, payload)
	return m, nil
}

// VerifyMessage checks the Ed25519 signature over the canonical payload.
func VerifyMessage(publicKey ed25519.PublicKey, m SignedMessage) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key is %d bytes", ErrSignature, len(publicKey))
	}
	if len(m.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature is %d bytes", ErrSignature, len(m.Signature))
	}
	payload, err := m.CanonicalPayload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, payload, m.Signature) {
		return ErrSignature
	}
	return nil
}

func messageEqual(a, b SignedMessage) bool {
	ab, err := a.CanonicalPayload()
	if err != nil {
		return false
	}
	bb, err := b.CanonicalPayload()
	if err != nil {
		return false
	}
	return bytesEqual(ab, bb) && bytesEqual(a.Signature, b.Signature)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
