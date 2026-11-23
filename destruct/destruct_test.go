package destruct

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDestroyHappyPathAndIdempotency(t *testing.T) {
	env := newTestEnv(t)
	const vol VolumeID = "vol-1"
	env.createVolume(vol, "payments")

	lease, err := env.coord.GrantLease(context.Background(), vol, "r1", testTTL)
	if err != nil {
		t.Fatalf("GrantLease: %v", err)
	}

	// That lease is in-flight when destruction starts; it must be drained or
	// force-revoked before erasure begins.
	result, err := env.coord.Destroy(context.Background(), vol)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Complete || result.State != TxnDestroyed {
		t.Fatalf("result = %+v, want complete destroyed", result)
	}
	env.assertDestroyed(vol)

	revoked, err := env.store.GetLease(context.Background(), lease.ID)
	if err != nil {
		t.Fatalf("GetLease: %v", err)
	}
	if revoked.State == LeaseActive {
		t.Fatalf("lease %s still active", lease.ID)
	}

	// Idempotent: repeating Destroy reports destroyed without creating a txn.
	txnID := env.getVolume(vol).DestroyTxnID
	if _, err := env.coord.Destroy(context.Background(), vol); !errors.Is(err, ErrVolumeDestroyed) {
		t.Fatalf("repeat Destroy err = %v, want ErrVolumeDestroyed", err)
	}
	if env.getVolume(vol).DestroyTxnID != txnID {
		t.Fatalf("txn id changed on repeated Destroy: %s -> %s", txnID, env.getVolume(vol).DestroyTxnID)
	}

	// A sealed/destroyed volume never accepts leases again.
	if _, err := env.coord.GrantLease(context.Background(), vol, "r1", testTTL); !errors.Is(err, ErrVolumeDestroyed) {
		t.Fatalf("GrantLease after destroy err = %v, want ErrVolumeDestroyed", err)
	}
}

func TestConcurrentLeaseRevocation(t *testing.T) {
	env := newTestEnv(t)
	const vol VolumeID = "vol-concurrent"
	env.createVolume(vol, "ledger")

	// Seed several long-lived in-flight leases.
	for i := 0; i < 5; i++ {
		replica := ReplicaID(fmt.Sprintf("r%d", 1+i%3))
		if _, err := env.coord.GrantLease(context.Background(), vol, replica, testTTL); err != nil {
			t.Fatalf("seed GrantLease: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	// Lease hammer: keep granting and renewing while destruction runs.
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				lease, err := env.coord.GrantLease(ctx, vol, "r1", testTTL)
				if err != nil {
					return
				}
				_, _ = env.coord.RenewLease(ctx, lease.ID, testTTL)
			}
		}()
	}

	// Let the hammer start, then destroy.
	time.Sleep(5 * time.Millisecond)
	result, err := env.coord.Destroy(ctx, vol)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Complete {
		t.Fatalf("Destroy incomplete: %+v", result)
	}
	cancel()
	wg.Wait()

	env.assertDestroyed(vol)
	leases, err := env.store.ListLeases(context.Background(), vol)
	if err != nil {
		t.Fatalf("ListLeases: %v", err)
	}
	var active int
	for _, lease := range leases {
		if lease.State == LeaseActive {
			active++
		}
	}
	if active != 0 {
		t.Fatalf("%d active leases survived destruction", active)
	}

	// Nothing can re-open the volume by leasing after completion.
	if _, err := env.coord.GrantLease(context.Background(), vol, "r1", testTTL); !errors.Is(err, ErrVolumeDestroyed) {
		t.Fatalf("post-destroy grant err = %v", err)
	}
}

