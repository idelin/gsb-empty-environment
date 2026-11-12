package netsec_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	netsec "example.org/netsec/q0338"
)

func TestDelayedConfirmationBecomesVisibleAndRestartStaysIdempotent(t *testing.T) {
	ctx := context.Background()
	fault := netsec.NewFaultInjectingStore()
	c1 := newCoordinator(t, fault)

	v := mustCreateVolume(t, c1, "vault", testReplicas())
	d := mustDestroy(t, c1, v)

	// r1 commits immediately. The r2 confirmation is acknowledged by the
	// storage driver but its durable commit is delayed.
	if _, err := d.ReportErasure(ctx, "r1"); err != nil {
		t.Fatalf("ReportErasure r1: %v", err)
	}
	fault.DelayPuts()
	if _, err := d.ReportErasure(ctx, "r2"); err != nil {
		t.Fatalf("ReportErasure r2 (delayed): %v", err)
	}
	if fault.PendingCount() == 0 {
		t.Fatal("expected withheld r2 confirmation")
	}

	// While withheld, durable state still shows only r1; a concurrent reader
	// cannot observe the uncommitted confirmation.
	if data, err := fault.Committed().Get(ctx, "destroy/"+string(v.ID())+"/record"); err != nil {
		t.Fatalf("read record: %v", err)
	} else if strings.Contains(string(data), "\"r2\": true") {
		t.Fatal("withheld confirmation must not be durable yet")
	}

	// The delayed commit now lands (late delivery): r2 becomes durable.
	if !fault.DeliverPending(ctx) {
		t.Fatal("expected the withheld confirmation to become durable")
	}
	fault.StopDelayPuts()
	status, err := d.Status(ctx)
	if err != nil {
		t.Fatalf("Status after late delivery: %v", err)
	}
	if len(status.Erased) != 2 {
		t.Fatalf("expected r1 and r2 confirmed, got %v", status.Erased)
	}

	// A crash after the late commit leaves r1 and r2 durable; the restarted
	// process resumes and a retransmitted r2 is idempotent.
	fault.Crash()
	c2 := newCoordinator(t, fault.Committed())
	pending, err := c2.RecoverDestructions(ctx)
	if err != nil {
		t.Fatalf("RecoverDestructions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending transaction, got %d", len(pending))
	}
	if _, err := pending[0].ReportErasure(ctx, "r2"); !errors.Is(err, netsec.ErrAlreadyErased) {
		t.Fatalf("expected ErrAlreadyErased on retransmit, got %v", err)
	}
	status, err = pending[0].Step(ctx)
	if err != nil {
		t.Fatalf("resumed Step: %v", err)
	}
	if status.Outcome != netsec.StatusInProgress {
		t.Fatalf("expected in-progress until r3 confirmed, got %s", status.Outcome)
	}
	status, err = pending[0].ReportErasure(ctx, "r3")
	if err != nil {
		t.Fatalf("ReportErasure r3: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion, got %s", status.Outcome)
	}
}

