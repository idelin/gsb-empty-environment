package netsec

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
)

const (
	// HeaderV4Public is the fixed header of every PASETO v4.public token.
	HeaderV4Public = "v4.public."

	purposeV4Public = "v4.public"
	purposeV4Local  = "v4.local"

	// maxMessageBytes is the PASETO v4 limit on the signed payload length.
	maxMessageBytes = 65535
)

var (
	// ErrMalformedToken is returned when a token is not a well-formed v4.public token.
	ErrMalformedToken = errors.New("netsec: malformed paseto token")
	// ErrInvalidFooter is returned when the footer is not a strict {"kid": ...} object.
	ErrInvalidFooter = errors.New("netsec: invalid paseto footer")
	// ErrUnknownKey is returned when the footer kid is not registered.
	ErrUnknownKey = errors.New("netsec: key id is not registered")
	// ErrKeyPurpose is returned when the footer kid maps to a non-v4.public key.
	ErrKeyPurpose = errors.New("netsec: key id is not registered for v4.public")
	// ErrKeyExists is returned when a key id is registered more than once.
	ErrKeyExists = errors.New("netsec: key id already registered")
	// ErrInvalidSignature is returned when the Ed25519 signature does not verify.
	ErrInvalidSignature = errors.New("netsec: invalid paseto signature")
)

type registryEntry struct {
	purpose string
	public  ed25519.PublicKey
}

// KeyRegistry maps footer key IDs to purpose-tagged keys. A kid registered as
// v4.local can never be selected for v4.public verification, which blocks
// footer-kid rewriting attacks that aim to cross key purposes.
type KeyRegistry struct {
	mu      sync.RWMutex
	entries map[string]registryEntry
}

// NewKeyRegistry creates an empty key registry.
func NewKeyRegistry() *KeyRegistry {
	return &KeyRegistry{entries: make(map[string]registryEntry)}
}

// RegisterPublicKey registers an Ed25519 public key for v4.public verification.
// Empty or duplicate kids and malformed keys are rejected.
func (r *KeyRegistry) RegisterPublicKey(kid string, pub ed25519.PublicKey) error {
	if kid == "" {
		return errors.New("netsec: empty key id")
	}
	if len(pub) != ed25519.PublicKeySize {
		return errors.New("netsec: invalid ed25519 public key length")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[kid]; ok {
		return fmt.Errorf("%w: %q", ErrKeyExists, kid)
	}
	r.entries[kid] = registryEntry{
		purpose: purposeV4Public,
		public:  append(ed25519.PublicKey(nil), pub...),
	}
	return nil
}

// RegisterLocalKey records a v4.local symmetric key ID so that any v4.public
// token whose footer claims this kid is rejected instead of being verified
// with the wrong-purpose key.
func (r *KeyRegistry) RegisterLocalKey(kid string, key []byte) error {
	if kid == "" {
		return errors.New("netsec: empty key id")
	}
	if len(key) != 32 {
		return errors.New("netsec: v4.local key must be 32 bytes")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[kid]; ok {
		return fmt.Errorf("%w: %q", ErrKeyExists, kid)
	}
	r.entries[kid] = registryEntry{purpose: purposeV4Local}
	return nil
}

func (r *KeyRegistry) publicKey(kid string) (ed25519.PublicKey, error) {
	r.mu.RLock()
	entry, ok := r.entries[kid]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, kid)
	}
	if entry.purpose != purposeV4Public {
		return nil, fmt.Errorf("%w: %q is registered as %s", ErrKeyPurpose, kid, entry.purpose)
	}
	return entry.public, nil
}

// Token is a verified PASETO v4.public token handed to the request auth layer.
type Token struct {
	KID     string
	Payload []byte
	Footer  []byte
}