func TestLateAndInvalidConfirmations(t *testing.T) {
	env := newTestEnv(t)
	const vol VolumeID = "vol-late"
	env.createVolume(vol, "cache")

	// Make the first replica fail once so the txn stays open and external
	// confirmations can race in.
	env.replicas["r1"].FailNext(1, errors.New("replica r1 busy"))

	result, err := env.coord.Destroy(context.Background(), vol)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if result.Complete {
		t.Fatalf("Destroy unexpectedly complete with failing replica")
	}
	if !errors.Is(&IncompleteError{Reasons: result.Reasons}, ErrDestructionIncomplete) {
		t.Fatalf("reasons %v do not wrap ErrDestructionIncomplete", result.Reasons)
	}
	txnID := result.TxnID
	txn := env.getTxn(txnID)
	if txn.State != TxnErasing {
		t.Fatalf("txn state = %s, want erasing (not marked destroyed)", txn.State)
	}

	spec := txn.Replicas["r1"]

	// Unknown replica rejected.
	unknown := Confirmation{
		VolumeID: vol, TxnID: txnID, ReplicaID: "r9",
		Location: "/nowhere", Generation: spec.Generation,
		IssuedAt: env.clock.now(),
	}
	if err := env.coord.SubmitConfirmation(context.Background(), unknown); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("unknown replica err = %v, want ErrUnknownReplica", err)
	}

	// Foreign txn/volume identity rejected.
	foreign := Confirmation{
		VolumeID: "other-volume", TxnID: txnID, ReplicaID: "r1",
		Location: spec.Location, Generation: spec.Generation,
		IssuedAt:  env.clock.now(),
		Signature: SignConfirmation(env.replicas["r1"].priv, "other-volume", txnID, "r1", spec.Location, spec.Generation),
	}
	if err := env.coord.SubmitConfirmation(context.Background(), foreign); !errors.Is(err, ErrTxnMismatch) {
		t.Fatalf("foreign confirmation err = %v, want ErrTxnMismatch", err)
	}

	// Bad signature rejected.
	badSig := Confirmation{
		VolumeID: vol, TxnID: txnID, ReplicaID: "r1",
		Location: spec.Location, Generation: spec.Generation,
		IssuedAt:  env.clock.now(),
		Signature: []byte("not-a-signature"),
	}
	if err := env.coord.SubmitConfirmation(context.Background(), badSig); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("bad signature err = %v, want ErrInvalidSignature", err)
	}

	// A valid late confirmation for r1 is accepted.
	good := Confirmation{
		VolumeID: vol, TxnID: txnID, ReplicaID: "r1",
		Location: spec.Location, Generation: spec.Generation,
		IssuedAt:  env.clock.now(),
		Signature: SignConfirmation(env.replicas["r1"].priv, vol, txnID, "r1", spec.Location, spec.Generation),
	}
	if err := env.coord.SubmitConfirmation(context.Background(), good); err != nil {
		t.Fatalf("late valid confirmation: %v", err)
	}

	// Resuming the transaction converges; the stored confirmation is reused.
	result2, err := env.coord.Destroy(context.Background(), vol)
	if err != nil {
		t.Fatalf("resume Destroy: %v", err)
	}
	if !result2.Complete {
		t.Fatalf("resumed Destroy incomplete: %+v", result2)
	}
	// r1's confirmation was delivered out-of-band (SubmitConfirmation), so
	// check durable state directly rather than via the FakeReplica bookkeeping.
	record := env.getVolume(vol)
	if record.State != VolumeDestroyed {
		t.Fatalf("volume state = %s, want destroyed", record.State)
	}
	finalTxn := env.getTxn(record.DestroyTxnID)
	if finalTxn.State != TxnDestroyed {
		t.Fatalf("txn state = %s, want destroyed", finalTxn.State)
	}
	confs, err := env.store.ListConfirmations(context.Background(), finalTxn.ID)
	if err != nil {
		t.Fatalf("ListConfirmations: %v", err)
	}
	if len(confs) != len(finalTxn.Replicas) {
		t.Fatalf("confirmations = %d, want %d", len(confs), len(finalTxn.Replicas))
	}

	// A confirmation arriving after the txn closed is refused, never mixed in.
	late := good
	late.IssuedAt = env.clock.now().Add(time.Second)
	if err := env.coord.SubmitConfirmation(context.Background(), late); !errors.Is(err, ErrTxnClosed) {
		t.Fatalf("post-close confirmation err = %v, want ErrTxnClosed", err)
	}
}

