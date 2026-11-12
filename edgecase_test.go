package netsec_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	netsec "example.org/netsec/q0338"
)

func TestReportAbsentTreatsMissingCopyAsErased(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	c := newCoordinator(t, store)

	v := mustCreateVolume(t, c, "vault", testReplicas())
	d := mustDestroy(t, c, v)

	if _, err := d.ReportAbsent(ctx, "r1"); err != nil {
		t.Fatalf("ReportAbsent r1: %v", err)
	}
	if _, err := d.ReportAbsent(ctx, "r1"); !errors.Is(err, netsec.ErrAlreadyErased) {
		t.Fatalf("expected ErrAlreadyErased, got %v", err)
	}
	if _, err := d.ReportAbsent(ctx, "ghost"); !errors.Is(err, netsec.ErrReplicaNotFound) {
		t.Fatalf("expected ErrReplicaNotFound, got %v", err)
	}
	if _, err := d.ReportErasure(ctx, "r2"); err != nil {
		t.Fatalf("ReportErasure r2: %v", err)
	}
	status, err := d.ReportErasure(ctx, "r3")
	if err != nil {
		t.Fatalf("ReportErasure r3: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected complete with absent confirmation, got %s", status.Outcome)
	}
}

func TestVolumeByIDAndDestructionLookupErrors(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	c := newCoordinator(t, store)

	v := mustCreateVolume(t, c, "vault", testReplicas())
	got, err := c.VolumeByID(ctx, v.ID())
	if err != nil {
		t.Fatalf("VolumeByID: %v", err)
	}
	if got.ID() != v.ID() || got.Name() != "vault" || len(got.Replicas()) != 3 {
		t.Fatalf("unexpected volume handle: %+v", got)
	}
	if _, err := c.VolumeByID(ctx, "missing"); !errors.Is(err, netsec.ErrVolumeNotFound) {
		t.Fatalf("expected ErrVolumeNotFound, got %v", err)
	}
	if _, err := c.Destruction(ctx, v.ID()); !errors.Is(err, netsec.ErrDestructionUnknown) {
		t.Fatalf("expected ErrDestructionUnknown, got %v", err)
	}
	if _, err := c.OpenVolume(ctx, "missing"); !errors.Is(err, netsec.ErrVolumeNotFound) {
		t.Fatalf("expected ErrVolumeNotFound from OpenVolume, got %v", err)
	}

	d := mustDestroy(t, c, v)
	if _, err := c.VolumeByID(ctx, v.ID()); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("expected ErrVolumeDestroyed, got %v", err)
	}
	if _, err := d.ReportErasure(ctx, ""); !errors.Is(err, netsec.ErrReplicaNotFound) {
		t.Fatalf("expected ErrReplicaNotFound for empty replica, got %v", err)
	}
}

func TestCreateVolumeValidation(t *testing.T) {
	ctx := context.Background()
	c := newCoordinator(t, netsec.NewMemoryStore())

	if _, err := c.CreateVolume(ctx, "", testReplicas()); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for empty name, got %v", err)
	}
	if _, err := c.CreateVolume(ctx, "vault", nil); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for no replicas, got %v", err)
	}
	bad := []netsec.Replica{{ID: "", Location: "x"}}
	if _, err := c.CreateVolume(ctx, "vault", bad); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for blank replica id, got %v", err)
	}
	dup := []netsec.Replica{
		{ID: "r1", Location: "a"},
		{ID: "r1", Location: "b"},
	}
	if _, err := c.CreateVolume(ctx, "vault", dup); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for duplicate id, got %v", err)
	}

	v := mustCreateVolume(t, c, "vault", testReplicas())
	if _, err := c.CreateVolume(ctx, "vault", testReplicas()); !errors.Is(err, netsec.ErrNameInUse) {
		t.Fatalf("expected ErrNameInUse, got %v", err)
	}
	if _, err := v.GrantLease(ctx, 0); !errors.Is(err, netsec.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument ttl, got %v", err)
	}
}