// ParseV4Public verifies a PASETO v4.public token against the registry.
//
// The footer must be present, be valid JSON, and contain exactly one non-empty
// string "kid" field. The kid must map to a key explicitly registered as
// v4.public. Verification runs Ed25519 over
// PAE("v4.public.", payload, footer, implicitAssertion), so any footer or
// implicit-assertion tampering invalidates the signature.
func (r *KeyRegistry) ParseV4Public(token string, implicitAssertion []byte) (*Token, error) {
	if !strings.HasPrefix(token, HeaderV4Public) {
		return nil, fmt.Errorf("%w: missing v4.public header", ErrMalformedToken)
	}

	body := token[len(HeaderV4Public):]
	parts := strings.Split(body, ".")
	if len(parts) > 2 || parts[0] == "" {
		return nil, fmt.Errorf("%w: unexpected token layout", ErrMalformedToken)
	}
	if len(parts) != 2 || parts[1] == "" {
		return nil, fmt.Errorf("%w: footer with kid is required", ErrInvalidFooter)
	}

	payloadAndSig, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: payload is not base64url: %v", ErrMalformedToken, err)
	}
	footer, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: footer is not base64url: %v", ErrInvalidFooter, err)
	}

	kid, err := parseFooter(footer)
	if err != nil {
		return nil, err
	}
	pub, err := r.publicKey(kid)
	if err != nil {
		return nil, err
	}

	if len(payloadAndSig) < ed25519.SignatureSize {
		return nil, fmt.Errorf("%w: payload shorter than signature", ErrMalformedToken)
	}
	message := payloadAndSig[:len(payloadAndSig)-ed25519.SignatureSize]
	signature := payloadAndSig[len(payloadAndSig)-ed25519.SignatureSize:]
	if len(message) > maxMessageBytes {
		return nil, fmt.Errorf("%w: payload exceeds %d bytes", ErrMalformedToken, maxMessageBytes)
	}

	signed := preAuthEncode([]byte(HeaderV4Public), message, footer, implicitAssertion)
	if !ed25519.Verify(pub, signed, signature) {
		return nil, ErrInvalidSignature
	}

	return &Token{
		KID:     kid,
		Payload: append([]byte(nil), message...),
		Footer:  append([]byte(nil), footer...),
	}, nil
}

// SignV4Public mints a PASETO v4.public token. The footer must satisfy the
// same strict {"kid": "..."} policy enforced by ParseV4Public.
func SignV4Public(priv ed25519.PrivateKey, payload, footer, implicitAssertion []byte) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("netsec: invalid ed25519 private key")
	}
	if len(payload) > maxMessageBytes {
		return "", fmt.Errorf("netsec: payload exceeds %d bytes", maxMessageBytes)
	}
	if _, err := parseFooter(footer); err != nil {
		return "", err
	}

	signed := preAuthEncode([]byte(HeaderV4Public), payload, footer, implicitAssertion)
	signature := ed25519.Sign(priv, signed)

	blob := make([]byte, 0, len(payload)+ed25519.SignatureSize)
	blob = append(blob, payload...)
	blob = append(blob, signature...)

	var out strings.Builder
	out.WriteString(HeaderV4Public)
	out.WriteString(base64.RawURLEncoding.EncodeToString(blob))
	out.WriteByte('.')
	out.WriteString(base64.RawURLEncoding.EncodeToString(footer))
	return out.String(), nil
}

// parseFooter enforces that the footer is a JSON object containing exactly one
// non-empty string field named "kid". Unknown fields, duplicate kids,
// non-object JSON, non-string kids and trailing data are all rejected.
func parseFooter(footer []byte) (string, error) {
	if len(footer) == 0 {
		return "", fmt.Errorf("%w: footer is empty", ErrInvalidFooter)
	}

	dec := json.NewDecoder(bytes.NewReader(footer))
	first, err := dec.Token()
	if err != nil {
		return "", fmt.Errorf("%w: footer is not valid JSON: %v", ErrInvalidFooter, err)
	}
	delim, ok := first.(json.Delim)
	if !ok || delim != '{' {
		return "", fmt.Errorf("%w: footer must be a JSON object", ErrInvalidFooter)
	}

	var kid string
	seenKID := false
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("%w: malformed footer object: %v", ErrInvalidFooter, err)
		}
		key, ok := keyToken.(string)
		if !ok || key != "kid" {
			return "", fmt.Errorf("%w: unknown footer field %q", ErrInvalidFooter, key)
		}
		if seenKID {
			return "", fmt.Errorf("%w: duplicate kid field", ErrInvalidFooter)
		}

		valueToken, err := dec.Token()
		if err != nil {
			return "", fmt.Errorf("%w: malformed kid value: %v", ErrInvalidFooter, err)
		}
		kid, ok = valueToken.(string)
		if !ok {
			return "", fmt.Errorf("%w: kid must be a JSON string", ErrInvalidFooter)
		}
		if kid == "" {
			return "", fmt.Errorf("%w: kid must not be empty", ErrInvalidFooter)
		}
		seenKID = true
	}

	closing, err := dec.Token()
	if err != nil || closing != json.Delim('}') {
		return "", fmt.Errorf("%w: malformed footer object", ErrInvalidFooter)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("%w: trailing data after footer object", ErrInvalidFooter)
	}
	if !seenKID {
		return "", fmt.Errorf("%w: missing kid field", ErrInvalidFooter)
	}
	return kid, nil
}

// preAuthEncode implements PASETO PAE:
// LE64(count) || LE64(len(piece)) || piece ... for every piece.
func preAuthEncode(pieces ...[]byte) []byte {
	var buf bytes.Buffer
	writeLE64(&buf, len(pieces))
	for _, piece := range pieces {
		writeLE64(&buf, len(piece))
		buf.Write(piece)
	}
	return buf.Bytes()
}

func writeLE64(buf *bytes.Buffer, n int) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(n))
	buf.Write(b[:])
}
