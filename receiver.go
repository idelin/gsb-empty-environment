package netsec

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrEpochExists reports an attempt to replace a key for an epoch that is
	// still tracked. Keys for an epoch are immutable.
	ErrEpochExists = errors.New("netsec: epoch key already exists")
	// ErrEpochOrder reports an epoch that is not newer than the current epoch.
	ErrEpochOrder = errors.New("netsec: epoch must increase")
	// ErrEpochUnavailable reports an epoch outside the current/previous pair.
	ErrEpochUnavailable = errors.New("netsec: epoch unavailable")
	// ErrEpochRetired reports a previous epoch that the caller retired.
	ErrEpochRetired = errors.New("netsec: epoch retired")
	// ErrEpochNotFound reports an epoch with no tracked key.
	ErrEpochNotFound = errors.New("netsec: epoch not found")
	// ErrCannotRetireCurrent reports an attempt to retire the current epoch.
	ErrCannotRetireCurrent = errors.New("netsec: cannot retire current epoch")

	// ErrAuthentication reports an AES-GCM authentication failure. The replay
	// window is not modified when this error is returned.
	ErrAuthentication = errors.New("netsec: authentication failed")
	// ErrReplay is the parent error for duplicate and expired records.
	ErrReplay = errors.New("netsec: replay rejected")
	// ErrDuplicate reports a sequence already consumed in this epoch.
	ErrDuplicate = fmt.Errorf("%w: duplicate sequence", ErrReplay)
	// ErrTooOld reports a sequence older than the 1024-record replay window.
	ErrTooOld = fmt.Errorf("%w: sequence outside replay window", ErrReplay)

	// ErrNilDeliver reports a Receive call without a delivery callback.
	ErrNilDeliver = errors.New("netsec: nil delivery callback")
)

// DeliverFunc receives authenticated plaintext exactly once for each accepted
// record. Returning an error does not make the sequence available again.
type DeliverFunc func(plaintext []byte) error

type epochSlot struct {
	epoch   uint32
	aead    cipher.AEAD
	retired bool

	windowMu sync.Mutex
	window   replayWindow
}

// Receiver authenticates records for one connection and enforces a per-epoch
// replay window. The zero value is not usable; call NewReceiver.
type Receiver struct {
	mu       sync.RWMutex
	connID   []byte
	current  *epochSlot
	previous *epochSlot
}

// NewReceiver creates a receiver bound to connectionID. The identifier is
// copied and must match every record passed to Receive.
func NewReceiver(connectionID []byte) (*Receiver, error) {
	if err := validateConnectionID(connectionID); err != nil {
		return nil, err
	}
	connID := append([]byte(nil), connectionID...)
	return &Receiver{connID: connID}, nil
}

// New is an alias for NewReceiver.
func New(connectionID []byte) (*Receiver, error) {
	return NewReceiver(connectionID)
}

// ConnectionID returns a copy of the receiver's connection identifier.
func (r *Receiver) ConnectionID() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]byte(nil), r.connID...)
}

// Install installs the key for a new current epoch. The first installed epoch
// may be any value. Every later epoch must be strictly greater than the current
// epoch. Installing an epoch that is still tracked, including a retired
// previous epoch, returns ErrEpochExists; a key is never replaced in place.
//
// Installing a newer epoch makes the old current epoch the previous epoch and
// discards any older previous epoch. Thus at most the current and immediately
// previous epochs can authenticate records.
func (r *Receiver) Install(epoch uint32, key []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.current == nil {
		aead, err := newAEAD(key)
		if err != nil {
			return err
		}
		r.current = &epochSlot{epoch: epoch, aead: aead}
		return nil
	}

	if epoch == r.current.epoch || (r.previous != nil && epoch == r.previous.epoch) {
		return fmt.Errorf("%w: epoch %d", ErrEpochExists, epoch)
	}
	if epoch < r.current.epoch {
		return fmt.Errorf("%w: epoch %d is below current epoch %d", ErrEpochOrder, epoch, r.current.epoch)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	r.previous = r.current
	r.current = &epochSlot{epoch: epoch, aead: aead}
	return nil
}

// RotateKey is an alias for Install.
func (r *Receiver) RotateKey(epoch uint32, key []byte) error {
	return r.Install(epoch, key)
}

// Retire permanently rejects records for a previous epoch. Retiring an already
// retired previous epoch is idempotent. The current epoch cannot be retired.
func (r *Receiver) Retire(epoch uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.current != nil && epoch == r.current.epoch {
		return fmt.Errorf("%w: epoch %d", ErrCannotRetireCurrent, epoch)
	}
	if r.previous == nil || epoch != r.previous.epoch {
		return fmt.Errorf("%w: epoch %d", ErrEpochNotFound, epoch)
	}
	if r.previous.retired {
		return nil
	}

	r.previous.retired = true
	r.previous.aead = nil
	r.previous.window.reset()
	return nil
}

// Receive authenticates record, atomically claims its sequence in the epoch's
// replay window, and then invokes deliver with the plaintext.
//
// Authentication happens before the replay window is consulted, so a forged
// high sequence cannot advance or modify the window. Duplicate and too-old
// records are rejected after successful authentication and are not delivered.
//
// The sequence is consumed before deliver runs. Therefore an error returned by
// deliver is returned unchanged to the caller, while a retry of the same record
// is rejected as a duplicate. Callbacks are invoked without receiver locks, so
// a callback already claimed by an in-flight Receive may finish after Retire
// returns; records received after Retire are rejected.
func (r *Receiver) Receive(record Record, deliver DeliverFunc) error {
	if deliver == nil {
		return ErrNilDeliver
	}
	if len(record.Ciphertext) < aeadTagSize {
		return ErrAuthentication
	}
	if err := validateConnectionID(record.ConnectionID); err != nil {
		return err
	}

	r.mu.RLock()
	if !bytes.Equal(record.ConnectionID, r.connID) {
		r.mu.RUnlock()
		return ErrConnectionIDMismatch
	}

	slot, err := r.lookupLocked(record.Epoch)
	if err != nil {
		r.mu.RUnlock()
		return err
	}

	// The slot lock serializes AEAD use and replay-window updates for this
	// epoch. The receiver read lock keeps the slot from being retired or
	// discarded until the sequence has been claimed.
	slot.windowMu.Lock()
	nonce := recordNonce(record.Epoch, record.Sequence)
	aad := recordAAD(r.connID, record.Epoch, record.Sequence)
	plaintext, openErr := slot.aead.Open(nil, nonce[:], record.Ciphertext, aad)
	if openErr != nil {
		slot.windowMu.Unlock()
		r.mu.RUnlock()
		return ErrAuthentication
	}

	switch slot.window.accept(record.Sequence) {
	case replayDuplicate:
		slot.windowMu.Unlock()
		r.mu.RUnlock()
		return ErrDuplicate
	case replayTooOld:
		slot.windowMu.Unlock()
		r.mu.RUnlock()
		return ErrTooOld
	}
	slot.windowMu.Unlock()
	r.mu.RUnlock()

	return deliver(plaintext)
}

func (r *Receiver) lookupLocked(epoch uint32) (*epochSlot, error) {
	if r.current != nil && epoch == r.current.epoch {
		return r.current, nil
	}
	if r.previous != nil && epoch == r.previous.epoch {
		if r.previous.retired {
			return nil, fmt.Errorf("%w: epoch %d", ErrEpochRetired, epoch)
		}
		return r.previous, nil
	}
	return nil, fmt.Errorf("%w: epoch %d", ErrEpochUnavailable, epoch)
}