func TestCrashResumeContinuesSameTransactionAndStaysFrozen(t *testing.T) {
	ctx := context.Background()
	fault := netsec.NewFaultInjectingStore()
	clock := newFakeClock()
	eraser := newFakeEraser()

	c1, err := netsec.NewCoordinator(fault, netsec.WithClock(clock), netsec.WithEraser(eraser))
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	v1 := mustCreateVolume(t, c1, "vault", testReplicas())

	// The transaction is durably established and parked in draining via an
	// active lease. Withholding later writes models progression that never
	// reaches stable media before the crash.
	lease, err := v1.GrantLease(ctx, time.Hour)
	if err != nil {
		t.Fatalf("GrantLease: %v", err)
	}
	d1 := mustDestroy(t, c1, v1)
	if status, err := d1.Status(ctx); err != nil || status.Phase != netsec.PhaseDraining {
		t.Fatalf("expected parked in draining, got %+v err=%v", status, err)
	}
	if lease.ID == "" {
		t.Fatal("expected a durable lease identifier")
	}
	fault.DelayPuts()
	clock.Advance(2 * time.Hour)
	if _, err := d1.Step(ctx); err != nil {
		t.Fatalf("Step under delay: %v", err)
	}
	if fault.PendingCount() == 0 {
		t.Fatal("expected withheld writes")
	}
	fault.Crash()

	// A new process opens the same durable media. Pending writes are gone.
	c2, err := netsec.NewCoordinator(fault.Committed(), netsec.WithClock(clock), netsec.WithEraser(eraser))
	if err != nil {
		t.Fatalf("recovered coordinator: %v", err)
	}
	if _, err := c2.OpenVolume(ctx, "vault"); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("destroying volume must not reopen, got %v", err)
	}
	pending, err := c2.RecoverDestructions(ctx)
	if err != nil {
		t.Fatalf("RecoverDestructions: %v", err)
	}
	if len(pending) != 1 || pending[0].ID() != v1.ID() {
		t.Fatalf("expected resumed transaction %s, got %d", v1.ID(), len(pending))
	}

	status, err := pending[0].Step(ctx)
	if err != nil {
		t.Fatalf("resumed Step: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected resumed completion, got %s phase=%s", status.Outcome, status.Phase)
	}
	for _, replica := range testReplicas() {
		if !eraser.Erased(replica.ID) {
			t.Fatalf("replica %s not erased after resume", replica.ID)
		}
	}

	// Late durable deliveries from the crashed process must not corrupt the
	// completed generation.
	for fault.DeliverPending(ctx) {
	}
	status, err = pending[0].Status(ctx)
	if err != nil {
		t.Fatalf("Status after late delivery: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("late delivery changed outcome: %s", status.Outcome)
	}
}

func TestLateConfirmationsAndVolumeNameReuse(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()

	c1 := newCoordinator(t, store)
	v1 := mustCreateVolume(t, c1, "vault", testReplicas())
	d1 := mustDestroy(t, c1, v1)

	// Two of three confirmations arrive before the host process restarts.
	for _, id := range []netsec.ReplicaID{"r1", "r2"} {
		if _, err := d1.ReportErasure(ctx, id); err != nil {
			t.Fatalf("ReportErasure %s: %v", id, err)
		}
	}

	c2 := newCoordinator(t, store)
	pending, err := c2.RecoverDestructions(ctx)
	if err != nil {
		t.Fatalf("RecoverDestructions: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending transaction, got %d", len(pending))
	}

	// Name reuse is blocked while the old generation is still destroying.
	if _, err := c2.CreateVolume(ctx, "vault", testReplicas()); !errors.Is(err, netsec.ErrNameInUse) {
		t.Fatalf("expected ErrNameInUse during destruction, got %v", err)
	}

	// A duplicated late confirmation is rejected as already recorded instead
	// of being double-applied.
	if _, err := pending[0].ReportErasure(ctx, "r1"); !errors.Is(err, netsec.ErrAlreadyErased) {
		t.Fatalf("expected ErrAlreadyErased, got %v", err)
	}

	// The genuinely late final confirmation completes the old generation.
	status, err := pending[0].ReportErasure(ctx, "r3")
	if err != nil {
		t.Fatalf("late r3 report: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion after late r3, got %s", status.Outcome)
	}

	// The name is reusable now, producing a distinct generation whose replica
	// IDs may safely collide with the old generation.
	v2, err := c2.CreateVolume(ctx, "vault", testReplicas())
	if err != nil {
		t.Fatalf("CreateVolume reuse after destroy: %v", err)
	}
	if v2.ID() == v1.ID() {
		t.Fatal("reused name must produce a new volume generation")
	}
	// A stale late report addressed to the old generation cannot affect the
	// new one; the old transaction is closed and refuses further reports.
	if _, err := d1.ReportErasure(ctx, "r2"); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("expected ErrVolumeDestroyed for stale report, got %v", err)
	}

	// The new generation is fully usable and independently destructible.
	if v2.Frozen() {
		t.Fatal("new generation must not inherit the frozen flag")
	}
	d2 := mustDestroy(t, c2, v2)
	for _, replica := range testReplicas() {
		if _, err := d2.ReportErasure(ctx, replica.ID); err != nil {
			t.Fatalf("new generation ReportErasure %s: %v", replica.ID, err)
		}
	}
	status, err = d2.Status(ctx)
	if err != nil {
		t.Fatalf("new generation Status: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected new generation complete, got %s", status.Outcome)
	}
}