func TestGenerationMismatchAndPartialErase(t *testing.T) {
	env := newTestEnv(t)
	const vol VolumeID = "vol-rotate"
	env.createVolume(vol, "vault")

	// Rotate r1's key generation twice behind the volume record's back: the
	// signed confirmation then carries generation 9 while the txn snapshot
	// expects 7, so it must be rejected and destruction stays incomplete.
	env.replicas["r1"].RotateKey(9)

	result, err := env.coord.Destroy(context.Background(), vol)
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if result.Complete {
		t.Fatalf("Destroy marked complete despite generation mismatch: %+v", result)
	}
	found := false
	for _, reason := range result.Reasons {
		if strings.Contains(reason, "generation mismatch") {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons %v do not mention generation mismatch", result.Reasons)
	}
	if env.getVolume(vol).State == VolumeDestroyed {
		t.Fatalf("volume marked destroyed on partial erase")
	}
	env.assertNotUsable(vol)

	// Simulate the replica reconciling to the snapshot generation again.
	env.replicas["r1"].RotateKey(7)
	result2, err := env.coord.Destroy(context.Background(), vol)
	if err != nil {
		t.Fatalf("resume Destroy: %v", err)
	}
	if !result2.Complete {
		t.Fatalf("resumed txn incomplete: %+v", result2)
	}
	env.assertDestroyed(vol)
}

func TestVolumeNameReuseIsolation(t *testing.T) {
	env := newTestEnv(t)
	const (
		oldVol VolumeID = "vol-old"
		newVol VolumeID = "vol-new"
		name            = "shared-name"
	)
	env.createVolume(oldVol, name)

	if _, err := env.coord.GrantLease(context.Background(), oldVol, "r1", testTTL); err != nil {
		t.Fatalf("GrantLease: %v", err)
	}
	oldResult, err := env.coord.Destroy(context.Background(), oldVol)
	if err != nil {
		t.Fatalf("Destroy old: %v", err)
	}
	if !oldResult.Complete {
		t.Fatalf("old Destroy incomplete: %+v", oldResult)
	}
	oldTxn := env.getVolume(oldVol).DestroyTxnID

	// A brand-new volume reuses the same human name but gets a new identity.
	env.createVolume(newVol, name)
	newRecord := env.getVolume(newVol)
	if newRecord.State != VolumeUsable || newRecord.DestroyTxnID != "" {
		t.Fatalf("new volume not cleanly usable: %+v", newRecord)
	}

	// New volume leases must never appear under the old volume/txn.
	newLease, err := env.coord.GrantLease(context.Background(), newVol, "r2", testTTL)
	if err != nil {
		t.Fatalf("GrantLease on new volume: %v", err)
	}
	if newLease.Volume != newVol {
		t.Fatalf("lease attached to %s, want %s", newLease.Volume, newVol)
	}
	oldLeases, err := env.store.ListLeases(context.Background(), oldVol)
	if err != nil {
		t.Fatalf("ListLeases old: %v", err)
	}
	for _, l := range oldLeases {
		if l.ID == newLease.ID {
			t.Fatalf("new lease %s leaked into old volume", newLease.ID)
		}
	}

	newResult, err := env.coord.Destroy(context.Background(), newVol)
	if err != nil {
		t.Fatalf("Destroy new: %v", err)
	}
	if !newResult.Complete || newResult.TxnID == oldTxn {
		t.Fatalf("new txn must be distinct: old=%s new=%+v", oldTxn, newResult)
	}

	oldConfs, err := env.store.ListConfirmations(context.Background(), oldTxn)
	if err != nil {
		t.Fatalf("ListConfirmations old: %v", err)
	}
	for _, record := range oldConfs {
		if record.Confirmation.VolumeID != oldVol {
			t.Fatalf("old txn contains confirmation for %s", record.Confirmation.VolumeID)
		}
	}
	newTxn := env.getVolume(newVol).DestroyTxnID
	newConfs, err := env.store.ListConfirmations(context.Background(), newTxn)
	if err != nil {
		t.Fatalf("ListConfirmations new: %v", err)
	}
	for _, record := range newConfs {
		if record.Confirmation.VolumeID != newVol {
			t.Fatalf("new txn contains foreign confirmation: %+v", record.Confirmation)
		}
	}

	// Late confirmation carrying the old txn id stays quarantined: the old
	// txn is closed, and the new txn snapshot cannot accept it either.
	spec := env.getTxn(newTxn).Replicas["r1"]
	stale := Confirmation{
		VolumeID: oldVol, TxnID: oldTxn, ReplicaID: "r1",
		Location: spec.Location, Generation: spec.Generation,
		IssuedAt:  env.clock.now(),
		Signature: SignConfirmation(env.replicas["r1"].priv, oldVol, oldTxn, "r1", spec.Location, spec.Generation),
	}
	if err := env.coord.SubmitConfirmation(context.Background(), stale); !errors.Is(err, ErrTxnClosed) {
		t.Fatalf("stale confirmation err = %v, want ErrTxnClosed", err)
	}
}
