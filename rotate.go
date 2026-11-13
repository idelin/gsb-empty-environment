package netsec

import "fmt"

// AtomicRecordStore is the persistence contract needed for key rotation.
//
// Load must return the current record. CompareAndSwap must atomically replace
// old with next only if old is still the current record. It returns false and
// leaves the stored record untouched when another writer changed it first.
// Any error from CompareAndSwap must likewise leave the current record intact.
type AtomicRecordStore interface {
	Load() (*Record, error)
	CompareAndSwap(old, next *Record) (bool, error)
}

// Reencrypt fully opens the current record, seals it under newKeyID, and then
// atomically publishes the replacement.
//
// Open verifies the old commitment and authenticates the old ciphertext before
// any new record is created. A load failure, commitment failure, AEAD failure,
// unsealable plaintext, unknown/duplicate new key, or failed atomic compare-and-
// swap leaves the old record in place.
func (envelope *Envelope) Reencrypt(store AtomicRecordStore, newKeyID string, aad []byte) error {
	if store == nil {
		return fmt.Errorf("%w: nil record store", ErrInvalidConfiguration)
	}

	oldRecord, err := store.Load()
	if err != nil {
		return err
	}
	if oldRecord == nil {
		return ErrNoRecord
	}

	plaintext, err := envelope.Open(oldRecord, aad)
	if err != nil {
		return err
	}
	defer wipeBytes(plaintext)

	newRecord, err := envelope.Seal(newKeyID, plaintext, aad)
	if err != nil {
		return err
	}

	replaced, err := store.CompareAndSwap(oldRecord, newRecord)
	if err != nil {
		return err
	}
	if !replaced {
		return ErrStoreConflict
	}
	return nil
}

// Rotate is an alias for Reencrypt.
func (envelope *Envelope) Rotate(store AtomicRecordStore, newKeyID string, aad []byte) error {
	return envelope.Reencrypt(store, newKeyID, aad)
}
