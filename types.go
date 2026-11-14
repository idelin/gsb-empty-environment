package netsec

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// RequestKind identifies the parsed Kerberos request type whose FAST state is
// being managed.
type RequestKind uint8

const (
	RequestKindAS RequestKind = iota + 1
	RequestKindTGS
)

func (k RequestKind) valid() bool {
	return k == RequestKindAS || k == RequestKindTGS
}

// Principal is the strict-DER-parsed form of a Kerberos principal name. The
// component slice is deliberately kept separate from the realm so identity
// comparisons never depend on an ambiguous display-name encoding.
type Principal struct {
	NameType   int32
	Components []string
}

func (p Principal) valid() bool {
	if p.NameType < 0 || len(p.Components) == 0 || len(p.Components) > 64 {
		return false
	}
	for _, component := range p.Components {
		if component == "" || len(component) > 1024 {
			return false
		}
	}
	return true
}

func (p Principal) clone() Principal {
	components := make([]string, len(p.Components))
	copy(components, p.Components)
	return Principal{NameType: p.NameType, Components: components}
}

func principalsEqual(a, b Principal) bool {
	if a.NameType != b.NameType || len(a.Components) != len(b.Components) {
		return false
	}
	for i := range a.Components {
		if a.Components[i] != b.Components[i] {
			return false
		}
	}
	return true
}

// ArmorTicket identifies the FAST armor ticket session. SessionID must be
// derived by the caller from the parsed ticket/session (for example, a hash of
// the ticket and session-key identifier); it is never treated as a display
// name.
type ArmorTicket struct {
	SessionID []byte
	Realm     string
	Client    Principal
}

func (a ArmorTicket) valid() bool {
	return len(a.SessionID) > 0 &&
		len(a.SessionID) <= 4096 &&
		a.Realm != "" &&
		len(a.Realm) <= 2048 &&
		a.Client.valid()
}

func (a ArmorTicket) clone() ArmorTicket {
	return ArmorTicket{
		SessionID: cloneBytes(a.SessionID),
		Realm:     a.Realm,
		Client:    a.Client.clone(),
	}
}

// FinishedChecksum is a raw checksum received from a parsed FAST request. The
// frontend supplies a FinishedVerifier to authenticate it; this package does
// not implement Kerberos checksum encodings.
type FinishedChecksum struct {
	Type  int32
	Value []byte
}

func (f FinishedChecksum) valid() bool {
	return len(f.Value) > 0 && len(f.Value) <= 64<<10
}

// RequestBodyDigest is a SHA-256 digest of the strictly parsed KDC-REQ-BODY.
// Keeping the body stable across pre-authentication rounds is important:
// retries normally change padata (and therefore the finished checksum) but not
// the request body.
type RequestBodyDigest [sha256.Size]byte

// DigestRequestBody hashes a strict-DER-encoded KDC-REQ-BODY. Callers that
// already received a digest from a strict parser may construct
// RequestBodyDigest directly.
func DigestRequestBody(bodyDER []byte) RequestBodyDigest {
	return RequestBodyDigest(sha256.Sum256(bodyDER))
}

// Request is the strict parser's AS-REQ or TGS-REQ view. The package stores
// only the fields below; EncryptedField and UnknownDER are accepted as opaque
// input and are never included in a cookie or echoed in an error.
type Request struct {
	Kind RequestKind

	Client      Principal
	OuterRealm  string
	TargetRealm string

	Nonce      uint32
	Enctype    int32
	BodyDigest RequestBodyDigest

	Armor    ArmorTicket
	Finished FinishedChecksum

	// EncryptedField is the raw encrypted pre-authentication field from the
	// parsed message (for example PA-ENC-TS-ENC). Its bytes are not interpreted.
	EncryptedField []byte

	// UnknownDER contains parser-recognised unknown fields. It is intentionally
	// ignored by session identity and is never copied to a cookie or error.
	UnknownDER []byte
}

func (r Request) clone() Request {
	r.Client = r.Client.clone()
	r.Armor = r.Armor.clone()
	r.Finished.Value = cloneBytes(r.Finished.Value)
	r.EncryptedField = cloneBytes(r.EncryptedField)
	r.UnknownDER = cloneBytes(r.UnknownDER)
	return r
}

func (r Request) validForBinding() bool {
	return r.Kind.valid() &&
		r.Client.valid() &&
		r.OuterRealm != "" &&
		len(r.OuterRealm) <= 2048 &&
		r.TargetRealm != "" &&
		len(r.TargetRealm) <= 2048 &&
		r.Enctype > 0 &&
		r.Armor.valid() &&
		len(r.EncryptedField) <= 16<<20 &&
		len(r.UnknownDER) <= 16<<20
}