func TestRecoveryReassertsFrozenWhenRecordSurvivesManifestFlagLoss(t *testing.T) {
	ctx := context.Background()
	fault := netsec.NewFaultInjectingStore()
	c1 := newCoordinator(t, fault)
	v := mustCreateVolume(t, c1, "vault", testReplicas())
	lease, err := v.GrantLease(ctx, time.Hour)
	if err != nil {
		t.Fatalf("GrantLease: %v", err)
	}
	d := mustDestroy(t, c1, v)
	if status, _ := d.Status(ctx); status.Phase != netsec.PhaseDraining {
		t.Fatalf("expected draining, got %s", status.Phase)
	}

	// Simulate a torn state: the destruction record is durable while the
	// manifest's frozen flag somehow reverted on media.
	keys, err := fault.Committed().List(ctx, "volumes/")
	if err != nil || len(keys) != 1 {
		t.Fatalf("manifest list: %v %v", keys, err)
	}
	raw, err := fault.Committed().Get(ctx, keys[0])
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	raw = []byte(strings.ReplaceAll(string(raw), "\"frozen\": true", "\"frozen\": false"))
	if err := fault.Committed().Put(ctx, keys[0], raw); err != nil {
		t.Fatalf("rewrite manifest: %v", err)
	}

	c2 := newCoordinator(t, fault.Committed())
	if _, err := c2.RecoverDestructions(ctx); err != nil {
		t.Fatalf("RecoverDestructions: %v", err)
	}
	if _, err := c2.OpenVolume(ctx, "vault"); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("recovery must keep destroying volume closed, got %v", err)
	}
	// Recovered transaction resumes from draining.
	pending, err := c2.RecoverDestructions(ctx)
	if err != nil {
		t.Fatalf("RecoverDestructions after reopen: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending transaction, got %d", len(pending))
	}
	if err := v.ReleaseLease(ctx, lease.ID); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	for _, replica := range testReplicas() {
		if _, err := pending[0].ReportErasure(ctx, replica.ID); err != nil {
			t.Fatalf("ReportErasure %s: %v", replica.ID, err)
		}
	}
	status, err := pending[0].Status(ctx)
	if err != nil {
		t.Fatalf("final Status: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion, got %s", status.Outcome)
	}
}

