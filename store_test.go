package netsec

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := newKeyPair(t)
	return NewStore(fixedValidator((&captureVerifier{pub: pub}).verify)), priv
}

func recordWith(seq uint64, eol time.Time) testRecord {
	r := baseRecord()
	r.sequence = seq
	r.validity = []byte(eol.Format(time.RFC3339Nano))
	return r
}

func TestStoreAcceptsHigherSequence(t *testing.T) {
	store, priv := newTestStore(t)

	if _, err := store.Put(testKey, buildRecord(t, recordWith(10, fixedNow.Add(time.Hour)), priv)); err != nil {
		t.Fatalf("first put: %v", err)
	}
	if _, err := store.Put(testKey, buildRecord(t, recordWith(11, fixedNow.Add(30*time.Minute)), priv)); err != nil {
		t.Fatalf("higher sequence must win even with earlier EOL: %v", err)
	}

	got, err := store.Get(testKey)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Sequence != 11 {
		t.Fatalf("expected sequence 11 stored, got %d", got.Sequence)
	}
}

func TestStoreRejectsOldSequenceReplay(t *testing.T) {
	store, priv := newTestStore(t)

	if _, err := store.Put(testKey, buildRecord(t, recordWith(10, fixedNow.Add(time.Hour)), priv)); err != nil {
		t.Fatalf("first put: %v", err)
	}

	replay := recordWith(9, fixedNow.Add(24*time.Hour))
	if _, err := store.Put(testKey, buildRecord(t, replay, priv)); !errors.Is(err, ErrOldSequence) {
		t.Fatalf("expected ErrOldSequence, got %v", err)
	}

	got, _ := store.Get(testKey)
	if got.Sequence != 10 {
		t.Fatalf("replay must not overwrite stored record, got sequence %d", got.Sequence)
	}
}

func TestStoreEqualSequenceLaterEOLWins(t *testing.T) {
	store, priv := newTestStore(t)

	if _, err := store.Put(testKey, buildRecord(t, recordWith(10, fixedNow.Add(time.Hour)), priv)); err != nil {
		t.Fatalf("first put: %v", err)
	}

	later := recordWith(10, fixedNow.Add(2*time.Hour))
	if _, err := store.Put(testKey, buildRecord(t, later, priv)); err != nil {
		t.Fatalf("equal sequence with later EOL must win: %v", err)
	}

	got, _ := store.Get(testKey)
	if !got.EOL().Equal(fixedNow.Add(2 * time.Hour)) {
		t.Fatalf("expected later EOL stored, got %s", got.EOL())
	}
}

func TestStoreEqualSequenceEarlierEOLLoses(t *testing.T) {
	store, priv := newTestStore(t)

	first := recordWith(10, fixedNow.Add(2*time.Hour))
	if _, err := store.Put(testKey, buildRecord(t, first, priv)); err != nil {
		t.Fatalf("first put: %v", err)
	}

	earlier := recordWith(10, fixedNow.Add(time.Hour))
	if _, err := store.Put(testKey, buildRecord(t, earlier, priv)); !errors.Is(err, ErrEqualSequence) {
		t.Fatalf("expected ErrEqualSequence, got %v", err)
	}

	got, _ := store.Get(testKey)
	if !got.EOL().Equal(fixedNow.Add(2 * time.Hour)) {
		t.Fatalf("earlier EOL must not overwrite, got %s", got.EOL())
	}
}

func TestStoreEqualSequenceEqualEOLDiscards(t *testing.T) {
	store, priv := newTestStore(t)

	first := recordWith(10, fixedNow.Add(time.Hour))
	if _, err := store.Put(testKey, buildRecord(t, first, priv)); err != nil {
		t.Fatalf("first put: %v", err)
	}

	dup := recordWith(10, fixedNow.Add(time.Hour))
	dup.value = []byte("/ipfs/bafydifferent")
	if _, err := store.Put(testKey, buildRecord(t, dup, priv)); !errors.Is(err, ErrEqualRecord) {
		t.Fatalf("expected ErrEqualRecord, got %v", err)
	}

	got, _ := store.Get(testKey)
	if string(got.Value) != string(first.value) {
		t.Fatalf("duplicate must be discarded, stored value changed")
	}
}

func TestStoreRejectsInvalidRecord(t *testing.T) {
	store, priv := newTestStore(t)

	expired := recordWith(10, fixedNow.Add(-time.Minute))
	if _, err := store.Put(testKey, buildRecord(t, expired, priv)); !errors.Is(err, ErrRecordExpired) {
		t.Fatalf("expected ErrRecordExpired, got %v", err)
	}

	if _, err := store.Get(testKey); err == nil {
		t.Fatalf("expected no record stored for key")
	}
}

func TestCompareDirect(t *testing.T) {
	makeRec := func(seq uint64, eol time.Time) *Record {
		return &Record{Sequence: seq, eol: eol}
	}

	if err := Compare(makeRec(1, fixedNow), makeRec(2, fixedNow)); err != nil {
		t.Fatalf("higher sequence should win: %v", err)
	}
	if err := Compare(makeRec(2, fixedNow), makeRec(1, fixedNow)); !errors.Is(err, ErrOldSequence) {
		t.Fatalf("expected ErrOldSequence, got %v", err)
	}
	if err := Compare(makeRec(1, fixedNow), makeRec(1, fixedNow.Add(time.Hour))); err != nil {
		t.Fatalf("later EOL should win: %v", err)
	}
	if err := Compare(makeRec(1, fixedNow.Add(time.Hour)), makeRec(1, fixedNow)); !errors.Is(err, ErrEqualSequence) {
		t.Fatalf("expected ErrEqualSequence, got %v", err)
	}
	if err := Compare(makeRec(1, fixedNow), makeRec(1, fixedNow)); !errors.Is(err, ErrEqualRecord) {
		t.Fatalf("expected ErrEqualRecord, got %v", err)
	}
}
