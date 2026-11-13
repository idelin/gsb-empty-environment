package netsec

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
)

// Recipient identifies a device that can open an attachment. PublicKey is an
// X25519 key used only to wrap the per-attachment file key.
type Recipient struct {
	DeviceID  string
	PublicKey *ecdh.PublicKey
}

// NewRecipient validates and constructs a recipient descriptor.
func NewRecipient(deviceID string, publicKey *ecdh.PublicKey) (Recipient, error) {
	if deviceID == "" || len(deviceID) > MaxDeviceIDLength {
		return Recipient{}, fmt.Errorf("%w: invalid recipient device ID", ErrInvalidManifest)
	}
	if publicKey == nil {
		return Recipient{}, fmt.Errorf("%w: nil recipient public key", ErrInvalidManifest)
	}
	if len(publicKey.Bytes()) != 32 {
		return Recipient{}, fmt.Errorf("%w: bad recipient public key", ErrInvalidManifest)
	}
	var zero [32]byte
	if bytes.Equal(publicKey.Bytes(), zero[:]) {
		return Recipient{}, fmt.Errorf("%w: zero recipient public key", ErrInvalidManifest)
	}
	return Recipient{DeviceID: deviceID, PublicKey: publicKey}, nil
}

// Device holds one device's encryption and signing key material. Recipient
// devices only need EncryptionKey; sending devices need SigningKey as well.
type Device struct {
	DeviceID      string
	EncryptionKey *ecdh.PrivateKey
	SigningKey    ed25519.PrivateKey
}

// GenerateDevice creates fresh X25519 and Ed25519 key material for a device.
func GenerateDevice(deviceID string) (*Device, error) {
	return GenerateDeviceWithReader(deviceID, rand.Reader)
}

// GenerateDeviceWithReader is GenerateDevice with an injectable randomness
// source, primarily for tests.
func GenerateDeviceWithReader(deviceID string, random io.Reader) (*Device, error) {
	if deviceID == "" || len(deviceID) > MaxDeviceIDLength {
		return nil, fmt.Errorf("%w: invalid device ID", ErrInvalidManifest)
	}
	if random == nil {
		random = rand.Reader
	}
	encryptionKey, err := ecdh.X25519().GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate device encryption key: %w", err)
	}
	_, signingKey, err := ed25519.GenerateKey(random)
	if err != nil {
		return nil, fmt.Errorf("netsec: generate device signing key: %w", err)
	}
	return &Device{
		DeviceID:      deviceID,
		EncryptionKey: encryptionKey,
		SigningKey:    signingKey,
	}, nil
}

// NewDevice constructs a device from existing key material.
func NewDevice(deviceID string, encryptionKey *ecdh.PrivateKey, signingKey ed25519.PrivateKey) (*Device, error) {
	if deviceID == "" || len(deviceID) > MaxDeviceIDLength {
		return nil, fmt.Errorf("%w: invalid device ID", ErrInvalidManifest)
	}
	if encryptionKey == nil {
		return nil, fmt.Errorf("%w: nil device encryption key", ErrInvalidManifest)
	}
	if signingKey != nil && len(signingKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: bad device signing key", ErrInvalidManifest)
	}
	return &Device{
		DeviceID:      deviceID,
		EncryptionKey: encryptionKey,
		SigningKey:    append(ed25519.PrivateKey(nil), signingKey...),
	}, nil
}

// Recipient returns this device's public key-wrapping descriptor.
func (d *Device) Recipient() Recipient {
	if d == nil || d.EncryptionKey == nil {
		return Recipient{}
	}
	return Recipient{DeviceID: d.DeviceID, PublicKey: d.EncryptionKey.PublicKey()}
}

// SigningPublicKey returns this device's Ed25519 verification key.
func (d *Device) SigningPublicKey() ed25519.PublicKey {
	if d == nil || len(d.SigningKey) != ed25519.PrivateKeySize {
		return nil
	}
	return append(ed25519.PublicKey(nil), d.SigningKey.Public().(ed25519.PublicKey)...)
}
