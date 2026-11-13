package netsec

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
)

const maxIdentifierLen = 512

// PublicIdentity is the registered, public part of a device identity.
type PublicIdentity struct {
	AccountID   string
	DeviceID    string
	SigningKey  ed25519.PublicKey
	IdentityKey *ecdh.PublicKey
}

// Validate checks that the identity has usable Ed25519 and P-256 public keys.
func (p PublicIdentity) Validate() error {
	if err := validateIdentifiers(p.AccountID, p.DeviceID); err != nil {
		return err
	}
	if len(p.SigningKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: Ed25519 public key has %d bytes", ErrInvalidIdentity, len(p.SigningKey))
	}
	if err := validateP256Public("identity key", p.IdentityKey); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidIdentity, err)
	}
	return nil
}

// Equal reports whether two registered identities are byte-for-byte equivalent.
func (p PublicIdentity) Equal(q PublicIdentity) bool {
	if p.AccountID != q.AccountID || p.DeviceID != q.DeviceID {
		return false
	}
	if !bytes.Equal(p.SigningKey, q.SigningKey) {
		return false
	}
	if p.IdentityKey == nil || q.IdentityKey == nil {
		return p.IdentityKey == nil && q.IdentityKey == nil
	}
	return bytes.Equal(p.IdentityKey.Bytes(), q.IdentityKey.Bytes())
}

func (p PublicIdentity) clone() PublicIdentity {
	c := PublicIdentity{
		AccountID:  p.AccountID,
		DeviceID:   p.DeviceID,
		SigningKey: append(ed25519.PublicKey(nil), p.SigningKey...),
	}
	if p.IdentityKey != nil {
		if k, err := cloneP256Public(p.IdentityKey); err == nil {
			c.IdentityKey = k
		}
	}
	return c
}

// DeviceIdentity holds the private keys and registered identifiers of one device.
type DeviceIdentity struct {
	accountID       string
	deviceID        string
	signingPrivate  ed25519.PrivateKey
	signingPublic   ed25519.PublicKey
	identityPrivate *ecdh.PrivateKey
	identityPublic  *ecdh.PublicKey
}

// NewDeviceIdentity generates a fresh Ed25519 signing key and P-256 identity key.
func NewDeviceIdentity(accountID, deviceID string) (*DeviceIdentity, error) {
	_, signingPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate Ed25519 key: %w", err)
	}
	identityPrivate, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate P-256 identity key: %w", err)
	}
	return NewDeviceIdentityFromKeys(accountID, deviceID, signingPrivate, identityPrivate)
}

// NewDeviceIdentityFromKeys imports an existing device key pair.
func NewDeviceIdentityFromKeys(accountID, deviceID string, signingPrivate ed25519.PrivateKey, identityPrivate *ecdh.PrivateKey) (*DeviceIdentity, error) {
	if err := validateIdentifiers(accountID, deviceID); err != nil {
		return nil, err
	}
	if len(signingPrivate) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: Ed25519 private key has %d bytes", ErrInvalidIdentity, len(signingPrivate))
	}
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, signingPrivate[:ed25519.SeedSize])
	canonicalSigning := ed25519.NewKeyFromSeed(seed)
	signingPublic, ok := canonicalSigning.Public().(ed25519.PublicKey)
	if !ok || len(signingPublic) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: invalid Ed25519 public key", ErrInvalidIdentity)
	}
	if identityPrivate == nil {
		return nil, fmt.Errorf("%w: missing P-256 private key", ErrInvalidIdentity)
	}
	canonicalIdentity, err := cloneP256Private(identityPrivate)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidIdentity, err)
	}
	identityPublic := canonicalIdentity.PublicKey()
	if err := validateP256Public("identity key", identityPublic); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidIdentity, err)
	}
	return &DeviceIdentity{
		accountID:       accountID,
		deviceID:        deviceID,
		signingPrivate:  append(ed25519.PrivateKey(nil), canonicalSigning...),
		signingPublic:   append(ed25519.PublicKey(nil), signingPublic...),
		identityPrivate: canonicalIdentity,
		identityPublic:  identityPublic,
	}, nil
}

// Public returns a defensive copy of the registered public identity.
func (d *DeviceIdentity) Public() PublicIdentity {
	return PublicIdentity{
		AccountID:  d.accountID,
		DeviceID:   d.deviceID,
		SigningKey: append(ed25519.PublicKey(nil), d.signingPublic...),
		IdentityKey: func() *ecdh.PublicKey {
			k, _ := cloneP256Public(d.identityPublic)
			return k
		}(),
	}
}

// AccountID returns the registered account identifier.
func (d *DeviceIdentity) AccountID() string { return d.accountID }

// DeviceID returns the registered device identifier.
func (d *DeviceIdentity) DeviceID() string { return d.deviceID }

// SigningPublicKey returns a copy of the Ed25519 public key.
func (d *DeviceIdentity) SigningPublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), d.signingPublic...)
}

// IdentityPublicKey returns a copy of the P-256 identity public key.
func (d *DeviceIdentity) IdentityPublicKey() *ecdh.PublicKey {
	k, _ := cloneP256Public(d.identityPublic)
	return k
}

// Sign signs an application-defined payload with the device Ed25519 key.
func (d *DeviceIdentity) Sign(message []byte) []byte {
	return ed25519.Sign(d.signingPrivate, message)
}

func validateIdentifiers(accountID, deviceID string) error {
	if accountID == "" || deviceID == "" {
		return fmt.Errorf("%w: account and device IDs are required", ErrInvalidIdentity)
	}
	if len(accountID) > maxIdentifierLen || len(deviceID) > maxIdentifierLen {
		return fmt.Errorf("%w: identifier is too long", ErrInvalidIdentity)
	}
	return nil
}

func validateP256Public(name string, key *ecdh.PublicKey) error {
	if key == nil {
		return fmt.Errorf("%w: %s is nil", ErrInvalidPoint, name)
	}
	if _, err := parseP256Public(key.Bytes()); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalidPoint, name, err)
	}
	return nil
}

func parseP256Public(encoded []byte) (*ecdh.PublicKey, error) {
	if len(encoded) == 1 && encoded[0] == 0 {
		return nil, fmt.Errorf("%w: point at infinity", ErrInvalidPoint)
	}
	if len(encoded) != 65 || encoded[0] != 4 {
		return nil, fmt.Errorf("%w: point is not canonical uncompressed P-256", ErrInvalidPoint)
	}
	key, err := ecdh.P256().NewPublicKey(encoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPoint, err)
	}
	return key, nil
}

func cloneP256Public(key *ecdh.PublicKey) (*ecdh.PublicKey, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil public key", ErrInvalidPoint)
	}
	return parseP256Public(key.Bytes())
}

func cloneP256Private(key *ecdh.PrivateKey) (*ecdh.PrivateKey, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil private key", ErrInvalidIdentity)
	}
	publicBytes := key.PublicKey().Bytes()
	if len(publicBytes) != 65 {
		return nil, fmt.Errorf("%w: private key is not P-256", ErrInvalidIdentity)
	}
	privateKey, err := ecdh.P256().NewPrivateKey(key.Bytes())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidIdentity, err)
	}
	if _, err := parseP256Public(privateKey.PublicKey().Bytes()); err != nil {
		return nil, err
	}
	return privateKey, nil
}