func TestActiveLeasesAndReappearingManifestReplica(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	clock := newFakeClock()
	eraser := newFakeEraser()
	scanner := newFakeScanner()
	c, err := netsec.NewCoordinator(store, netsec.WithClock(clock),
		netsec.WithEraser(eraser), netsec.WithScanner(scanner))
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	v := mustCreateVolume(t, c, "vault", testReplicas())
	short, err := v.GrantLease(ctx, time.Minute)
	if err != nil {
		t.Fatalf("GrantLease short: %v", err)
	}
	long, err := v.GrantLease(ctx, time.Hour)
	if err != nil {
		t.Fatalf("GrantLease long: %v", err)
	}
	active, err := v.ActiveLeases(ctx)
	if err != nil {
		t.Fatalf("ActiveLeases: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active leases, got %d", len(active))
	}

	d := mustDestroy(t, c, v)
	if status, _ := d.Status(ctx); status.Phase != netsec.PhaseDraining {
		t.Fatalf("expected draining, got %s", status.Phase)
	}
	clock.Advance(2 * time.Minute)
	active, err = v.ActiveLeases(ctx)
	if err != nil {
		t.Fatalf("ActiveLeases after short expiry: %v", err)
	}
	if len(active) != 1 || active[0].ID != long.ID {
		t.Fatalf("expected only long lease active, got %+v", active)
	}
	if err := v.ReleaseLease(ctx, long.ID); err != nil {
		t.Fatalf("ReleaseLease long: %v", err)
	}

	// Initially every manifest replica is reachable.
	scanner.Set(testReplicas()...)
	status, err := d.Step(ctx)
	if err != nil {
		t.Fatalf("Step to completion: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion, got %s", status.Outcome)
	}

	// Simulate r1 material reappearing on media after completion: its
	// confirmation is invalidated in the durable transaction and a
	// verification sweep still reports the location reachable.
	recordKey := "destroy/" + string(v.ID()) + "/record"
	raw, err := store.Get(ctx, recordKey)
	if err != nil {
		t.Fatalf("get destroy record: %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	erased, ok := record["erased"].(map[string]any)
	if !ok {
		t.Fatalf("erased block missing: %v", record)
	}
	delete(erased, "r1")
	record["phase"] = "complete"
	rewritten, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := store.Put(ctx, recordKey, rewritten); err != nil {
		t.Fatalf("rewrite destroy record: %v", err)
	}
	scanner.Set(
		netsec.Replica{ID: "r1", Location: "store/key/r1"},
		netsec.Replica{ID: "r2", Location: "store/key/r2"},
		netsec.Replica{ID: "r3", Location: "store/key/r3"},
	)
	status, err = d.Step(ctx)
	if err != nil {
		t.Fatalf("verification Step after r1 reappears: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("r1 re-erasure should re-complete, got %s", status.Outcome)
	}
	if eraser.CallsFor("r1") != 2 {
		t.Fatalf("expected r1 erased twice after reappearing, got %d", eraser.CallsFor("r1"))
	}

	// The expired short lease is gone; releasing it is idempotent.
	if err := v.ReleaseLease(ctx, short.ID); err != nil {
		t.Fatalf("idempotent release of expired lease: %v", err)
	}
}

func TestFaultStoreFlushPendingAndInjectedGetListDeleteFaults(t *testing.T) {
	ctx := context.Background()
	fault := netsec.NewFaultInjectingStore()
	fault.DelayPuts()
	if err := fault.Put(ctx, "k1", []byte("v1")); err != nil {
		t.Fatalf("delayed put: %v", err)
	}
	if err := fault.Put(ctx, "k2", []byte("v2")); err != nil {
		t.Fatalf("delayed put: %v", err)
	}
	if fault.PendingCount() != 2 {
		t.Fatalf("expected 2 pending, got %d", fault.PendingCount())
	}
	if n := fault.FlushPending(ctx); n != 2 {
		t.Fatalf("expected 2 flushed, got %d", n)
	}
	if got, err := fault.Get(ctx, "k1"); err != nil || string(got) != "v1" {
		t.Fatalf("flushed get: %v %v", got, err)
	}

	fault.FailGet(func(key string) bool { return key == "k1" })
	if _, err := fault.Get(ctx, "k1"); !netsec.IsFaultIOError(err) {
		t.Fatalf("expected injected get fault, got %v", err)
	}
	fault.FailGet(nil)
	fault.FailList(func(prefix string) bool { return prefix == "k" })
	if _, err := fault.List(ctx, "k"); !netsec.IsFaultIOError(err) {
		t.Fatalf("expected injected list fault, got %v", err)
	}
	fault.FailList(nil)
	fault.FailDelete(func(key string) bool { return key == "k1" })
	if err := fault.Delete(ctx, "k1"); !netsec.IsFaultIOError(err) {
		t.Fatalf("expected injected delete fault, got %v", err)
	}
	fault.FailDelete(nil)
	if err := fault.Delete(ctx, "k1"); err != nil {
		t.Fatalf("delete after fault cleared: %v", err)
	}
	if _, err := fault.Get(ctx, "k1"); !errors.Is(err, netsec.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
	fault.Crash()
	if fault.PendingCount() != 0 {
		t.Fatal("crash must discard pending writes")
	}
	puts, gets, dels, lists := fault.OpCounters()
	if puts == 0 || gets == 0 || dels == 0 || lists == 0 {
		t.Fatalf("expected all operation types counted, got %d/%d/%d/%d", puts, gets, dels, lists)
	}
}