func (r Request) validForVerification() bool {
	return r.validForBinding() && r.Finished.valid()
}

// ASReq is a strict-DER parser's AS-REQ view accepted by the frontend adapter.
// It does not contain a general ASN.1 encoder or decoder.
type ASReq struct {
	Client         Principal
	OuterRealm     string
	TargetRealm    string
	Nonce          uint32
	Enctype        int32
	BodyDigest     RequestBodyDigest
	Armor          ArmorTicket
	Finished       FinishedChecksum
	EncryptedField []byte
	UnknownDER     []byte
}

// ToRequest converts the parsed AS-REQ view into the common session binding.
func (r ASReq) ToRequest() Request {
	return Request{
		Kind:           RequestKindAS,
		Client:         r.Client,
		OuterRealm:     r.OuterRealm,
		TargetRealm:    r.TargetRealm,
		Nonce:          r.Nonce,
		Enctype:        r.Enctype,
		BodyDigest:     r.BodyDigest,
		Armor:          r.Armor,
		Finished:       r.Finished,
		EncryptedField: r.EncryptedField,
		UnknownDER:     r.UnknownDER,
	}
}

// TGSReq is a strict-DER parser's TGS-REQ view accepted by the frontend
// adapter.
type TGSReq struct {
	Client         Principal
	OuterRealm     string
	TargetRealm    string
	Nonce          uint32
	Enctype        int32
	BodyDigest     RequestBodyDigest
	Armor          ArmorTicket
	Finished       FinishedChecksum
	EncryptedField []byte
	UnknownDER     []byte
}

// ToRequest converts the parsed TGS-REQ view into the common session binding.
func (r TGSReq) ToRequest() Request {
	return Request{
		Kind:           RequestKindTGS,
		Client:         r.Client,
		OuterRealm:     r.OuterRealm,
		TargetRealm:    r.TargetRealm,
		Nonce:          r.Nonce,
		Enctype:        r.Enctype,
		BodyDigest:     r.BodyDigest,
		Armor:          r.Armor,
		Finished:       r.Finished,
		EncryptedField: r.EncryptedField,
		UnknownDER:     r.UnknownDER,
	}
}

// ParsedRequest is implemented by ASReq and TGSReq.
type ParsedRequest interface {
	ToRequest() Request
}

// Phase is the explicit multi-step state of a FAST pre-authentication
// session. Phases may be advanced to the same or a later phase; they may never
// move backwards.
type Phase uint8

const (
	PhasePreAuthPending Phase = iota + 1
	PhasePreAuthVerified
	PhaseReady
)

func (p Phase) valid() bool {
	return p >= PhasePreAuthPending && p <= PhaseReady
}

func (p Phase) rank() int {
	return int(p)
}

// FinishedVerifier authenticates the raw finished checksum using the armor
// ticket session key. Implementations must verify that the checksum covers the
// supplied request body digest. Errors are deliberately collapsed into
// ErrSessionRejected by Manager.
type FinishedVerifier interface {
	VerifyFinished(context.Context, Request) error
}

// FinishedVerifierFunc adapts a function to FinishedVerifier.
type FinishedVerifierFunc func(context.Context, Request) error

// VerifyFinished implements FinishedVerifier.
func (f FinishedVerifierFunc) VerifyFinished(ctx context.Context, req Request) error {
	return f(ctx, req)
}

var (
	// ErrSessionRejected is returned for every cookie, binding, expiry,
	// checksum, realm, or replay failure. Callers must not expect to distinguish
	// which check failed.
	ErrSessionRejected = errors.New("FAST session rejected")

	// ErrInvalidConfiguration reports construction-time configuration errors.
	ErrInvalidConfiguration = errors.New("FAST session configuration is invalid")

	// ErrInvalidKey reports a malformed key supplied to a Keyring.
	ErrInvalidKey = errors.New("FAST key is invalid")

	// ErrKeyGenerationRollback reports an attempt to move a Keyring to an older
	// or equal generation.
	ErrKeyGenerationRollback = errors.New("FAST key generation rollback")

	// ErrKeyNotFound reports activation of a generation for which no key was
	// installed.
	ErrKeyNotFound = errors.New("FAST key generation not found")
)

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func appendUint16(dst []byte, value uint16) []byte {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], value)
	return append(dst, buf[:]...)
}

func appendUint32(dst []byte, value uint32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], value)
	return append(dst, buf[:]...)
}

func appendUint64(dst []byte, value uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], value)
	return append(dst, buf[:]...)
}

func appendInt32(dst []byte, value int32) []byte {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], uint32(value))
	return append(dst, buf[:]...)
}

func appendBytes(dst, value []byte) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendString(dst []byte, value string) []byte {
	return appendBytes(dst, []byte(value))
}
