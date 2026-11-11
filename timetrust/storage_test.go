package timetrust

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFailedSaveDoesNotExtendValidity(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &faultyStore{failSave: true}
	trust := newTestTrust(t, clock, store, keys)

	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	proof := SignProof(keys.private, baseTime().Add(time.Hour), nonce)
	err = trust.ObserveProof(proof)
	if !errors.Is(err, errFaultyStore) {
		t.Fatalf("ObserveProof error = %v, want injected failure", err)
	}

	// The failed commit is reported, nothing was durably committed and no
	// upper bound was anchored: the host must block.
	if store.hasCommit {
		t.Fatalf("failed save unexpectedly committed a value")
	}
	if got := trust.DurableLower(); !got.IsZero() {
		t.Fatalf("durable lower bound advanced despite failed save: %v", got)
	}
	if got := trust.Evaluate(baseTime().Add(2 * time.Hour)); got != DecisionUnknown {
		t.Fatalf("decision after failed save = %s, want unknown", got)
	}
	if _, _, hasLower, hasUpper := trust.TrustedInterval(); hasLower || hasUpper {
		t.Fatalf("failed save produced trusted bounds")
	}

	// The attempted timestamp never becomes visible through Load.
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.IsZero() {
		t.Fatalf("Load after failed save = %v, want zero", loaded)
	}
}

