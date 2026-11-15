package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

const (
	aeadNonceSize = 12
	aeadTagSize   = 16
)

// Record is an authenticated record as it appears on the wire. Ciphertext
// includes the AES-GCM authentication tag.
type Record struct {
	ConnectionID []byte
	Epoch        uint32
	Sequence     uint64
	Ciphertext   []byte
}

// ConnID returns the record's connection identifier.
func (r Record) ConnID() []byte {
	return r.ConnectionID
}

// Seq returns the record's sequence number.
func (r Record) Seq() uint64 {
	return r.Sequence
}

// Seal encrypts plaintext as a record for the supplied connection and epoch.
// It is provided so callers and tests can produce records using the exact AAD
// and nonce format consumed by Receiver.
func Seal(connectionID []byte, epoch uint32, sequence uint64, key, plaintext []byte) ([]byte, error) {
	if err := validateConnectionID(connectionID); err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}

	nonce := recordNonce(epoch, sequence)
	aad := recordAAD(connectionID, epoch, sequence)
	return aead.Seal(nil, nonce[:], plaintext, aad), nil
}

func validateConnectionID(connectionID []byte) error {
	if uint64(len(connectionID)) > math.MaxUint32 {
		return fmt.Errorf("%w: %d bytes exceeds uint32", ErrInvalidConnectionID, len(connectionID))
	}
	return nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	switch len(key) {
	case 16, 24, 32:
	default:
		return nil, fmt.Errorf("%w: AES key must be 16, 24, or 32 bytes, got %d", ErrInvalidKey, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidKey, err)
	}
	return aead, nil
}

func recordNonce(epoch uint32, sequence uint64) [aeadNonceSize]byte {
	var nonce [aeadNonceSize]byte
	binary.BigEndian.PutUint32(nonce[0:4], epoch)
	binary.BigEndian.PutUint64(nonce[4:12], sequence)
	return nonce
}

func recordAAD(connectionID []byte, epoch uint32, sequence uint64) []byte {
	aad := make([]byte, 4+len(connectionID)+4+8)
	binary.BigEndian.PutUint32(aad[0:4], uint32(len(connectionID)))
	copy(aad[4:], connectionID)

	offset := 4 + len(connectionID)
	binary.BigEndian.PutUint32(aad[offset:offset+4], epoch)
	binary.BigEndian.PutUint64(aad[offset+4:], sequence)
	return aad
}

var (
	// ErrInvalidConnectionID reports a connection identifier that cannot be
	// represented by the uint32 length prefix in the AAD.
	ErrInvalidConnectionID = errors.New("netsec: invalid connection ID")
	// ErrConnectionIDMismatch reports a record from a different connection.
	ErrConnectionIDMismatch = errors.New("netsec: connection ID mismatch")
	// ErrInvalidKey reports a key that is not a valid AES key.
	ErrInvalidKey = errors.New("netsec: invalid AES key")
)
