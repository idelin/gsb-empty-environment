package netsec

import (
	"crypto/ed25519"
	"crypto/subtle"
	"fmt"
	"sync"
)

const (
	// HashSize is the SHA-256 output size used by the Noise handshake.
	HashSize = 32

	// StaticKeySize is the size of an X25519 public or private key.
	StaticKeySize = 32

	// IdentityPublicKeySize is the size of an Ed25519 public key.
	IdentityPublicKeySize = 32

	// IdentitySignatureSize is the size of an Ed25519 signature.
	IdentitySignatureSize = 64

	// IdentityPayloadSize is the exact size of an encoded identity payload.
	IdentityPayloadSize = 1 + IdentityPublicKeySize + StaticKeySize + IdentitySignatureSize

	identityPayloadVersion byte = 1
)

// IdentityPayload binds an Ed25519 identity key to the X25519 static key
// negotiated in one Noise XX handshake. Signature is made over the signing
// party's Noise static public key and the handshake hash at the point where
// the payload was added to the handshake.
//
// The encoded form is:
//
//	version (1 byte) || Ed25519 public key (32 bytes) ||
//	X25519 static public key (32 bytes) || Ed25519 signature (64 bytes)
type IdentityPayload struct {
	PublicKey ed25519.PublicKey
	StaticKey []byte
	Signature []byte
}

// PeerIdentity is the authenticated identity returned after a successful
// handshake.
type PeerIdentity = IdentityPayload

// SignedIdentityMessage returns the exact byte string that an identity
// signature authenticates. Both inputs must be fixed-size keys or hashes so
// concatenation is unambiguous.
func SignedIdentityMessage(handshakeHash, staticPublicKey []byte) ([]byte, error) {
	if len(handshakeHash) != HashSize {
		return nil, fmt.Errorf("%w: handshake hash has length %d", ErrInvalidIdentityPayload, len(handshakeHash))
	}
	if len(staticPublicKey) != StaticKeySize {
		return nil, fmt.Errorf("%w: static public key has length %d", ErrInvalidKey, len(staticPublicKey))
	}
	message := make([]byte, 0, HashSize+StaticKeySize)
	message = append(message, handshakeHash...)
	message = append(message, staticPublicKey...)
	return message, nil
}

// NewSignedIdentityPayload signs the binding between handshakeHash and
// staticPublicKey with an Ed25519 identity key.
func NewSignedIdentityPayload(handshakeHash, staticPublicKey []byte, identityPrivateKey ed25519.PrivateKey) (*IdentityPayload, error) {
	if len(identityPrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: Ed25519 private key has length %d", ErrInvalidKey, len(identityPrivateKey))
	}
	message, err := SignedIdentityMessage(handshakeHash, staticPublicKey)
	if err != nil {
		return nil, err
	}
	signature := ed25519.Sign(identityPrivateKey, message)
	publicKey := append(ed25519.PublicKey(nil), identityPrivateKey[32:]...)
	return &IdentityPayload{
		PublicKey: publicKey,
		StaticKey: append([]byte(nil), staticPublicKey...),
		Signature: append([]byte(nil), signature...),
	}, nil
}

// SignIdentityPayload signs and encodes an identity payload. It is the form
// suitable for inclusion in the second or third XX handshake message.
func SignIdentityPayload(handshakeHash, staticPublicKey []byte, identityPrivateKey ed25519.PrivateKey) ([]byte, error) {
	payload, err := NewSignedIdentityPayload(handshakeHash, staticPublicKey, identityPrivateKey)
	if err != nil {
		return nil, err
	}
	return payload.Bytes(), nil
}

// Verify checks that the payload names expectedStaticKey, was signed by the
// included Ed25519 key, and authenticates handshakeHash.
func (p *IdentityPayload) Verify(handshakeHash, expectedStaticKey []byte) error {
	if p == nil {
		return ErrIdentityPayloadMissing
	}
	if len(p.PublicKey) != IdentityPublicKeySize ||
		len(p.StaticKey) != StaticKeySize ||
		len(p.Signature) != IdentitySignatureSize {
		return ErrInvalidIdentityPayload
	}
	if len(expectedStaticKey) != StaticKeySize {
		return fmt.Errorf("%w: static public key has length %d", ErrInvalidKey, len(expectedStaticKey))
	}
	if subtle.ConstantTimeCompare(p.StaticKey, expectedStaticKey) != 1 {
		return ErrStaticKeyMismatch
	}
	message, err := SignedIdentityMessage(handshakeHash, p.StaticKey)
	if err != nil {
		return err
	}
	if !ed25519.Verify(p.PublicKey, message, p.Signature) {
		return ErrInvalidIdentitySignature
	}
	return nil
}