func TestFailingStorePreservesPreviousCommit(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &faultyStore{}
	trust := newTestTrust(t, clock, store, keys)

	// First commit succeeds and anchors a working interval.
	observeSigned(t, trust, keys, baseTime())
	clock.Advance(time.Minute)
	if got := trust.Evaluate(baseTime().Add(2 * time.Hour)); got != DecisionValid {
		t.Fatalf("pre-failure decision = %s, want valid", got)
	}

	// Then the store breaks: the newer proof must not replace the old state.
	store.failSave = true
	nonce, err := trust.IssueChallenge()
	if err != nil {
		t.Fatalf("IssueChallenge: %v", err)
	}
	newTimestamp := baseTime().Add(time.Hour)
	err = trust.ObserveProof(SignProof(keys.private, newTimestamp, nonce))
	if !errors.Is(err, errFaultyStore) {
		t.Fatalf("ObserveProof error = %v, want injected failure", err)
	}

	if got := trust.DurableLower(); !got.Equal(baseTime()) {
		t.Fatalf("durable lower = %v, want previous commit %v", got, baseTime())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.Equal(baseTime()) {
		t.Fatalf("store content = %v, want previous commit %v", loaded, baseTime())
	}
	// The previous anchor still governs; its interval cannot jump to the
	// uncommitted timestamp, so validity is not extended.
	lower, upper, hasLower, hasUpper := trust.TrustedInterval()
	if !hasLower || !hasUpper {
		t.Fatalf("previous anchor lost after failed save")
	}
	if !upper.Before(newTimestamp) {
		t.Fatalf("upper bound %v reached uncommitted timestamp %v", upper, newTimestamp)
	}
	if lower.Before(baseTime()) {
		t.Fatalf("lower bound %v moved before the committed anchor %v", lower, baseTime())
	}

	// Once the store recovers, a fresh proof commits normally again.
	store.recoverStore()
	observeSigned(t, trust, keys, newTimestamp)
	if got := trust.DurableLower(); !got.Equal(newTimestamp) {
		t.Fatalf("durable lower after recovery = %v, want %v", got, newTimestamp)
	}
}

func TestCheckpointRetriesFailedCommit(t *testing.T) {
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	store := &faultyStore{failSave: true}
	trust := newTestTrust(t, clock, store, keys)

	committed := baseTime()
	next := committed.Add(time.Hour)
	// Model an in-memory lower bound whose durable commit previously failed.
	notePendingLower(trust, committed, next)

	// Checkpoint keeps surfacing the storage failure and never claims a
	// commit that did not happen.
	if err := trust.Checkpoint(); !errors.Is(err, errFaultyStore) {
		t.Fatalf("Checkpoint error = %v, want injected failure", err)
	}
	if got := trust.DurableLower(); !got.Equal(committed) {
		t.Fatalf("durable lower = %v, want unchanged %v", got, committed)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.IsZero() {
		t.Fatalf("Load after failed Checkpoint = %v, want zero", loaded)
	}

	// Once the store recovers, the retry commits the in-memory lower bound
	// without moving anything backwards.
	store.failSave = true
	store.recoverStore()
	if err := trust.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint after recovery: %v", err)
	}
	loaded, _ = store.Load()
	if !loaded.Equal(next) {
		t.Fatalf("committed = %v, want %v", loaded, next)
	}
	if got := trust.DurableLower(); !got.Equal(next) {
		t.Fatalf("durable lower after recovery = %v, want %v", got, next)
	}
}

func TestFileStoreRoundTripAndCorruption(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	path := filepath.Join(dir, "lower-bound")
	store := NewFileStore(path)

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load before first save: %v", err)
	}
	if !loaded.IsZero() {
		t.Fatalf("empty store loaded %v, want zero", loaded)
	}

	bound := baseTime().Add(123 * time.Millisecond)
	if err := store.Save(bound); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatalf("Load after save: %v", err)
	}
	if !loaded.Equal(bound) {
		t.Fatalf("loaded = %v, want %v", loaded, bound)
	}

	// A second, later commit replaces the first atomically.
	later := bound.Add(time.Hour)
	if err := store.Save(later); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	loaded, _ = store.Load()
	if !loaded.Equal(later) {
		t.Fatalf("loaded after second save = %v, want %v", loaded, later)
	}

	// Corrupt bytes are rejected rather than silently trusted.
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatalf("corrupting store: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("corrupt store error = %v, want ErrCorruptStore", err)
	}

	// A well-sized record with a magic prefix but a zero timestamp is also
	// corruption rather than a valid lower bound.
	record := make([]byte, fileStoreSize)
	copy(record, fileStoreMagic)
	if err := os.WriteFile(path, record, 0o600); err != nil {
		t.Fatalf("writing zero-timestamp record: %v", err)
	}
	if _, err := store.Load(); !errors.Is(err, ErrCorruptStore) {
		t.Fatalf("zero-timestamp record error = %v, want ErrCorruptStore", err)
	}

	// A trust instance rebuilt from the file keeps only the lower bound.
	if err := store.Save(bound); err != nil {
		t.Fatalf("restoring store: %v", err)
	}
	clock := NewFakeClock(baseTime())
	keys := newTestKeyPair(t)
	reloaded := newTestTrust(t, clock, store, keys)
	if got := reloaded.DurableLower(); !got.Equal(bound) {
		t.Fatalf("reloaded durable lower = %v, want %v", got, bound)
	}
	if _, _, hasLower, hasUpper := reloaded.TrustedInterval(); !hasLower || hasUpper {
		t.Fatalf("reloaded trust: hasLower/hasUpper wrong, want true/false")
	}
}

func TestFileStoreSaveFailureLeavesPreviousCommit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lower-bound")
	store := NewFileStore(path)
	original := baseTime()
	if err := store.Save(original); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	// Point the store at a read-only directory: the staging write fails and
	// the previously committed file must remain byte-for-byte intact.
	readOnly := filepath.Join(dir, "readonly")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatalf("Mkdir read-only: %v", err)
	}
	broken := NewFileStore(filepath.Join(readOnly, "lower-bound"))
	if err := broken.Save(original.Add(time.Hour)); err == nil {
		t.Fatalf("expected save into read-only directory to fail")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load after failed save: %v", err)
	}
	if !loaded.Equal(original) {
		t.Fatalf("previous commit changed: %v, want %v", loaded, original)
	}
}
