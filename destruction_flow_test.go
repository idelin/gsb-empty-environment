package netsec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0338"
)

func TestFullDestructionProhibitsLeasesAndErasesAllReplicas(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	clock := newFakeClock()
	eraser := newFakeEraser()
	c, err := netsec.NewCoordinator(store, netsec.WithClock(clock), netsec.WithEraser(eraser))
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	v := mustCreateVolume(t, c, "vault", testReplicas())
	lease, err := v.GrantLease(ctx, time.Minute)
	if err != nil {
		t.Fatalf("GrantLease before destroy: %v", err)
	}

	d, err := c.DestroyVolume(ctx, v)
	if err != nil {
		t.Fatalf("DestroyVolume with active lease: %v", err)
	}
	status, err := d.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Outcome != netsec.StatusInProgress || status.Phase != netsec.PhaseDraining {
		t.Fatalf("expected draining/in-progress, got phase=%s outcome=%s", status.Phase, status.Outcome)
	}

	// The freeze is durable before any erasure: new leases are refused and no
	// key copy is touched while a lease remains active.
	if _, err := v.GrantLease(ctx, time.Minute); !errors.Is(err, netsec.ErrLeasesProhibited) {
		t.Fatalf("expected ErrLeasesProhibited, got %v", err)
	}
	if eraser.Calls() != 0 {
		t.Fatalf("eraser ran before lease drain: %d calls", eraser.Calls())
	}

	clock.Advance(2 * time.Minute)
	status, err = d.Step(ctx)
	if err != nil {
		t.Fatalf("Step after lease expiry: %v", err)
	}
	if status.Phase != netsec.PhaseComplete || status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected complete, got phase=%s outcome=%s", status.Phase, status.Outcome)
	}
	for _, replica := range testReplicas() {
		if !eraser.Erased(replica.ID) {
			t.Fatalf("replica %s was not erased", replica.ID)
		}
	}

	if _, err := c.OpenVolume(ctx, "vault"); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("expected ErrVolumeDestroyed on reopen, got %v", err)
	}
	if err := v.ReleaseLease(ctx, lease.ID); err != nil {
		t.Fatalf("releasing a drained lease must be idempotent, got %v", err)
	}
}

func TestUnknownReplicaKeepsDestructionIncomplete(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	eraser := newFakeEraser()
	scanner := newFakeScanner()
	c := newCoordinator(t, store, netsec.WithEraser(eraser), netsec.WithScanner(scanner))

	v := mustCreateVolume(t, c, "vault", testReplicas())
	// A non-manifest copy is reachable when destruction reaches the
	// reconciliation scan.
	scanner.Set(
		netsec.Replica{ID: "ghost", Location: "leaked/key/ghost"},
		netsec.Replica{ID: "r1", Location: "store/key/r1"},
		netsec.Replica{ID: "r2", Location: "store/key/r2"},
		netsec.Replica{ID: "r3", Location: "store/key/r3"},
	)
	d, err := c.DestroyVolume(ctx, v)
	if !errors.Is(err, netsec.ErrUnknownReplica) {
		t.Fatalf("expected ErrUnknownReplica from scan, got %v", err)
	}
	status, err := d.Step(ctx)
	if err != nil {
		t.Fatalf("Step with ghost replica: %v", err)
	}
	if status.Outcome != netsec.StatusIncomplete {
		t.Fatalf("expected incomplete outcome, got %s", status.Outcome)
	}
	if len(status.UnknownReplicas) != 1 || status.UnknownReplicas[0] != "ghost" {
		t.Fatalf("expected ghost in unknown replicas, got %v", status.UnknownReplicas)
	}
	for _, replica := range testReplicas() {
		if !eraser.Erased(replica.ID) {
			t.Fatalf("manifest replica %s should be erased before reconciliation blocks", replica.ID)
		}
	}

	// The host erases the ghost; the next scan no longer sees it, and the
	// confirmation removes the blocker so the transaction can complete.
	scanner.Set(
		netsec.Replica{ID: "r1", Location: "store/key/r1"},
		netsec.Replica{ID: "r2", Location: "store/key/r2"},
		netsec.Replica{ID: "r3", Location: "store/key/r3"},
	)
	status, err = d.ReportErasure(ctx, "ghost")
	if err != nil {
		t.Fatalf("ReportErasure ghost: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion once ghost confirmed, got %s", status.Outcome)
	}
}

