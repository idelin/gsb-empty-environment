package destruct

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLeaseLifecycleAndSealRejection(t *testing.T) {
	env := newTestEnv(t)
	const vol VolumeID = "vol-lease"
	env.createVolume(vol, "lease")

	lease, err := env.coord.GrantLease(context.Background(), vol, "r1", testTTL)
	if err != nil {
		t.Fatalf("GrantLease: %v", err)
	}

	// Renewal works while usable.
	renewed, err := env.coord.RenewLease(context.Background(), lease.ID, testTTL*2)
	if err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	if !renewed.ExpiresAt.After(lease.ExpiresAt) {
		t.Fatalf("renewal did not extend expiry: %s -> %s", lease.ExpiresAt, renewed.ExpiresAt)
	}

	// Unknown replica and bad TTL are rejected up front.
	if _, err := env.coord.GrantLease(context.Background(), vol, "rx", testTTL); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("unknown replica err = %v, want ErrUnknownReplica", err)
	}
	if _, err := env.coord.GrantLease(context.Background(), vol, "r1", 0); err == nil {
		t.Fatalf("zero TTL unexpectedly accepted")
	}

	// Explicit host-side revocation.
	if err := env.coord.RevokeLease(context.Background(), lease.ID); err != nil {
		t.Fatalf("RevokeLease: %v", err)
	}
	if _, err := env.coord.RenewLease(context.Background(), lease.ID, testTTL); !errors.Is(err, ErrLeaseNotActive) {
		t.Fatalf("renew revoked err = %v, want ErrLeaseNotActive", err)
	}
	// Repeated revocation is idempotent.
	if err := env.coord.RevokeLease(context.Background(), lease.ID); err != nil {
		t.Fatalf("repeat RevokeLease: %v", err)
	}

	// A fresh lease must be refused renewal once sealing starts.
	sealed, err := env.coord.GrantLease(context.Background(), vol, "r2", time.Hour)
	if err != nil {
		t.Fatalf("GrantLease r2: %v", err)
	}
	txn, err := env.coord.beginDestruction(context.Background(), vol)
	if err != nil {
		t.Fatalf("beginDestruction: %v", err)
	}
	env.assertNotUsable(vol)
	if _, err := env.coord.RenewLease(context.Background(), sealed.ID, time.Hour); !errors.Is(err, ErrVolumeSealed) {
		t.Fatalf("renew under seal err = %v, want ErrVolumeSealed", err)
	}
	record, err := env.store.GetLease(context.Background(), sealed.ID)
	if err != nil {
		t.Fatalf("GetLease: %v", err)
	}
	if record.State != LeaseRevoked {
		t.Fatalf("sealed lease state = %s, want revoked", record.State)
	}

	// Drive the open txn to completion.
	result, err := env.coord.runTxn(context.Background(), txn)
	if err != nil {
		t.Fatalf("runTxn: %v", err)
	}
	if !result.Complete {
		t.Fatalf("txn incomplete: %+v", result)
	}
	env.assertDestroyed(vol)

	// Lease APIs stay closed on a destroyed volume.
	if _, err := env.coord.RenewLease(context.Background(), sealed.ID, time.Hour); !errors.Is(err, ErrVolumeDestroyed) && !errors.Is(err, ErrLeaseNotActive) {
		t.Fatalf("renew destroyed err = %v, want ErrVolumeDestroyed or ErrLeaseNotActive", err)
	}
}

func TestPutConfirmationContentConflict(t *testing.T) {
	env := newTestEnv(t)
	env.createVolume("vol-conf", "conf")
	result, err := env.coord.Destroy(context.Background(), "vol-conf")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Complete {
		t.Fatalf("incomplete: %+v", result)
	}
	txnID := env.getVolume("vol-conf").DestroyTxnID
	confs, err := env.store.ListConfirmations(context.Background(), txnID)
	if err != nil {
		t.Fatalf("ListConfirmations: %v", err)
	}

	// Re-writing the identical confirmation is idempotent.
	duplicate := ConfirmationRecord{
		Confirmation: cloneConfirmation(confs[0].Confirmation),
		ReceivedAt:   env.clock.now(),
	}
	if err := env.store.PutConfirmation(context.Background(), &duplicate); err != nil {
		t.Fatalf("identical PutConfirmation: %v", err)
	}

	// A different payload for the same (txn, replica) conflicts.
	tampered := duplicate
	tampered.Confirmation.Signature = append([]byte(nil), duplicate.Confirmation.Signature...)
	tampered.Confirmation.Signature[0] ^= 0xFF
	if err := env.store.PutConfirmation(context.Background(), &tampered); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered PutConfirmation err = %v, want ErrConflict", err)
	}

	// GetConfirmation round trip.
	got, err := env.store.GetConfirmation(context.Background(), txnID, confs[0].Confirmation.ReplicaID)
	if err != nil {
		t.Fatalf("GetConfirmation: %v", err)
	}
	if got.Confirmation.TxnID != txnID {
		t.Fatalf("got txn %s, want %s", got.Confirmation.TxnID, txnID)
	}
	if _, err := env.store.GetConfirmation(context.Background(), txnID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing confirmation err = %v, want ErrNotFound", err)
	}
}

func TestCreateVolumeValidation(t *testing.T) {
	env := newTestEnv(t)
	var specs []ReplicaSpec
	for _, spec := range env.specs {
		specs = append(specs, cloneReplicaSpec(spec))
	}
	if _, err := env.coord.CreateVolume(context.Background(), "", "noname", specs); err == nil {
		t.Fatalf("empty volume id accepted")
	}
	if _, err := env.coord.CreateVolume(context.Background(), "v-empty", "noreplicas", nil); err == nil {
		t.Fatalf("empty replica set accepted")
	}
	dup := []ReplicaSpec{cloneReplicaSpec(specs[0]), cloneReplicaSpec(specs[0])}
	if _, err := env.coord.CreateVolume(context.Background(), "v-dup", "dup", dup); err == nil {
		t.Fatalf("duplicate replica ids accepted")
	}
	env.createVolume("v-once", "once")
	if _, err := env.coord.CreateVolume(context.Background(), "v-once", "again", specs); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateVolume err = %v, want ErrConflict", err)
	}
}
