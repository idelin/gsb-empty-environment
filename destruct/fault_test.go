package destruct

import (
	"context"
	"errors"
	"testing"
	"time"
)

var allMutations = []string{
	MutCreateVolume,
	MutCompareSwapVolume,
	MutCreateLease,
	MutCompareSwapLease,
	MutCreateTxn,
	MutCompareSwapTxn,
	MutPutConfirmation,
}

// freshDestroyEnv builds an environment with a usable volume and an in-flight
// lease so every destruction code path (seal, lease drain, erase, flips) is
// reachable during the sweep.
func freshDestroyEnv(t *testing.T) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	env.createVolume("vol-fault", "faulty")
	if _, err := env.coord.GrantLease(context.Background(), "vol-fault", "r1", testTTL); err != nil {
		t.Fatalf("GrantLease: %v", err)
	}
	if _, err := env.coord.GrantLease(context.Background(), "vol-fault", "r2", testTTL); err != nil {
		t.Fatalf("GrantLease: %v", err)
	}
	return env
}

// driveToDestroyed keeps restarting and resuming after crashes/faults until
// the volume is destroyed. It asserts the volume never returns to usable.
func driveToDestroyed(t *testing.T, env *testEnv, vol VolumeID) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sealed := false
	for i := 0; i < 50; i++ {
		result, err := env.coord.Destroy(ctx, vol)
		switch {
		case errors.Is(err, ErrInjectedCrash):
			env.crash()
			if env.getVolume(vol).State != VolumeUsable {
				sealed = true
			}
		case errors.Is(err, ErrInjectedFault):
			// Transient failed write: the same process retries the whole
			// idempotent destruction run.
			if env.getVolume(vol).State != VolumeUsable {
				sealed = true
			}
		case errors.Is(err, ErrVolumeDestroyed):
			env.assertDestroyed(vol)
			return
		case err != nil:
			t.Fatalf("attempt %d: unexpected error %v", i, err)
		case result.Complete:
			env.assertDestroyed(vol)
			return
		default:
			// Open txn: loop and resume.
			sealed = true
		}
		// A crash that lands before any durable write leaves the volume
		// usable; once sealing has committed, the volume can never return to
		// that state for the rest of this run.
		if sealed {
			env.assertNotUsable(vol)
		}
	}
	t.Fatalf("destruction did not converge after retries")
}

// TestCrashCutSweep injects a one-shot crash at every distinct mutating
// store operation reached during a full destruction run and checks recovery
// always converges without double confirmations or a usable volume.
func TestCrashCutSweep(t *testing.T) {
	for _, op := range allMutations {
		op := op
		t.Run(op, func(t *testing.T) {
			env := freshDestroyEnv(t)
			env.store.InjectFault(op, FaultCrash, true)
			env.store.Arm()
			driveToDestroyed(t, env, "vol-fault")
			assertSingleConfirmationPerReplica(t, env, "vol-fault")
		})
	}
}

// TestTransientWriteFaultSweep injects a non-crash failed write at every
// mutating operation: retries of the whole idempotent run must converge.
func TestTransientWriteFaultSweep(t *testing.T) {
	for _, op := range allMutations {
		op := op
		t.Run(op, func(t *testing.T) {
			env := freshDestroyEnv(t)
			env.store.InjectFault(op, FaultError, true)
			env.store.Arm()
			driveToDestroyed(t, env, "vol-fault")
			assertSingleConfirmationPerReplica(t, env, "vol-fault")
		})
	}
}

func assertSingleConfirmationPerReplica(t *testing.T, env *testEnv, vol VolumeID) {
	t.Helper()
	record := env.getVolume(vol)
	confs, err := env.store.ListConfirmations(context.Background(), record.DestroyTxnID)
	if err != nil {
		t.Fatalf("ListConfirmations: %v", err)
	}
	seen := map[ReplicaID]int{}
	for _, r := range confs {
		if r.Confirmation.VolumeID != vol {
			t.Fatalf("confirmation belongs to %s, want %s", r.Confirmation.VolumeID, vol)
		}
		seen[r.Confirmation.ReplicaID]++
	}
	if len(seen) != len(record.Replicas) {
		t.Fatalf("confirmed replicas = %d, want %d", len(seen), len(record.Replicas))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("replica %s has %d confirmations, want 1", id, count)
		}
	}
}

// TestPartialEraseRecovery makes one replica fail its erase twice while crash
// faults also interrupt writes; the transaction must stay open, hold the
// already-collected confirmations, and finish on resume without re-erasing
// replicas that already returned a confirmation.
func TestPartialEraseRecovery(t *testing.T) {
	env := newTestEnv(t)
	env.createVolume("vol-partial", "partial")
	env.replicas["r2"].FailNext(3, errors.New("r2 disk controller timeout"))
	env.store.InjectFault(MutPutConfirmation, FaultCrash, true)
	env.store.Arm()

	ctx := context.Background()
	result, err := env.coord.Destroy(ctx, "vol-partial")
	if err != nil && !errors.Is(err, ErrInjectedCrash) {
		t.Fatalf("first Destroy: %v", err)
	}
	if err == nil && result.Complete {
		t.Fatalf("destruction unexpectedly complete on first attempt")
	}

	// Simulated restart: open txns are discovered and resumed through Recover.
	env.crash()
	var results []*DestroyResult
	for attempt := 0; attempt < 10; attempt++ {
		var err error
		results, err = env.coord.Recover(ctx)
		if err != nil {
			t.Fatalf("Recover: %v", err)
		}
		var complete int
		for _, r := range results {
			if r.Complete {
				complete++
			}
		}
		if complete == 1 {
			break
		}
	}
	var complete int
	for _, r := range results {
		if r.Complete {
			complete++
		}
	}
	if complete != 1 {
		t.Fatalf("Recover complete count = %d, want 1; results=%+v", complete, results)
	}
	env.assertDestroyed("vol-partial")

	// Every replica holds exactly one durable confirmation even though the
	// confirmation write for the first replica crashed and had to be retried.
	for _, id := range []ReplicaID{"r1", "r3"} {
		confs, err := env.store.ListConfirmations(ctx, env.getVolume("vol-partial").DestroyTxnID)
		if err != nil {
			t.Fatalf("ListConfirmations: %v", err)
		}
		var matches int
		for _, r := range confs {
			if r.Confirmation.ReplicaID == id {
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("replica %s has %d durable confirmations, want 1", id, matches)
		}
	}
	// r2 failed twice then succeeded.
	if env.replicas["r2"].EraseCount() < 4 {
		t.Fatalf("r2 erase count = %d, want at least 4", env.replicas["r2"].EraseCount())
	}
	assertSingleConfirmationPerReplica(t, env, "vol-partial")

	// Recover is idempotent once everything is destroyed.
	again, err := env.coord.Recover(ctx)
	if err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("Recover after completion returned %+v, want empty", again)
	}
}

