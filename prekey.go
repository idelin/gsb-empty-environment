package netsec

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"time"
)

const bundleSignatureDomain = "example.org/netsec/q0154/prekey-bundle/v1"

// SignedPreKey is the long-lived signed prekey of a receiver.
type SignedPreKey struct {
	ID         uint32
	PrivateKey *ecdh.PrivateKey
}

// NewSignedPreKey generates a fresh P-256 signed prekey.
func NewSignedPreKey(id uint32) (*SignedPreKey, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: signed prekey ID must be non-zero", ErrInvalidBundle)
	}
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate signed prekey: %w", err)
	}
	return &SignedPreKey{ID: id, PrivateKey: privateKey}, nil
}

// NewSignedPreKeyFromPrivate imports an existing P-256 signed prekey.
func NewSignedPreKeyFromPrivate(id uint32, privateKey *ecdh.PrivateKey) (*SignedPreKey, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: signed prekey ID must be non-zero", ErrInvalidBundle)
	}
	canonical, err := cloneP256Private(privateKey)
	if err != nil {
		return nil, err
	}
	return &SignedPreKey{ID: id, PrivateKey: canonical}, nil
}

// PublicKey returns the signed prekey public point.
func (s *SignedPreKey) PublicKey() *ecdh.PublicKey {
	if s == nil || s.PrivateKey == nil {
		return nil
	}
	return s.PrivateKey.PublicKey()
}

// OneTimePreKey is an optional, single-use prekey of a receiver.
type OneTimePreKey struct {
	ID         uint32
	PrivateKey *ecdh.PrivateKey
}

// NewOneTimePreKey generates a fresh P-256 one-time prekey.
func NewOneTimePreKey(id uint32) (*OneTimePreKey, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: one-time prekey ID must be non-zero", ErrInvalidBundle)
	}
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate one-time prekey: %w", err)
	}
	return &OneTimePreKey{ID: id, PrivateKey: privateKey}, nil
}

// NewOneTimePreKeyFromPrivate imports an existing P-256 one-time prekey.
func NewOneTimePreKeyFromPrivate(id uint32, privateKey *ecdh.PrivateKey) (*OneTimePreKey, error) {
	if id == 0 {
		return nil, fmt.Errorf("%w: one-time prekey ID must be non-zero", ErrInvalidBundle)
	}
	canonical, err := cloneP256Private(privateKey)
	if err != nil {
		return nil, err
	}
	return &OneTimePreKey{ID: id, PrivateKey: canonical}, nil
}

// PublicKey returns the one-time prekey public point.
func (o *OneTimePreKey) PublicKey() *ecdh.PublicKey {
	if o == nil || o.PrivateKey == nil {
		return nil
	}
	return o.PrivateKey.PublicKey()
}

// PreKeyBundle is the directory payload published by a receiver.
type PreKeyBundle struct {
	Version         uint16
	AccountID       string
	DeviceID        string
	SigningKey      ed25519.PublicKey
	IdentityKey     *ecdh.PublicKey
	SignedPreKeyID  uint32
	SignedPreKey    *ecdh.PublicKey
	OneTimePreKeyID uint32
	OneTimePreKey   *ecdh.PublicKey
	ExpiresAt       time.Time
	Signature       []byte
}

// BuildPreKeyBundle constructs and signs a bundle for identity.
func BuildPreKeyBundle(identity *DeviceIdentity, signedPreKey *SignedPreKey, oneTimePreKey *OneTimePreKey, expiresAt time.Time) (*PreKeyBundle, error) {
	if identity == nil {
		return nil, fmt.Errorf("%w: missing receiver identity", ErrInvalidBundle)
	}
	if signedPreKey == nil || signedPreKey.PrivateKey == nil {
		return nil, fmt.Errorf("%w: missing signed prekey", ErrInvalidBundle)
	}
	public := identity.Public()
	bundle := &PreKeyBundle{
		Version:        ProtocolVersion,
		AccountID:      public.AccountID,
		DeviceID:       public.DeviceID,
		SigningKey:     public.SigningKey,
		IdentityKey:    public.IdentityKey,
		SignedPreKeyID: signedPreKey.ID,
		SignedPreKey:   signedPreKey.PublicKey(),
		ExpiresAt:      expiresAt,
	}
	if oneTimePreKey != nil {
		if oneTimePreKey.PrivateKey == nil {
			return nil, fmt.Errorf("%w: missing one-time prekey private key", ErrInvalidBundle)
		}
		bundle.OneTimePreKeyID = oneTimePreKey.ID
		bundle.OneTimePreKey = oneTimePreKey.PublicKey()
	}
	if err := bundle.Sign(identity); err != nil {
		return nil, err
	}
	return bundle, nil
}

// Sign signs the bundle with the receiver Ed25519 key.
func (b *PreKeyBundle) Sign(identity *DeviceIdentity) error {
	if b == nil || identity == nil {
		return fmt.Errorf("%w: missing bundle or signer", ErrInvalidBundle)
	}
	b.Version = ProtocolVersion
	if err := b.validateStructure(); err != nil {
		return err
	}
	if !bytes.Equal(b.SigningKey, identity.SigningPublicKey()) {
		return fmt.Errorf("%w: bundle signing key does not match signer", ErrInvalidBundle)
	}
	b.Version = ProtocolVersion
	b.Signature = ed25519.Sign(identity.signingPrivate, b.signedBytes())
	return nil
}