func TestInjectedStorageFaultIsRetriableAndDurable(t *testing.T) {
	ctx := context.Background()
	fault := netsec.NewFaultInjectingStore()
	eraser := newFakeEraser()
	c := newCoordinator(t, fault, netsec.WithEraser(eraser))

	v := mustCreateVolume(t, c, "vault", testReplicas())

	// Fail the first durable write of the destruction record itself. The
	// manifest freeze preceding it still commits, so the volume stays
	// prohibited from issuing leases.
	fault.FailPut(func(key string) bool {
		return key == "destroy/"+string(v.ID())+"/record"
	})
	if _, err := c.DestroyVolume(ctx, v); !netsec.IsFaultIOError(err) {
		t.Fatalf("expected injected I/O error, got %v", err)
	}
	fault.FailPut(nil)

	// The fault is one-shot; retrying resumes along the same transaction
	// without creating a duplicate or reviving the volume.
	d, err := c.DestroyVolume(ctx, v)
	if err != nil {
		t.Fatalf("retry DestroyVolume: %v", err)
	}
	status, err := d.Status(ctx)
	if err != nil {
		t.Fatalf("Status after retry: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion after retry, got %s", status.Outcome)
	}
	if _, err := c.OpenVolume(ctx, "vault"); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("expected ErrVolumeDestroyed, got %v", err)
	}
}

func TestErasureBackendFailureLeavesResumableTransaction(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	eraser := newFakeEraser()
	eraser.failFor = map[netsec.ReplicaID]error{
		"r2": errors.New("backend busy"),
	}
	c := newCoordinator(t, store, netsec.WithEraser(eraser))

	v := mustCreateVolume(t, c, "vault", testReplicas())
	d, err := c.DestroyVolume(ctx, v)
	if err == nil {
		t.Fatal("expected erasure backend error")
	}

	// r1 was durably confirmed before the r2 failure; r2's erasure intent is
	// recorded but unconfirmed.
	status, err := d.Status(ctx)
	if err != nil {
		t.Fatalf("Status after backend failure: %v", err)
	}
	if len(status.Erased) != 1 || status.Erased[0] != "r1" {
		t.Fatalf("expected only r1 confirmed, got %v", status.Erased)
	}
	if len(status.PendingReplicas) != 2 {
		t.Fatalf("expected r2 and r3 pending, got %v", status.PendingReplicas)
	}

	delete(eraser.failFor, "r2")
	// After the backend recovers, stepping retries the failed r2 call and
	// then erases r3. The already-confirmed r1 is never repeated.
	status, err = d.Step(ctx)
	if err != nil {
		t.Fatalf("resumed Step: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected completion after backend recovery, got %s", status.Outcome)
	}
	if eraser.CallsFor("r1") != 1 {
		t.Fatalf("confirmed r1 must not be retried, got %d calls", eraser.CallsFor("r1"))
	}
	if eraser.CallsFor("r2") != 2 || eraser.CallsFor("r3") != 1 {
		t.Fatalf("expected r2 twice (failure then retry) and r3 once, got r2=%d r3=%d",
			eraser.CallsFor("r2"), eraser.CallsFor("r3"))
	}
}

func TestUnknownReplicaReportedDirectlyBlocksCompletion(t *testing.T) {
	ctx := context.Background()
	store := netsec.NewMemoryStore()
	c := newCoordinator(t, store)

	v := mustCreateVolume(t, c, "vault", testReplicas())
	d := mustDestroy(t, c, v)

	// A direct confirmation for a copy that is not in the manifest is
	// recorded as resolved; the transaction remains incomplete only while
	// manifest replicas are outstanding.
	status, err := d.ReportErasure(ctx, "leaked")
	if err != nil {
		t.Fatalf("ReportErasure leaked: %v", err)
	}
	for _, id := range []netsec.ReplicaID{"r1", "r2", "r3"} {
		if _, err := d.ReportErasure(ctx, id); err != nil {
			t.Fatalf("ReportErasure %s: %v", id, err)
		}
	}
	status, err = d.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Outcome != netsec.StatusComplete {
		t.Fatalf("expected complete once all copies confirmed, got %s", status.Outcome)
	}

	// A repeated report for the non-manifest copy is a no-op confirmation.
	if _, err := d.ReportErasure(ctx, "leaked"); !errors.Is(err, netsec.ErrVolumeDestroyed) {
		t.Fatalf("expected ErrVolumeDestroyed after completion, got %v", err)
	}
}
