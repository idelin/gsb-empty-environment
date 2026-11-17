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
	// NonceSize is the AES-GCM nonce size used by this package.
	NonceSize = 12
)

var (
	// ErrInvalidKey reports an AES key whose length is not 16, 24, or 32 bytes.
	ErrInvalidKey = errors.New("netsec: invalid AES key")
	// ErrInvalidRecord reports record metadata that cannot be encoded.
	ErrInvalidRecord = errors.New("netsec: invalid record")
	// ErrAuthentication reports an authentication failure. The replay window is
	// never changed when this error is returned.
	ErrAuthentication = errors.New("netsec: authentication failed")
	// ErrReplay is the parent error for records that have already been consumed
	// or that fall outside the replay window.
	ErrReplay = errors.New("netsec: replayed record")
	// ErrDuplicate reports a sequence number that was already consumed in the
	// same epoch and connection.
	ErrDuplicate = fmt.Errorf("%w: duplicate sequence", ErrReplay)
	// ErrTooOld reports a sequence number older than the 1024-record window.
	ErrTooOld = fmt.Errorf("%w: sequence is too old", ErrReplay)
	// ErrEpochUnavailable reports that the epoch is neither current, nor the
	// still-active previous epoch.
	ErrEpochUnavailable = errors.New("netsec: epoch key unavailable")
	// ErrEpochExists reports an attempt to replace a key for an existing epoch.
	ErrEpochExists = errors.New("netsec: epoch key already exists")
	// ErrEpochRollback reports an attempt to install an epoch older than the
	// current epoch.
	ErrEpochRollback = errors.New("netsec: epoch must increase")
	// ErrCannotRetireCurrent reports an attempt to retire the current epoch.
	ErrCannotRetireCurrent = errors.New("netsec: cannot retire current epoch")
	// ErrNoPreviousEpoch reports an attempt to retire when no previous epoch is
	// active.
	ErrNoPreviousEpoch = errors.New("netsec: no previous epoch")
	// ErrNilCallback reports a receive call without a delivery callback.
	ErrNilCallback = errors.New("netsec: nil delivery callback")
	// ErrDelivery wraps an error returned by a delivery callback. The record is
	// still consumed when this error is returned.
	ErrDelivery = errors.New("netsec: delivery callback failed")
)

// Record is an authenticated connection-layer record. Ciphertext contains the
// AES-GCM ciphertext followed by its authentication tag.
type Record struct {
	ConnectionID []byte
	Epoch        uint32
	Sequence     uint64
	Ciphertext   []byte
}

// RecordAAD returns the exact additional authenticated data bound to a record:
//
//	uint32be(len(connectionID)) || connectionID || uint32be(epoch) || uint64be(sequence)
func RecordAAD(connectionID []byte, epoch uint32, sequence uint64) ([]byte, error) {
	if uint64(len(connectionID)) > math.MaxUint32 {
		return nil, ErrInvalidRecord
	}

	aad := make([]byte, 4+len(connectionID)+4+8)
	binary.BigEndian.PutUint32(aad[0:4], uint32(len(connectionID)))
	copy(aad[4:], connectionID)
	offset := 4 + len(connectionID)
	binary.BigEndian.PutUint32(aad[offset:offset+4], epoch)
	binary.BigEndian.PutUint64(aad[offset+4:offset+12], sequence)
	return aad, nil
}

// RecordNonce returns uint32be(epoch) || uint64be(sequence), the 12-byte
// AES-GCM nonce for a record.
func RecordNonce(epoch uint32, sequence uint64) [NonceSize]byte {
	var nonce [NonceSize]byte
	binary.BigEndian.PutUint32(nonce[0:4], epoch)
	binary.BigEndian.PutUint64(nonce[4:12], sequence)
	return nonce
}

// SealRecord encrypts plaintext and fills record.Ciphertext. The record's
// connection ID, epoch, and sequence are authenticated as AAD and are also
// bound to the derived nonce.
func SealRecord(key []byte, record Record, plaintext []byte) (Record, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return Record{}, err
	}
	aad, err := RecordAAD(record.ConnectionID, record.Epoch, record.Sequence)
	if err != nil {
		return Record{}, err
	}
	nonce := RecordNonce(record.Epoch, record.Sequence)
	record.Ciphertext = aead.Seal(nil, nonce[:], plaintext, aad)
	return record, nil
}

// OpenRecord authenticates and decrypts one record without applying replay or
// epoch-lifecycle checks. Receiver.Receive should normally be used for inbound
// records.
func OpenRecord(key []byte, record Record) ([]byte, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	aad, err := RecordAAD(record.ConnectionID, record.Epoch, record.Sequence)
	if err != nil {
		return nil, err
	}
	nonce := RecordNonce(record.Epoch, record.Sequence)
	plaintext, err := aead.Open(nil, nonce[:], record.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthentication, err)
	}
	return plaintext, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
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