// VerifySignature checks structure and the Ed25519 bundle signature.
func (b *PreKeyBundle) VerifySignature() error {
	if b == nil {
		return fmt.Errorf("%w: nil bundle", ErrInvalidBundle)
	}
	if err := b.validateStructure(); err != nil {
		return err
	}
	if len(b.Signature) != ed25519.SignatureSize {
		return fmt.Errorf("%w: bundle signature has %d bytes", ErrInvalidSignature, len(b.Signature))
	}
	if !ed25519.Verify(b.SigningKey, b.signedBytes(), b.Signature) {
		return ErrInvalidSignature
	}
	return nil
}

// Verify checks the signature and that the bundle has not expired at now.
func (b *PreKeyBundle) Verify(now time.Time) error {
	if err := b.VerifySignature(); err != nil {
		return err
	}
	if b.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: missing expiry", ErrInvalidBundle)
	}
	if !now.Before(b.ExpiresAt) {
		return fmt.Errorf("%w: expiry %s", ErrBundleExpired, b.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func (b *PreKeyBundle) validateStructure() error {
	if b == nil {
		return fmt.Errorf("%w: nil bundle", ErrInvalidBundle)
	}
	if b.Version != ProtocolVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidBundle, b.Version)
	}
	if err := validateIdentifiers(b.AccountID, b.DeviceID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	if len(b.SigningKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: invalid Ed25519 signing key", ErrInvalidBundle)
	}
	if err := validateP256Public("bundle identity key", b.IdentityKey); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	if b.SignedPreKeyID == 0 {
		return fmt.Errorf("%w: invalid signed prekey ID", ErrInvalidBundle)
	}
	if err := validateP256Public("signed prekey", b.SignedPreKey); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
	}
	if b.OneTimePreKey == nil {
		if b.OneTimePreKeyID != 0 {
			return fmt.Errorf("%w: one-time prekey ID without a key", ErrInvalidBundle)
		}
	} else {
		if b.OneTimePreKeyID == 0 {
			return fmt.Errorf("%w: invalid one-time prekey ID", ErrInvalidBundle)
		}
		if err := validateP256Public("one-time prekey", b.OneTimePreKey); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidBundle, err)
		}
	}
	return nil
}

func (b *PreKeyBundle) signedBytes() []byte {
	w := &wireBuilder{}
	w.text(bundleSignatureDomain)
	w.u16(b.Version)
	w.text(b.AccountID)
	w.text(b.DeviceID)
	w.blob(b.SigningKey)
	w.blob(pointBytes(b.IdentityKey))
	w.u32(b.SignedPreKeyID)
	w.blob(pointBytes(b.SignedPreKey))
	w.boolean(b.OneTimePreKey != nil)
	w.u32(b.OneTimePreKeyID)
	if b.OneTimePreKey == nil {
		w.blob(nil)
	} else {
		w.blob(pointBytes(b.OneTimePreKey))
	}
	w.i64(b.ExpiresAt.UTC().UnixNano())
	return w.bytes()
}

// SignedBytes returns the exact canonical bytes covered by the bundle signature.
func (b *PreKeyBundle) SignedBytes() []byte {
	if b == nil {
		return nil
	}
	return b.signedBytes()
}

// Digest returns the SHA-256 digest of the signed bundle content.
func (b *PreKeyBundle) Digest() [32]byte {
	if b == nil {
		return [32]byte{}
	}
	return sha256.Sum256(b.signedBytes())
}

// RegisteredIdentity returns the receiver identity asserted by the bundle.
func (b *PreKeyBundle) RegisteredIdentity() (PublicIdentity, error) {
	if err := b.validateStructure(); err != nil {
		return PublicIdentity{}, err
	}
	identityKey, err := cloneP256Public(b.IdentityKey)
	if err != nil {
		return PublicIdentity{}, err
	}
	return PublicIdentity{
		AccountID:   b.AccountID,
		DeviceID:    b.DeviceID,
		SigningKey:  append(ed25519.PublicKey(nil), b.SigningKey...),
		IdentityKey: identityKey,
	}, nil
}

// Clone returns a deep copy of the bundle, including public points and signature.
func (b *PreKeyBundle) Clone() *PreKeyBundle {
	if b == nil {
		return nil
	}
	c := *b
	c.SigningKey = append(ed25519.PublicKey(nil), b.SigningKey...)
	c.Signature = append([]byte(nil), b.Signature...)
	if b.IdentityKey != nil {
		c.IdentityKey, _ = cloneP256Public(b.IdentityKey)
	}
	if b.SignedPreKey != nil {
		c.SignedPreKey, _ = cloneP256Public(b.SignedPreKey)
	}
	if b.OneTimePreKey != nil {
		c.OneTimePreKey, _ = cloneP256Public(b.OneTimePreKey)
	}
	return &c
}

func pointBytes(key *ecdh.PublicKey) []byte {
	if key == nil {
		return nil
	}
	return key.Bytes()
}