func TestPostCompletionScanReopensForNewlyObservedCopy(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	eraser := newFakeEraser()
	scanner := newFakeScanner()
	c := newCoordinator(t, store, netsec.WithEraser(eraser), netsec.WithScanner(scanner))

	v := mustCreateVolume(t, c, "vault", testReplicas())
	scanner.Set(testReplicas()...)
	d := mustDestroy(t, c, v)
	if status, _ := d.Status(ctx); status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected initial completion, got %s", status.Outcome)
	}

	// A later verification sweep discovers a previously unseen copy.
	scanner.Set(
		netsec.Replica{ID: "late-ghost", Location: "leaked/key/late"},
		netsec.Replica{ID: "r1", Location: "store/key/r1"},
		netsec.Replica{ID: "r2", Location: "store/key/r2"},
		netsec.Replica{ID: "r3", Location: "store/key/r3"},
	)
	status, err := d.Step(ctx)
	if !errors.Is(err, netsec.ErrUnknownReplica) {
		t.Fatalf("expected ErrUnknownReplica from verification, got %v", err)
	}
	if status.Outcome != netsec.StatusIncomplete || len(status.UnknownReplicas) != 1 {
		t.Fatalf("expected reopened incomplete transaction, got %+v", status)
	}

	scanner.Set(testReplicas()...)
	status, err = d.ReportErasure(ctx, "late-ghost")
	if err != nil {
		t.Fatalf("ReportErasure late-ghost: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected re-completion, got %s", status.Outcome)
	}
}

func TestExplicitLeaseReleaseAllowsErasure(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	eraser := newFakeEraser()
	c := newCoordinator(t, store, netsec.WithEraser(eraser))

	v := mustCreateVolume(t, c, "vault", testReplicas())
	lease, err := v.GrantLease(ctx, time.Hour)
	if err != nil {
		t.Fatalf("GrantLease: %v", err)
	}
	d := mustDestroy(t, c, v)

	if _, err := d.Step(ctx); err != nil {
		t.Fatalf("Step while leased: %v", err)
	}
	if status, _ := d.Status(ctx); status.Phase != netsec.PhaseDraining {
		t.Fatalf("expected draining, got %s", status.Phase)
	}
	if err := v.ReleaseLease(ctx, lease.ID); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	status, err := d.Step(ctx)
	if err != nil {
		t.Fatalf("Step after release: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected complete, got %s", status.Outcome)
	}
}

func TestHostDrivenErasureConfirmationsAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	c := newCoordinator(t, store)

	v := mustCreateVolume(t, c, "vault", testReplicas())
	d := mustDestroy(t, c, v)

	for _, id := range []netsec.ReplicaID{"r1", "r2"} {
		if _, err := d.ReportErasure(ctx, id); err != nil {
			t.Fatalf("ReportErasure %s: %v", id, err)
		}
	}
	status, err := d.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.PendingReplicas) != 1 || status.PendingReplicas[0] != "r3" {
		t.Fatalf("expected only r3 pending, got %v", status.PendingReplicas)
	}

	if _, err := d.ReportErasure(ctx, "r1"); !errors.Is(err, netsec.ErrAlreadyErased) {
		t.Fatalf("expected ErrAlreadyErased, got %v", err)
	}
	if _, err := d.ReportErasure(ctx, "r3"); err != nil {
		t.Fatalf("final ReportErasure: %v", err)
	}
	status, err = d.Status(ctx)
	if err != nil {
		t.Fatalf("final Status: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected complete, got %s", status.Outcome)
	}
}
