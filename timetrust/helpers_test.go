package timetrust

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
)

// memoryStore is an in-memory LowerBoundStore for tests.
type memoryStore struct {
	saved  time.Time
	loaded bool
}

func (m *memoryStore) Load() (time.Time, error) {
	if !m.loaded {
		return time.Time{}, nil
	}
	return m.saved, nil
}

func (m *memoryStore) Save(bound time.Time) error {
	m.saved = bound
	m.loaded = true
	return nil
}

// errFaultyStore is the fixed failure injected by faultyStore.
var errFaultyStore = errors.New("timetrust: injected storage failure")

// faultyStore fails Save while failSave is true and records every attempted
// value so tests can assert that a failed commit never appears in Load.
type faultyStore struct {
	committed time.Time
	hasCommit bool
	failSave  bool
}

func (f *faultyStore) Load() (time.Time, error) {
	if !f.hasCommit {
		return time.Time{}, nil
	}
	return f.committed, nil
}

func (f *faultyStore) Save(bound time.Time) error {
	if f.failSave {
		return errFaultyStore
	}
	f.committed = bound
	f.hasCommit = true
	return nil
}

// recoverStore resumes normal operation while retaining earlier commits.
func (f *faultyStore) recoverStore() {
	f.failSave = false
}

type testKeyPair struct {
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newTestKeyPair(t *testing.T) testKeyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	return testKeyPair{public: pub, private: priv}
}

func newTestTrust(t *testing.T, clock Clock, store LowerBoundStore, keys testKeyPair) *Trust {
	t.Helper()
	trust, err := New(clock, Ed25519Verifier{PublicKey: keys.public}, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return trust
}

// observeSigned issues a challenge, signs a proof for it and feeds it to the
// trust component.
func observeSigned(t *testing.T, trust *Trust, keys testKeyPair, timestamp time.Time) {
	t.Helper()
	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	if err := trust.ObserveProof(SignProof(keys.private, timestamp, nonce)); err != nil {
		t.Fatalf("ObserveProof: %v", err)
	}
}

func baseTime() time.Time {
	return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
}

// notePendingLower models a lower bound tracked in memory after a durable
// commit failed: durableLower stays at the old commit while memoryLower runs
// ahead, exactly the state Checkpoint is designed to flush.
func notePendingLower(trust *Trust, durable, pending time.Time) {
	trust.durableLower = durable
	trust.memoryLower = pending
}
