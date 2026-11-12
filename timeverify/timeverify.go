// Package timeverify validates the signed-entry timestamp (SET) embedded in a
// Cosign verification bundle and checks that the signed integratedTime is
// consistent with the signing certificate's validity window.
//
// The check is designed to defeat an attacker who holds an expired Fulcio
// certificate and tries to forge the signing time: the SET is signed by the
// transparency log and is the only trustworthy source of the inclusion time,
// so a timestamp supplied outside the signed canonical payload is never
// trusted.
package timeverify

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// DefaultSkew is the maximum amount by which an integratedTime may be in the
// future relative to the verifier's clock and still be accepted.
const DefaultSkew = 30 * time.Second

var (
	// ErrNilBundle is returned when Verify receives a nil bundle.
	ErrNilBundle = errors.New("timeverify: nil bundle")
	// ErrNilCertificate is returned when the bundle carries no leaf certificate.
	ErrNilCertificate = errors.New("timeverify: nil leaf certificate")
	// ErrInvalidLogID is returned when the entry's logID does not match the
	// SHA-256 hash of the configured log public key.
	ErrInvalidLogID = errors.New("timeverify: logID does not match configured log key")
	// ErrSETVerification is returned when the SET signature does not verify
	// against the log public key, including when the canonical payload was
	// tampered with (body, integratedTime, logIndex or logID).
	ErrSETVerification = errors.New("timeverify: SET signature verification failed")
	// ErrIntegratedTimeBeforeCert is returned when the signed integratedTime
	// predates the certificate NotBefore.
	ErrIntegratedTimeBeforeCert = errors.New("timeverify: integratedTime is before certificate NotBefore")
	// ErrIntegratedTimeAfterCert is returned when the signed integratedTime
	// is later than the certificate NotAfter, i.e. the certificate had already
	// expired when the entry was logged.
	ErrIntegratedTimeAfterCert = errors.New("timeverify: integratedTime is after certificate NotAfter")
	// ErrIntegratedTimeInFuture is returned when the signed integratedTime is
	// more than the allowed skew ahead of the verifier clock.
	ErrIntegratedTimeInFuture = errors.New("timeverify: integratedTime is too far in the future")
	// ErrUnsupportedKey is returned when the log public key is neither ECDSA
	// nor Ed25519.
	ErrUnsupportedKey = errors.New("timeverify: unsupported log public key type")
)

// Payload is the set of fields covered by the SET. It mirrors the subset of
// a Rekor hashedrekord entry whose canonical JSON is signed as the SET:
//
//	{"body":...,"integratedTime":...,"logID":...,"logIndex":...}
//
// Body is the exact base64-encoded entry body that was logged; it is stored
// and marshaled as a JSON string.
type Payload struct {
	Body           string
	IntegratedTime int64
	LogIndex       int64
	LogID          string
}

// Bundle is the minimal subset of a Cosign verification bundle required for
// timestamp validation.
type Bundle struct {
	// Leaf is the Fulcio code-signing certificate embedded in the bundle.
	Leaf *x509.Certificate
	// Payload carries the inclusion record covered by the SET.
	Payload Payload
	// SET is the raw Signed Entry Timestamp from the transparency log.
	SET []byte
}

// canonicalEntry is the deterministic, compact JSON object signed by the log.
// Field order is fixed and must match Rekor's canonical SET payload.
type canonicalEntry struct {
	Body           string `json:"body"`
	IntegratedTime int64  `json:"integratedTime"`
	LogID          string `json:"logID"`
	LogIndex       int64  `json:"logIndex"`
}

// canonicalJSON renders the SET payload as compact JSON with keys in the
// order expected by Rekor. Values are primitives/strings, so encoding/json
// produces the unambiguous canonical form with no whitespace.
func (p Payload) canonicalJSON() []byte {
	raw, err := json.Marshal(canonicalEntry{
		Body:           p.Body,
		IntegratedTime: p.IntegratedTime,
		LogID:          p.LogID,
		LogIndex:       p.LogIndex,
	})
	if err != nil {
		// All fields are basic types whose marshaling cannot fail.
		panic("timeverify: unreachable canonical JSON marshal error: " + err.Error())
	}
	return raw
}

// LogID derives the Rekor log identifier for a raw DER-encoded PKIX public
// key: the lowercase hex encoding of its SHA-256 digest.
func LogID(derPKIX []byte) string {
	sum := sha256.Sum256(derPKIX)
	return hex.EncodeToString(sum[:])
}

// LogKey pairs a transparency-log public key with the logID it represents.
type LogKey struct {
	// Public is the log key used to verify SET signatures.
	Public crypto.PublicKey
	// LogID is the expected log identifier (hex SHA-256 of the DER PKIX key).
	LogID string
}

