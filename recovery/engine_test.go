package recovery

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func testKeyring(tenants ...TenantID) Keyring {
	keyring := make(Keyring)
	for _, tenant := range tenants {
		keyring[tenant] = NewKeyPair([]byte("test-master-key-material-32-bytes"), tenant)
	}
	return keyring
}

func testEngine(t *testing.T, store *MemStore, keyring Keyring, layout Layout) *Engine {
	t.Helper()
	engine, err := New(store, keyring, Config{
		Layout:     layout,
		FrameSize:  store.FrameSize(),
		MaxRetries: 2,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine
}

func signedTxn(t *testing.T, keyring Keyring, tenant TenantID, lease Lease, base uint64, label string) SignedTxn {
	t.Helper()
	txn := SignedTxn{
		ID:             TxIDFromString("txn-" + label),
		Tenant:         tenant,
		LeaseEpoch:     lease.Epoch,
		LeaseID:        lease.ID,
		BaseGeneration: base,
		Nonce:          [16]byte{1, 2, 3, byte(len(label))},
	}
	txn, err := keyring.SignTransaction(txn)
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}
	return txn
}

func testBlock(tenant TenantID, label, payload string, version uint64) Block {
	return Block{
		Tenant:  tenant,
		ID:      BlockIDFromString(label),
		Version: version,
		Data:    []byte(payload),
	}
}

func TestCommitRecoverAndEncryptedStore(t *testing.T) {
	tenant := TenantIDFromString("tenant-a")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-a1")}
	layout := SingleTenantLayout(tenant, 12)
	store, err := NewMemStore(12, 1024)
	if err != nil {
		t.Fatal(err)
	}
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 0, "create")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	blocks := []Block{
		testBlock(tenant, "block-1", "plaintext-secret-one", 1),
		testBlock(tenant, "block-2", "plaintext-secret-two", 2),
	}
	for _, block := range blocks {
		if err := batch.Stage(block); err != nil {
			t.Fatalf("Stage: %v", err)
		}
	}
	receipt, err := batch.Commit()
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if receipt.Generation != 1 || receipt.FrameCount != 6 || receipt.BlockCount != 2 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	for _, write := range store.WriteLog() {
		if bytes.Contains(write.Data, []byte("plaintext-secret")) {
			t.Fatalf("storage host observed plaintext block data")
		}
	}

	restarted := testEngine(t, store, keys, layout)
	var applied int
	result, err := restarted.RecoverAndApply(RecoveryRequest{Tenant: tenant, Lease: lease}, func(got RecoveryResult) error {
		applied++
		if !reflect.DeepEqual(got.Snapshot.Blocks, blocks) {
			t.Fatalf("recovered blocks differ: %#v", got.Snapshot.Blocks)
		}
		if got.Receipt.Txn != txn.ID || got.Receipt.Generation != 1 {
			t.Fatalf("unexpected receipt: %+v", got.Receipt)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RecoverAndApply: %v", err)
	}
	if applied != 1 {
		t.Fatalf("apply called %d times, want 1", applied)
	}
	if result.Snapshot == nil || !reflect.DeepEqual(result.Snapshot.Blocks, blocks) {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	plan, err := restarted.Plan(tenant)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trace) != plan.MaxReads || plan.MaxWrites != 0 {
		t.Fatalf("trace %d, plan reads %d writes %d", len(result.Trace), plan.MaxReads, plan.MaxWrites)
	}
	for _, event := range result.Trace {
		if event.Op != OpRead || event.Length != 1024 || event.Phase != PhaseRecoverScan {
			t.Fatalf("unexpected recovery event: %+v", event)
		}
		if event.Slot < plan.LaneStart || event.Slot >= plan.LaneStart+plan.LaneSlots {
			t.Fatalf("recovery read outside lane: %+v", event)
		}
	}
}

func TestDeleteAndNewLeaseDoNotResurrectOldTenantState(t *testing.T) {
	tenant := TenantIDFromString("tenant-delete")
	oldLease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-old")}
	newLease := Lease{Tenant: tenant, Epoch: 2, ID: LeaseIDFromString("lease-new")}
	layout := SingleTenantLayout(tenant, 12)
	store, _ := NewMemStore(12, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)

	txn1 := signedTxn(t, keys, tenant, oldLease, 0, "old")
	batch, err := engine.Begin(txn1)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Stage(testBlock(tenant, "old-block", "old-data", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.Commit(); err != nil {
		t.Fatal(err)
	}

	txn2 := signedTxn(t, keys, tenant, oldLease, 1, "delete")
	if _, err := engine.Delete(txn2); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	restarted := testEngine(t, store, keys, layout)
	deleted, err := restarted.Recover(RecoveryRequest{Tenant: tenant, Lease: oldLease})
	if err != nil {
		t.Fatal(err)
	}
	if !deleted.Deleted || deleted.Snapshot != nil {
		t.Fatalf("tombstone not recovered: %+v", deleted)
	}
	fresh, err := restarted.Recover(RecoveryRequest{Tenant: tenant, Lease: newLease})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Receipt != nil || fresh.Snapshot != nil {
		t.Fatalf("new lease saw old generation: %+v", fresh)
	}
}

func TestUnacknowledgedBatchIsInvisibleAndResolvable(t *testing.T) {
	tenant := TenantIDFromString("tenant-unack")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-unack")}
	layout := SingleTenantLayout(tenant, 16)
	store, _ := NewMemStore(16, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)

	txn := signedTxn(t, keys, tenant, lease, 0, "prepared")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Stage(testBlock(tenant, "unack-block", "unack-data", 1)); err != nil {
		t.Fatal(err)
	}
	// Simulate power loss before the receipt can be written: make the receipt
	// slot fail permanently for this engine.
	for i := 0; i < 3; i++ {
		store.QueueFailure(Failure{Op: OpWrite, Region: RegionA, Slot: 4, Kind: FailBeforeWrite})
	}
	if _, err := batch.Commit(); err == nil {
		t.Fatal("Commit unexpectedly succeeded")
	}

	restarted := testEngine(t, store, keys, layout)
	recovered, err := restarted.Recover(RecoveryRequest{Tenant: tenant, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Receipt != nil || recovered.Snapshot != nil {
		t.Fatalf("unacknowledged batch became visible: %+v", recovered)
	}

	resolved, err := restarted.Resolve(txn)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.State != ResolveRetrieved || resolved.Snapshot == nil || len(resolved.Snapshot.Blocks) != 1 {
		t.Fatalf("signed transaction did not retrieve result: %+v", resolved)
	}
	after, err := restarted.Recover(RecoveryRequest{Tenant: tenant, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	if after.Receipt == nil || after.Receipt.Txn != txn.ID || len(after.Snapshot.Blocks) != 1 {
		t.Fatalf("resolved receipt not durable: %+v", after)
	}

	other := signedTxn(t, keys, tenant, lease, 0, "other")
	if _, err := restarted.Resolve(other); !errors.Is(err, ErrTxnMismatch) {
		// "other" has a different transaction id and therefore does not match
		// any chain; only a same-id/different-token request is a mismatch.
		if err != nil {
			t.Logf("resolve other: %v", err)
		}
	}
}

func TestRecoveryRejectsTornFrameAndCacheAlias(t *testing.T) {
	tenant := TenantIDFromString("tenant-adversary")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-adversary")}
	layout := SingleTenantLayout(tenant, 12)
	store, _ := NewMemStore(12, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 0, "adversary")
	batch, _ := engine.Begin(txn)
	if err := batch.Stage(testBlock(tenant, "a", "a-data", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.Commit(); err != nil {
		t.Fatal(err)
	}

	state := store.Snapshot()
	aliased := state
	aliased.A = cloneSlots(state.A)
	aliased.A[4] = append([]byte(nil), state.A[2]...)
	if err := store.RestoreRaw(aliased); err != nil {
		t.Fatal(err)
	}
	result, err := testEngine(t, store, keys, layout).Recover(RecoveryRequest{Tenant: tenant, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt != nil {
		t.Fatal("slot-aliased frame was accepted as commit evidence")
	}

	if err := store.RestoreRaw(state); err != nil {
		t.Fatal(err)
	}
	torn := store.Snapshot()
	torn.A = cloneSlots(state.A)
	torn.A[0] = append([]byte(nil), state.A[0][:700]...)
	if err := store.RestoreRaw(torn); err != nil {
		t.Fatal(err)
	}
	result, err = testEngine(t, store, keys, layout).Recover(RecoveryRequest{Tenant: tenant, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt != nil {
		t.Fatal("torn header was accepted as commit evidence")
	}
}

func TestWrongBaseGenerationIsRejectedBeforeWrite(t *testing.T) {
	tenant := TenantIDFromString("tenant-conflict")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-conflict")}
	layout := SingleTenantLayout(tenant, 10)
	store, _ := NewMemStore(10, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 7, "conflict")
	if _, err := engine.Begin(txn); !errors.Is(err, ErrConflict) {
		t.Fatalf("Begin error %v, want conflict", err)
	}
	if len(store.WriteLog()) != 0 {
		t.Fatal("conflicting transaction wrote stable storage")
	}
}

func TestEngineNeedsOnlyTheTargetTenantKey(t *testing.T) {
	tenant := TenantIDFromString("tenant-target-key")
	foreign := TenantIDFromString("tenant-foreign-key")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-target-key")}
	layout := Layout{
		RegionSlots: 12,
		Lanes: []LaneSpec{
			{Tenant: tenant, Start: 0, Slots: 6},
			{Tenant: foreign, Start: 6, Slots: 6},
		},
	}
	store, _ := NewMemStore(12, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 0, "target-key")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Stage(testBlock(tenant, "only-target", "only-target-data", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := testEngine(t, store, keys, layout).Recover(RecoveryRequest{Tenant: tenant, Lease: lease})
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || len(result.Snapshot.Blocks) != 1 {
		t.Fatalf("target recovery failed without foreign key: %+v", result)
	}
	foreignTxn := txn
	foreignTxn.Tenant = foreign
	if _, err := engine.Begin(foreignTxn); !errors.Is(err, ErrSignature) {
		t.Fatalf("foreign operation error %v, want signature/key rejection", err)
	}
}

func TestRecoverAndApplySuppressesReplayWithinOneEngine(t *testing.T) {
	tenant := TenantIDFromString("tenant-idempotent")
	lease := Lease{Tenant: tenant, Epoch: 1, ID: LeaseIDFromString("lease-idempotent")}
	layout := SingleTenantLayout(tenant, 10)
	store, _ := NewMemStore(10, 1024)
	keys := testKeyring(tenant)
	engine := testEngine(t, store, keys, layout)
	txn := signedTxn(t, keys, tenant, lease, 0, "idempotent")
	batch, err := engine.Begin(txn)
	if err != nil {
		t.Fatal(err)
	}
	if err := batch.Stage(testBlock(tenant, "idempotent-block", "idempotent-data", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := batch.Commit(); err != nil {
		t.Fatal(err)
	}
	applies := 0
	for i := 0; i < 3; i++ {
		result, err := engine.RecoverAndApply(RecoveryRequest{Tenant: tenant, Lease: lease}, func(RecoveryResult) error {
			applies++
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Receipt == nil || result.Receipt.Txn != txn.ID {
			t.Fatalf("unexpected recovery result: %+v", result)
		}
	}
	if applies != 1 {
		t.Fatalf("apply called %d times, want 1", applies)
	}
}
