package netsec

import (
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"io"
)

// AEADFactory constructs an AEAD for the supplied raw key. The host owns the
// concrete cipher choice and key handling. NewAEAD must not retain mutable
// references to the supplied slice.
type AEADFactory interface {
	NewAEAD(key []byte) (cipher.AEAD, error)
}

// AEADFactoryFunc adapts a function to AEADFactory.
type AEADFactoryFunc func(key []byte) (cipher.AEAD, error)

// NewAEAD implements AEADFactory.
func (newAEAD AEADFactoryFunc) NewAEAD(key []byte) (cipher.AEAD, error) {
	return newAEAD(key)
}

// Envelope seals and opens key-committed records.
type Envelope struct {
	factory AEADFactory
	keyRing KeyRing
	random  io.Reader
}

// NewEnvelope builds an envelope around host-provided dependencies.
func NewEnvelope(factory AEADFactory, keyRing KeyRing) (*Envelope, error) {
	if factory == nil {
		return nil, fmt.Errorf("%w: nil AEAD factory", ErrInvalidConfiguration)
	}
	if keyRing == nil {
		return nil, fmt.Errorf("%w: nil key ring", ErrInvalidConfiguration)
	}
	return &Envelope{
		factory: factory,
		keyRing: keyRing,
		random:  rand.Reader,
	}, nil
}

// Seal encrypts plaintext under the unique key identified by keyID.
//
// A fresh random nonce is generated for every call. The commitment is
// independent of the AEAD's own authentication tag and binds the record
// version, key ID, nonce, AAD, and ciphertext lengths.
func (envelope *Envelope) Seal(keyID string, plaintext, aad []byte) (*Record, error) {
	if envelope == nil || envelope.factory == nil || envelope.keyRing == nil || envelope.random == nil {
		return nil, fmt.Errorf("%w: incomplete envelope", ErrInvalidConfiguration)
	}
	if err := validateKeyID(keyID); err != nil {
		return nil, err
	}
	if len(aad) > MaxAADLen {
		return nil, fmt.Errorf("%w: AAD length %d exceeds %d", ErrInputTooLarge, len(aad), MaxAADLen)
	}
	if len(plaintext) > MaxCiphertextLen {
		return nil, fmt.Errorf("%w: plaintext length %d exceeds %d", ErrInputTooLarge, len(plaintext), MaxCiphertextLen)
	}

	rawKey, ok := envelope.keyRing.LookupKey(keyID)
	if !ok || len(rawKey) != KeySize {
		return nil, fmt.Errorf("%w: %q", ErrKeyUnavailable, keyID)
	}
	key := cloneBytes(rawKey)
	defer wipeBytes(key)

	aead, err := envelope.factory.NewAEAD(key)
	if err != nil || aead == nil {
		return nil, fmt.Errorf("%w: cannot construct AEAD", ErrAEAD)
	}
	nonceSize := aead.NonceSize()
	if nonceSize <= 0 || nonceSize > MaxNonceLen {
		return nil, fmt.Errorf("%w: invalid nonce size %d", ErrAEAD, nonceSize)
	}
	if aead.Overhead() < 0 || aead.Overhead() > MaxCiphertextLen {
		return nil, fmt.Errorf("%w: invalid AEAD overhead %d", ErrAEAD, aead.Overhead())
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(envelope.random, nonce); err != nil {
		wipeBytes(nonce)
		return nil, fmt.Errorf("%w: %v", ErrRandomness, err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, aad)
	if len(ciphertext) != len(plaintext)+aead.Overhead() {
		wipeBytes(ciphertext)
		return nil, fmt.Errorf("%w: AEAD returned non-contractual ciphertext length", ErrAEAD)
	}
	if len(ciphertext) > MaxCiphertextLen {
		wipeBytes(ciphertext)
		return nil, fmt.Errorf("%w: ciphertext length %d exceeds %d", ErrInputTooLarge, len(ciphertext), MaxCiphertextLen)
	}

	commitmentKey := deriveCommitmentKey(key)
	defer wipeBytes(commitmentKey)
	commitment := computeCommitment(commitmentKey, RecordVersion, keyID, nonce, aad, ciphertext)

	return &Record{
		Version:    RecordVersion,
		KeyID:      keyID,
		Nonce:      nonce,
		Commit:     commitment,
		Ciphertext: ciphertext,
	}, nil
}

// Open verifies the key commitment before invoking AEAD.Open. Every failure
// mode returns exactly ErrOpen and a nil plaintext.
func (envelope *Envelope) Open(record *Record, aad []byte) ([]byte, error) {
	if envelope == nil || envelope.factory == nil || envelope.keyRing == nil {
		return nil, ErrOpen
	}
	if err := validateRecord(record); err != nil || len(aad) > MaxAADLen {
		return nil, ErrOpen
	}

	rawKey, ok := envelope.keyRing.LookupKey(record.KeyID)
	if !ok || len(rawKey) != KeySize {
		return nil, ErrOpen
	}
	key := cloneBytes(rawKey)
	defer wipeBytes(key)

	commitmentKey := deriveCommitmentKey(key)
	defer wipeBytes(commitmentKey)
	expected := computeCommitment(commitmentKey, record.Version, record.KeyID, record.Nonce, aad, record.Ciphertext)
	if subtle.ConstantTimeCompare(expected[:], record.Commit[:]) != 1 {
		return nil, ErrOpen
	}

	aead, err := envelope.factory.NewAEAD(key)
	if err != nil || aead == nil {
		return nil, ErrOpen
	}
	nonceSize := aead.NonceSize()
	overhead := aead.Overhead()
	if nonceSize <= 0 || nonceSize != len(record.Nonce) || overhead < 0 || len(record.Ciphertext) < overhead {
		return nil, ErrOpen
	}

	plaintext, err := aead.Open(nil, record.Nonce, record.Ciphertext, aad)
	if err != nil {
		wipeBytes(plaintext)
		return nil, ErrOpen
	}
	if len(plaintext) != len(record.Ciphertext)-overhead {
		wipeBytes(plaintext)
		return nil, ErrOpen
	}
	return plaintext, nil
}

// SealBytes seals plaintext and returns the canonical encoded record.
func (envelope *Envelope) SealBytes(keyID string, plaintext, aad []byte) ([]byte, error) {
	record, err := envelope.Seal(keyID, plaintext, aad)
	if err != nil {
		return nil, err
	}
	encoded, err := record.MarshalBinary()
	if err != nil {
		wipeBytes(record.Ciphertext)
		return nil, err
	}
	return encoded, nil
}

// OpenBytes parses a canonical record and opens it. Parse failures are mapped
// to ErrOpen so callers see the same external failure for all rejection paths.
func (envelope *Envelope) OpenBytes(encoded, aad []byte) ([]byte, error) {
	record, err := ParseRecord(encoded)
	if err != nil {
		return nil, ErrOpen
	}
	return envelope.Open(record, aad)
}

func wipeBytes(data []byte) {
	clear(data)
}