// Bytes returns the encoded identity payload.
func (p *IdentityPayload) Bytes() []byte {
	if p == nil {
		return nil
	}
	out := make([]byte, IdentityPayloadSize)
	out[0] = identityPayloadVersion
	copy(out[1:1+IdentityPublicKeySize], p.PublicKey)
	copy(out[1+IdentityPublicKeySize:1+IdentityPublicKeySize+StaticKeySize], p.StaticKey)
	copy(out[1+IdentityPublicKeySize+StaticKeySize:], p.Signature)
	return out
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (p *IdentityPayload) MarshalBinary() ([]byte, error) {
	if p == nil {
		return nil, ErrInvalidIdentityPayload
	}
	return p.Bytes(), nil
}

// ParseIdentityPayload decodes an identity payload and validates its
// structure. It does not verify the signature.
func ParseIdentityPayload(encoded []byte) (*IdentityPayload, error) {
	if len(encoded) != IdentityPayloadSize || encoded[0] != identityPayloadVersion {
		return nil, ErrInvalidIdentityPayload
	}
	offset := 1
	publicKey := append(ed25519.PublicKey(nil), encoded[offset:offset+IdentityPublicKeySize]...)
	offset += IdentityPublicKeySize
	staticKey := append([]byte(nil), encoded[offset:offset+StaticKeySize]...)
	offset += StaticKeySize
	signature := append([]byte(nil), encoded[offset:]...)
	return &IdentityPayload{
		PublicKey: publicKey,
		StaticKey: staticKey,
		Signature: signature,
	}, nil
}

// VerifyIdentityPayload parses encoded and verifies it against the handshake
// hash and the peer static public key observed in the handshake.
func VerifyIdentityPayload(encoded, handshakeHash, expectedStaticKey []byte) (*IdentityPayload, error) {
	if len(encoded) == 0 {
		return nil, ErrIdentityPayloadMissing
	}
	payload, err := ParseIdentityPayload(encoded)
	if err != nil {
		return nil, err
	}
	if err := payload.Verify(handshakeHash, expectedStaticKey); err != nil {
		return nil, err
	}
	return payload, nil
}

// ValidateFirstMessagePayload enforces the XX rule that message one carries
// no application payload. messageNumber is one-based.
func ValidateFirstMessagePayload(messageNumber int, payload []byte) error {
	if messageNumber == 1 && len(payload) != 0 {
		return ErrFirstMessagePayload
	}
	return nil
}

// ValidateIdentityPayloadAtCompletion is the function a handshake completion
// callback should use. It refuses to return an identity until complete is
// true and verifies the signature over handshakeHash and expectedStaticKey.
func ValidateIdentityPayloadAtCompletion(
	complete bool,
	handshakeHash, expectedStaticKey, payload []byte,
) (*PeerIdentity, error) {
	if !complete {
		return nil, ErrHandshakeIncomplete
	}
	if len(payload) == 0 {
		return nil, ErrIdentityPayloadMissing
	}
	return VerifyIdentityPayload(payload, handshakeHash, expectedStaticKey)
}

// IdentityVerifier is a stateful helper for applications that collect
// handshake messages before invoking the completion callback. It can be used
// by the callback, but the important invariant is that no identity is
// returned or stored before Complete is called.
type IdentityVerifier struct {
	mu       sync.Mutex
	identity *PeerIdentity
}

// NewIdentityVerifier creates an empty verifier.
func NewIdentityVerifier() *IdentityVerifier {
	return &IdentityVerifier{}
}

// ObserveMessage records an incoming handshake message. Message one must
// have an empty payload.
func (v *IdentityVerifier) ObserveMessage(messageNumber int, payload []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := ValidateFirstMessagePayload(messageNumber, payload); err != nil {
		return err
	}
	return nil
}

// Complete verifies and publishes the peer identity exactly once. It rejects
// calls made before the handshake is complete and never stores an identity
// whose signature or key binding is invalid.
func (v *IdentityVerifier) Complete(
	handshakeComplete bool,
	handshakeHash, peerStaticPublicKey, payload []byte,
) (*PeerIdentity, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !handshakeComplete {
		return nil, ErrHandshakeIncomplete
	}
	if v.identity != nil {
		return nil, ErrIdentityAlreadyPublished
	}
	identity, err := ValidateIdentityPayloadAtCompletion(true, handshakeHash, peerStaticPublicKey, payload)
	if err != nil {
		return nil, err
	}
	v.identity = identity
	return identity, nil
}

// Identity returns the published identity. It returns ErrHandshakeIncomplete
// until Complete has succeeded.
func (v *IdentityVerifier) Identity() (*PeerIdentity, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.identity == nil {
		return nil, ErrHandshakeIncomplete
	}
	return v.identity, nil
}
