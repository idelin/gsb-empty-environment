package netsec

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"testing"
)

func assertSameRecord(t *testing.T, got, want *Record) {
	t.Helper()
	gotEncoded, err := got.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal got record: %v", err)
	}
	wantEncoded, err := want.MarshalBinary()
	if err != nil {
		t.Fatalf("marshal want record: %v", err)
	}
	if !bytes.Equal(gotEncoded, wantEncoded) {
		t.Fatal("stored record changed")
	}
}

func TestReencryptRotatesToNewKeyID(t *testing.T) {
	keyA := testKey(t, "tenant-a:v1")
	keyB := testKey(t, "tenant-a:v2")
	envelope := testEnvelope(t, testRing(t, keyA, keyB))
	plaintext := []byte("payload during rotation")
	aad := []byte("object=99")

	oldRecord := mustSeal(t, envelope, keyA.ID, plaintext, aad)
	store := NewMemoryRecordStore(oldRecord)

	if err := envelope.Reencrypt(store, keyB.ID, aad); err != nil {
		t.Fatalf("Reencrypt: %v", err)
	}

	current, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if current.KeyID != keyB.ID {
		t.Fatalf("rotated key ID = %q, want %q", current.KeyID, keyB.ID)
	}
	opened, err := envelope.Open(current, aad)
	if err != nil {
		t.Fatalf("Open rotated record: %v", err)
	}
	if !bytes.Equal(opened, plaintext) {
		t.Fatalf("rotated plaintext = %q", opened)
	}
}

type stubStore struct {
	current       *Record
	loadErr       error
	compareErr    error
	forceConflict bool
	compareCalls  int
}

func (store *stubStore) Load() (*Record, error) {
	if store.loadErr != nil {
		return nil, store.loadErr
	}
	if store.current == nil {
		return nil, ErrNoRecord
	}
	return store.current.Clone(), nil
}

func (store *stubStore) CompareAndSwap(old, next *Record) (bool, error) {
	store.compareCalls++
	if store.compareErr != nil {
		return false, store.compareErr
	}
	if store.forceConflict {
		return false, nil
	}
	currentEncoded, err := store.current.MarshalBinary()
	if err != nil {
		return false, err
	}
	oldEncoded, err := old.MarshalBinary()
	if err != nil {
		return false, err
	}
	if !bytes.Equal(currentEncoded, oldEncoded) {
		return false, nil
	}
	store.current = next.Clone()
	return true, nil
}

func TestReencryptFailuresKeepOldRecord(t *testing.T) {
	keyA := testKey(t, "tenant-a:v1")
	keyB := testKey(t, "tenant-a:v2")
	envelope := testEnvelope(t, testRing(t, keyA, keyB))
	aad := []byte("object=rotation")
	oldRecord := mustSeal(t, envelope, keyA.ID, []byte("old payload"), aad)

	tests := []struct {
		name      string
		store     *stubStore
		newKeyID  string
		mutateOld func(*Record)
		wantErr   error
	}{
		{
			name:     "load failure",
			store:    &stubStore{current: oldRecord, loadErr: errors.New("disk read failed")},
			newKeyID: keyB.ID,
		},
		{
			name: "commitment failure",
			store: &stubStore{current: func() *Record {
				record := oldRecord.Clone()
				record.Commit[0] ^= 1
				return record
			}()},
			newKeyID: keyB.ID,
			wantErr:  ErrOpen,
		},
		{
			name:     "unknown new key",
			store:    &stubStore{current: oldRecord},
			newKeyID: "tenant-missing",
			wantErr:  ErrKeyUnavailable,
		},
		{
			name:     "compare-and-swap conflict",
			store:    &stubStore{current: oldRecord, forceConflict: true},
			newKeyID: keyB.ID,
			wantErr:  ErrStoreConflict,
		},
		{
			name:     "compare-and-swap error",
			store:    &stubStore{current: oldRecord, compareErr: errors.New("write failed")},
			newKeyID: keyB.ID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := test.store.current.Clone()
			err := envelope.Reencrypt(test.store, test.newKeyID, aad)
			if test.wantErr == nil {
				if err == nil {
					t.Fatal("Reencrypt unexpectedly succeeded")
				}
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("Reencrypt error = %v, want %v", err, test.wantErr)
			}
			assertSameRecord(t, test.store.current, before)
		})
	}
}

func TestReencryptAEADFailureKeepsOldRecord(t *testing.T) {
	keyA := testKey(t, "tenant-a:v1")
	keyB := testKey(t, "tenant-a:v2")

	// Seal with a working AEAD, then rotate in an envelope whose AEAD rejects
	// every Open. The existing record must remain untouched.
	working := testEnvelope(t, testRing(t, keyA, keyB))
	aad := []byte("object=aead")
	oldRecord := mustSeal(t, working, keyA.ID, []byte("payload"), aad)
	before := oldRecord.Clone()
	store := NewMemoryRecordStore(oldRecord)

	brokenEnvelope, err := NewEnvelope(AEADFactoryFunc(func(key []byte) (cipher.AEAD, error) {
		inner, err := aesGCMFactory(key)
		if err != nil {
			return nil, err
		}
		return failOpenAEAD{inner: inner}, nil
	}), testRing(t, keyA, keyB))
	if err != nil {
		t.Fatalf("NewEnvelope: %v", err)
	}
	if err := brokenEnvelope.Reencrypt(store, keyB.ID, aad); err != ErrOpen {
		t.Fatalf("Reencrypt error = %v, want ErrOpen", err)
	}
	current, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertSameRecord(t, current, before)
}

func TestRotateAlias(t *testing.T) {
	keyA := testKey(t, "tenant-a:v1")
	keyB := testKey(t, "tenant-a:v2")
	envelope := testEnvelope(t, testRing(t, keyA, keyB))
	store := NewMemoryRecordStore(mustSeal(t, envelope, keyA.ID, []byte("payload"), nil))
	if err := envelope.Rotate(store, keyB.ID, nil); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if record.KeyID != keyB.ID {
		t.Fatalf("key ID = %q, want %q", record.KeyID, keyB.ID)
	}
}
