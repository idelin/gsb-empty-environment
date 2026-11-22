package refresh_test

import (
	"math/big"
	"testing"

	"prescreen.local/ddd-9449-20261122-v2/refresh"
)

func seedStore(t *testing.T, h *harness, secret *big.Int) *refresh.PausableStore {
	t.Helper()
	store := refresh.NewPausableStore()
	for _, r := range h.recipients {
		share := refresh.StoredShare{
			Round:       h.round - 1,
			RecipientID: r,
			Scalar: h.field.Eval(
				masterPolynomial(h, secret),
				h.coord(t, r)),
		}
		if err := store.PutCurrent(share); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return store
}

// masterPolynomial defines the pre-refresh sharing. It is degree < k and its
// constant term is the secret; share commits use the same coordinates the
// refresh increments target.
func masterPolynomial(h *harness, secret *big.Int) []*big.Int {
	coeffs := make([]*big.Int, h.threshold)
	coeffs[0] = secret
	for i := 1; i < h.threshold; i++ {
		coeffs[i] = big.NewInt(int64(10 + i))
	}
	return coeffs
}

func commitRequest(t *testing.T, h *harness, store *refresh.PausableStore, recipient string) refresh.ShareCommitRequest {
	t.Helper()
	current, ok, err := store.Current(recipient)
	if err != nil || !ok {
		t.Fatalf("current share: %v ok=%v", err, ok)
	}
	var deltas []*big.Int
	for _, sender := range h.senders {
		deltas = append(deltas, h.delta(t, sender, recipient))
	}
	return refresh.ShareCommitRequest{
		Round:       h.round,
		RecipientID: recipient,
		OldShare:    current,
		Deltas:      deltas,
		DeltaDigest: h.digest,
	}
}

func expectedNewShare(t *testing.T, h *harness, store *refresh.PausableStore, recipient string) *big.Int {
	t.Helper()
	old, ok, _ := store.Current(recipient)
	if !ok {
		t.Fatal("missing old share")
	}
	var deltas []*big.Int
	for _, sender := range h.senders {
		deltas = append(deltas, h.delta(t, sender, recipient))
	}
	return h.field.Sum(append([]*big.Int{old.Scalar}, deltas...)...)
}

func TestRestartAfterStageAppliesExactlyOnce(t *testing.T) {
	h := standardHarness(t)
	store := seedStore(t, h, big.NewInt(42))
	req := commitRequest(t, h, store, "r1")
	want := h.field.Add(req.OldShare.Scalar, h.field.Sum(req.Deltas...))

	store.ArmCrash(refresh.CrashAfterStage)
	if _, err := h.engine.StageAndCommit(store, req); err == nil {
		t.Fatal("expected simulated crash after stage")
	}
	if got, _, _ := store.Current("r1"); got.Scalar.Cmp(req.OldShare.Scalar) != 0 {
		t.Fatal("share changed before stage completed")
	}

	// Re-issuing the same request resumes the staged row instead of re-adding.
	res, err := h.engine.StageAndCommit(store, req)
	if err != nil {
		t.Fatalf("recover via re-issue: %v", err)
	}
	if res.Outcome != refresh.OutcomeRecovered {
		t.Fatalf("outcome = %s, want recovered", res.Outcome)
	}
	if res.CurrentShare.Scalar.Cmp(want) != 0 {
		t.Fatalf("share after recovery = %v, want %v", res.CurrentShare.Scalar, want)
	}

	res2, err := h.engine.StageAndCommit(store, req)
	if err != nil {
		t.Fatalf("idempotent re-call: %v", err)
	}
	if res2.Outcome != refresh.OutcomeNoop || res2.CurrentShare.Scalar.Cmp(want) != 0 {
		t.Fatalf("second call not idempotent: %+v", res2)
	}
}

func TestRestartAfterReplaceCompletesCommit(t *testing.T) {
	h := standardHarness(t)
	store := seedStore(t, h, big.NewInt(42))
	req := commitRequest(t, h, store, "r2")
	want := expectedNewShare(t, h, store, "r2")

	store.ArmCrash(refresh.CrashAfterReplace)
	if _, err := h.engine.StageAndCommit(store, req); err == nil {
		t.Fatal("expected simulated crash after replace")
	}
	res, err := h.engine.Recover(store, "r2")
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if res.Outcome != refresh.OutcomeRecovered || res.CurrentShare.Scalar.Cmp(want) != 0 {
		t.Fatalf("recovery result = %+v, want scalar %v", res, want)
	}
	if _, ok, _ := store.LoadStage("r2"); ok {
		t.Fatal("staged marker survives after recovery")
	}

	res2, err := h.engine.Recover(store, "r2")
	if err != nil || res2.Outcome != refresh.OutcomeNoop {
		t.Fatalf("clean recover = %+v, err=%v", res2, err)
	}
}

func TestSnapshotRestoreAcrossRestart(t *testing.T) {
	h := standardHarness(t)
	store := seedStore(t, h, big.NewInt(42))
	req := commitRequest(t, h, store, "r3")
	before := store.Snapshot()

	store.ArmCrash(refresh.CrashAfterStage)
	if _, err := h.engine.StageAndCommit(store, req); err == nil {
		t.Fatal("expected simulated crash after stage")
	}

	// Rebuild from a pre-round backup and replay the request once.
	store.Restore(before)
	res, err := h.engine.StageAndCommit(store, req)
	if err != nil {
		t.Fatalf("post-restore apply: %v", err)
	}
	want := h.field.Add(req.OldShare.Scalar, h.field.Sum(req.Deltas...))
	if res.CurrentShare.Scalar.Cmp(want) != 0 {
		t.Fatalf("post-restore share = %v, want %v", res.CurrentShare.Scalar, want)
	}
}

func TestNonAdjacentRoundRejected(t *testing.T) {
	h := standardHarness(t)
	store := seedStore(t, h, big.NewInt(42))
	req := commitRequest(t, h, store, "r1")
	req.Round = h.round + 5

	if _, err := h.engine.StageAndCommit(store, req); err == nil {
		t.Fatal("non-adjacent round must be rejected")
	}
}
