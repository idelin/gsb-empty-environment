package netsec

import (
	"errors"
	"testing"
)

func TestOutOfOrderContactReceiptsAreStale(t *testing.T) {
	f := newFixture(t)
	log := newFakeLog(f.logID)
	keyA, _ := newTestKey(t)
	contactPub, contactPriv := newTestKey(t)
	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}

	v1 := f.list(t, 1, ZeroDigest, NewDevice("device-a", keyA, 1))
	signed1 := f.sign(t, v1)
	proof1 := log.publish(t, signed1)
	applyCommitted(t, client, signed1, proof1)

	v2 := addDevice(t, v1, NewDevice("device-b", keyA, 2), 2)
	signed2 := f.sign(t, v2)
	proof2 := log.publish(t, signed2)
	applyCommitted(t, client, signed2, proof2)

	v3 := addDevice(t, v2, NewDevice("device-c", keyA, 3), 3)
	signed3 := f.sign(t, v3)
	proof3 := log.publish(t, signed3)
	applyCommitted(t, client, signed3, proof3)

	receipt3 := signedObservation(t, "contact", contactPriv,
		observationFor(t, f.account, 3, v3, proof3.Checkpoint))
	receipt2 := signedObservation(t, "contact", contactPriv,
		observationFor(t, f.account, 2, v2, proof2.Checkpoint))
	receipt1 := signedObservation(t, "contact", contactPriv,
		observationFor(t, f.account, 1, v1, proof1.Checkpoint))

	result, err := client.CheckObservation(receipt3, contactPub)
	if err != nil {
		t.Fatalf("newest receipt: %v", err)
	}
	if result.Status != ObservationAccepted || result.Stale {
		t.Fatalf("newest receipt status = %+v", result)
	}

	result, err = client.CheckObservation(receipt2, contactPub)
	if err != nil {
		t.Fatalf("older receipt v2: %v", err)
	}
	if result.Status != ObservationStale || !result.Stale {
		t.Fatalf("v2 receipt should be stale, got %+v", result)
	}
	result, err = client.CheckObservation(receipt1, contactPub)
	if err != nil {
		t.Fatalf("older receipt v1: %v", err)
	}
	if result.Status != ObservationStale || !result.Stale {
		t.Fatalf("v1 receipt should be stale, got %+v", result)
	}
	if client.Frozen() {
		t.Fatal("out-of-order but consistent receipts froze the client")
	}
	if _, err := client.OpenSession("device-a"); err != nil {
		t.Fatalf("session should remain available: %v", err)
	}
}

func TestSameCheckpointDifferentListsCreatesEvidence(t *testing.T) {
	f := newFixture(t)
	keyA, _ := newTestKey(t)
	contactPub, contactPriv := newTestKey(t)
	v2 := f.list(t, 2, ZeroDigest, NewDevice("device-a", keyA, 1))
	v3 := f.list(t, 3, ZeroDigest, NewDevice("device-b", keyA, 1))
	checkpoint := NewCheckpoint(f.logID, []Digest{mustDigest(t, v2)})

	receipt1 := signedObservation(t, "contact-one", contactPriv,
		observationFor(t, f.account, 2, v2, checkpoint))
	receipt2 := signedObservation(t, "contact-two", contactPriv,
		observationFor(t, f.account, 3, v3, checkpoint))

	client, err := NewClient(f.account, f.masterPub, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CheckObservation(receipt1, contactPub)
	if err != nil || !result.Stale && result.Status != ObservationAccepted {
		t.Fatalf("first receipt: result=%+v err=%v", result, err)
	}
	result, err = client.CheckObservation(receipt2, contactPub)
	if !errors.Is(err, ErrForkDetected) {
		t.Fatalf("same-checkpoint conflict: result=%+v err=%v", result, err)
	}
	if !result.Frozen || result.Evidence == nil {
		t.Fatalf("conflict did not freeze: %+v", result)
	}
	if result.Evidence.Reason != ForkSameCheckpointDifferentList {
		t.Fatalf("reason = %s", result.Evidence.Reason)
	}
	if _, err := client.OpenSession("device-a"); !errors.Is(err, ErrForkFrozen) {
		t.Fatalf("frozen client opened a session: %v", err)
	}
}

func TestObservationSignatureAndAccountChecks(t *testing.T) {
	f := newFixture(t)
	_, contactPriv := newTestKey(t)
	otherPub, _ := newTestKey(t)
	obs := Observation{
		AccountID:        f.account,
		Version:          1,
		ListDigest:       mustDigest(t, f.list(t, 1, ZeroDigest)),
		CheckpointDigest: NewCheckpoint(f.logID, []Digest{mustDigest(t, f.list(t, 1, ZeroDigest))}).Digest(),
	}
	receipt := signedObservation(t, "contact", contactPriv, obs)
	if err := receipt.Verify(f.account, otherPub); !errors.Is(err, ErrObservationSignature) {
		t.Fatalf("wrong signer: %v", err)
	}
	otherObs := obs
	otherObs.AccountID = "another-account"
	otherReceipt := signedObservation(t, "contact", contactPriv, otherObs)
	if err := otherReceipt.Verify(f.account, otherPub); !errors.Is(err, ErrObservationAccount) {
		t.Fatalf("wrong account returned %v", err)
	}
}

func TestReceiptsConflictDetectsBothForkShapes(t *testing.T) {
	obsA := Observation{AccountID: "account", Version: 2, ListDigest: Digest{1}, CheckpointDigest: Digest{2}}
	obsB := Observation{AccountID: "account", Version: 2, ListDigest: Digest{3}, CheckpointDigest: Digest{4}}
	reason, conflict := ReceiptsConflict(
		SignedObservation{Observation: obsA},
		SignedObservation{Observation: obsB},
	)
	if !conflict || reason != ForkSameVersionDifferentDigest {
		t.Fatalf("same-version conflict = %v, %v", reason, conflict)
	}

	obsC := Observation{AccountID: "account", Version: 3, ListDigest: Digest{5}, CheckpointDigest: obsA.CheckpointDigest}
	reason, conflict = ReceiptsConflict(
		SignedObservation{Observation: obsA},
		SignedObservation{Observation: obsC},
	)
	if !conflict || reason != ForkSameCheckpointDifferentList {
		t.Fatalf("same-checkpoint conflict = %v, %v", reason, conflict)
	}
}