// NewLogKey builds a LogKey from a parsed log public key by computing its
// DER PKIX encoding and deriving the logID.
func NewLogKey(pub crypto.PublicKey) (*LogKey, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return &LogKey{Public: pub, LogID: LogID(der)}, nil
}

// Verifier performs bundle timestamp validation against a fixed set of
// trusted transparency logs.
type Verifier struct {
	trusted map[string]crypto.PublicKey
	skew    time.Duration
	now     func() time.Time
}

// Option configures a Verifier.
type Option func(*Verifier)

// WithSkew overrides the maximum permitted clock skew for a future
// integratedTime.
func WithSkew(skew time.Duration) Option {
	return func(v *Verifier) { v.skew = skew }
}

// WithClock injects a custom time source, primarily for testing.
func WithClock(now func() time.Time) Option {
	return func(v *Verifier) { v.now = now }
}

// NewVerifier creates a Verifier trusting the supplied log keys. At least one
// log key is required.
func NewVerifier(keys []*LogKey, opts ...Option) (*Verifier, error) {
	if len(keys) == 0 {
		return nil, errors.New("timeverify: at least one trusted log key is required")
	}
	v := &Verifier{
		trusted: make(map[string]crypto.PublicKey, len(keys)),
		skew:    DefaultSkew,
		now:     time.Now,
	}
	for _, key := range keys {
		if key == nil {
			return nil, errors.New("timeverify: nil log key")
		}
		v.trusted[key.LogID] = key.Public
	}
	for _, opt := range opts {
		opt(v)
	}
	if v.skew < 0 {
		return nil, errors.New("timeverify: skew must be non-negative")
	}
	return v, nil
}

// Verify validates a Cosign bundle's signed timestamp. It returns nil when:
//
//   - the entry logID identifies one of the trusted logs and matches the
//     hash of that log's public key;
//   - the SET verifies over the canonical JSON of body, integratedTime,
//     logIndex and logID using that log key;
//   - the signed integratedTime is within the leaf certificate validity
//     window (NotBefore <= integratedTime <= NotAfter);
//   - the integratedTime is not more than the allowed skew in the future.
//
// The returned errors wrap the package sentinel errors and support errors.Is.
func (v *Verifier) Verify(b *Bundle) error {
	if b == nil {
		return ErrNilBundle
	}
	if b.Leaf == nil {
		return ErrNilCertificate
	}

	logKey, ok := v.trusted[b.Payload.LogID]
	if !ok {
		return ErrInvalidLogID
	}
	// Defense in depth: even a key present in the trusted map must hash to
	// the claimed logID, so swapping the logID onto another entry is rejected.
	der, err := x509.MarshalPKIXPublicKey(logKey)
	if err != nil {
		return err
	}
	if LogID(der) != b.Payload.LogID {
		return ErrInvalidLogID
	}

	canonical := b.Payload.canonicalJSON()
	if err := verifySET(logKey, canonical, b.SET); err != nil {
		return err
	}

	integratedAt := time.Unix(b.Payload.IntegratedTime, 0).UTC()
	if integratedAt.Before(b.Leaf.NotBefore) {
		return ErrIntegratedTimeBeforeCert
	}
	if integratedAt.After(b.Leaf.NotAfter) {
		return ErrIntegratedTimeAfterCert
	}
	if integratedAt.After(v.now().Add(v.skew)) {
		return ErrIntegratedTimeInFuture
	}
	return nil
}

// verifySET checks the SET signature over the SHA-256 digest of the canonical
// payload for the key types Rekor currently uses.
func verifySET(key crypto.PublicKey, canonical, sig []byte) error {
	digest := sha256.Sum256(canonical)
	switch pub := key.(type) {
	case *ecdsa.PublicKey:
		if !ecdsa.VerifyASN1(pub, digest[:], sig) {
			return ErrSETVerification
		}
		return nil
	case ed25519.PublicKey:
		// Ed25519 signs the message directly rather than a prehash.
		if !ed25519.Verify(pub, canonical, sig) {
			return ErrSETVerification
		}
		return nil
	default:
		return ErrUnsupportedKey
	}
}

// SignSET is a helper for log operators and tests: it produces a SET over the
// canonical payload using the same signing scheme verifySET expects.
// ECDSA (P-256/P-384/P-521) and Ed25519 private keys are supported.
func SignSET(key crypto.PrivateKey, p Payload) ([]byte, error) {
	canonical := p.canonicalJSON()
	switch priv := key.(type) {
	case *ecdsa.PrivateKey:
		digest := sha256.Sum256(canonical)
		return ecdsa.SignASN1(rand.Reader, priv, digest[:])
	case ed25519.PrivateKey:
		return ed25519.Sign(priv, canonical), nil
	default:
		return nil, ErrUnsupportedKey
	}
}

// EncodeBody returns the base64 encoding used for the canonical "body" field.
func EncodeBody(rawBody []byte) string {
	return base64.StdEncoding.EncodeToString(rawBody)
}