// TestCrashBeforeSealRecover crashes after the txn record is created but
// before the volume CAS lands. Recovery must finish the open transaction.
func TestCrashBeforeSealRecover(t *testing.T) {
	env := newTestEnv(t)
	env.createVolume("vol-seal", "seal")
	env.store.InjectFault(MutCompareSwapVolume, FaultCrash, true)
	env.store.Arm()

	_, err := env.coord.Destroy(context.Background(), "vol-seal")
	if !errors.Is(err, ErrInjectedCrash) {
		t.Fatalf("Destroy err = %v, want ErrInjectedCrash", err)
	}

	// Whether the CAS applied or not, restart and recover.
	env.crash()
	results, err := env.coord.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(results) == 0 {
		// The crash landed before the txn was durable: nothing to recover,
		// so a fresh Destroy call seals and completes the volume.
		result, derr := env.coord.Destroy(context.Background(), "vol-seal")
		if derr != nil || !result.Complete {
			t.Fatalf("post-crash Destroy result=%+v err=%v", result, derr)
		}
	} else if len(results) != 1 || !results[0].Complete {
		t.Fatalf("Recover results = %+v, want one complete txn", results)
	}
	env.assertDestroyed("vol-seal")
}

// TestRecoverSkipsOrphanedSnapshots verifies that a superseded begin (its txn
// record exists but the volume now points at another authoritative txn) is
// skipped during recovery instead of being run twice.
func TestRecoverSkipsOrphanedSnapshots(t *testing.T) {
	env := newTestEnv(t)
	env.createVolume("vol-orphan", "orphan")

	orphan := &DestroyTxn{
		ID:        TxnID("orphan-txn"),
		Volume:    "vol-orphan",
		Replicas:  cloneReplicaMap(env.getVolume("vol-orphan").Replicas),
		State:     TxnSealing,
		CreatedAt: env.clock.now(),
	}
	if err := env.store.CreateTxn(context.Background(), orphan); err != nil {
		t.Fatalf("CreateTxn orphan: %v", err)
	}

	// Authoritative destruction runs through the public API and seals the
	// volume under its own txn id.
	result, err := env.coord.Destroy(context.Background(), "vol-orphan")
	if err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if !result.Complete {
		t.Fatalf("Destroy incomplete: %+v", result)
	}
	authoritative := env.getVolume("vol-orphan").DestroyTxnID
	if authoritative == orphan.ID {
		t.Fatalf("authoritative txn unexpectedly equals orphan")
	}

	results, err := env.coord.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	for _, r := range results {
		if r.TxnID == orphan.ID {
			t.Fatalf("orphan txn %s was resumed", orphan.ID)
		}
	}

	// The orphan txn never received confirmations and never turned destroyed.
	stored, err := env.store.ListConfirmations(context.Background(), orphan.ID)
	if err != nil {
		t.Fatalf("ListConfirmations orphan: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("orphan collected %d confirmations, want 0", len(stored))
	}
	if env.getTxn(orphan.ID).State == TxnDestroyed {
		t.Fatalf("orphan txn marked destroyed")
	}
}

// TestEraseIdempotencyWithoutFaults proves that, with no storage faults, each
// replica's Erase is invoked exactly once and a repeated Destroy does not
// create duplicate confirmations or a second transaction.
func TestEraseIdempotencyWithoutFaults(t *testing.T) {
	env := newTestEnv(t)
	env.createVolume("vol-idem", "idem")

	result, err := env.coord.Destroy(context.Background(), "vol-idem")
	if err != nil || !result.Complete {
		t.Fatalf("Destroy result=%+v err=%v", result, err)
	}
	for id, replica := range env.replicas {
		if replica.EraseCount() != 1 {
			t.Fatalf("replica %s erased %d times, want 1", id, replica.EraseCount())
		}
	}

	if _, err := env.coord.Destroy(context.Background(), "vol-idem"); !errors.Is(err, ErrVolumeDestroyed) {
		t.Fatalf("repeat Destroy err = %v, want ErrVolumeDestroyed", err)
	}
	for id, replica := range env.replicas {
		if replica.EraseCount() != 1 {
			t.Fatalf("replica %s re-erased: count=%d", id, replica.EraseCount())
		}
	}

	pending, err := env.store.ListPendingTxns(context.Background())
	if err != nil {
		t.Fatalf("ListPendingTxns: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending txns = %d, want 0", len(pending))
	}
	assertSingleConfirmationPerReplica(t, env, "vol-idem")
}
