package txmut_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"example.org/netsec/q0416/txmut"
)

// fakeService models a handshake server whose session state is an ordered
// log of applied messages. Snapshot/Restore copy that log; Deliver parses
// the candidate and appends to the log. Hooks inject failures per phase,
// and the deliver hook may run after a partial mutation to emulate a
// corrupted message that still changed server state.
type fakeService struct {
	mu        sync.Mutex
	state     []string
	snapshots map[string][]string
	nextSnap  int

	snapshotHook func(call int) error
	deliverHook  func(call int, candidate []byte) error
	restoreHook  func(call int, snapshotID string) error

	snapshotCalls int
	deliverCalls  int
	restoreCalls  int
}

func newFakeService(baseline ...string) *fakeService {
	return &fakeService{
		state:     append([]string(nil), baseline...),
		snapshots: make(map[string][]string),
	}
}

func (f *fakeService) Snapshot(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snapshotCalls++
	if f.snapshotHook != nil {
		if err := f.snapshotHook(f.snapshotCalls); err != nil {
			return "", err
		}
	}
	f.nextSnap++
	id := fmt.Sprintf("snap-%d", f.nextSnap)
	f.snapshots[id] = append([]string(nil), f.state...)
	return id, nil
}

func (f *fakeService) Restore(ctx context.Context, snapshotID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restoreCalls++
	if f.restoreHook != nil {
		if err := f.restoreHook(f.restoreCalls, snapshotID); err != nil {
			return err
		}
	}
	snap, ok := f.snapshots[snapshotID]
	if !ok {
		return fmt.Errorf("unknown snapshot %q", snapshotID)
	}
	f.state = append([]string(nil), snap...)
	return nil
}

// Deliver applies the candidate to the session state and replies with the
// full state log, so tests can observe exactly what the server remembers.
func (f *fakeService) Deliver(ctx context.Context, candidate []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deliverCalls++
	f.state = append(f.state, "rx:"+string(candidate))
	if f.deliverHook != nil {
		if err := f.deliverHook(f.deliverCalls, candidate); err != nil {
			return nil, err
		}
	}
	return []byte(strings.Join(f.state, ",")), nil
}

func (f *fakeService) currentState() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.state...)
}

// memoryStore is a ReceiptStore with an injectable failure.
type memoryStore struct {
	mu       sync.Mutex
	receipts []txmut.Receipt
	err      error
}

func (m *memoryStore) WriteReceipt(ctx context.Context, r txmut.Receipt) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.receipts = append(m.receipts, r)
	return nil
}

func (m *memoryStore) all() []txmut.Receipt {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]txmut.Receipt(nil), m.receipts...)
}

func asPhaseErr(t *testing.T, err error) *txmut.Error {
	t.Helper()
	var pe *txmut.Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *txmut.Error, got %T: %v", err, err)
	}
	return pe
}

// Sequential candidates must each observe only the baseline state: the
// corrupted message of trial N must never leak into trial N+1.
func TestTrialsDoNotCrossContaminate(t *testing.T) {
	svc := newFakeService("baseline")
	store := &memoryStore{}
	ex, err := txmut.NewExecutor(context.Background(), svc, store)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	candidates := []string{"AAAA", "BBBB", "CCCC"}
	for i, c := range candidates {
		receipt, err := ex.RunTrial(context.Background(), fmt.Sprintf("trial-%d", i), []byte(c))
		if err != nil {
			t.Fatalf("RunTrial(%s): %v", c, err)
		}
		got := string(receipt.Reply)
		want := "baseline,rx:" + c
		if got != want {
			t.Fatalf("trial %d saw state %q, want %q (state leaked across samples)", i, got, want)
		}
		if receipt.SnapshotID != ex.SnapshotID() {
			t.Fatalf("trial %d used snapshot %q, want fixed %q", i, receipt.SnapshotID, ex.SnapshotID())
		}
	}

	if ex.State() != txmut.StateReady {
		t.Fatalf("executor state = %v, want ready for reuse", ex.State())
	}
	if got := svc.currentState(); len(got) != 1 || got[0] != "baseline" {
		t.Fatalf("server state after trials = %v, want [baseline]", got)
	}
	if n := len(store.all()); n != len(candidates) {
		t.Fatalf("stored %d receipts, want %d", n, len(candidates))
	}
}

// A snapshot-phase failure must abort executor construction entirely.
func TestSnapshotFailureAbortsConstruction(t *testing.T) {
	svc := newFakeService("baseline")
	boom := errors.New("snapshot backend down")
	svc.snapshotHook = func(call int) error { return boom }

	_, err := txmut.NewExecutor(context.Background(), svc, &memoryStore{})
	pe := asPhaseErr(t, err)
	if pe.Phase != txmut.PhaseSnapshot {
		t.Fatalf("phase = %v, want %v", pe.Phase, txmut.PhaseSnapshot)
	}
	if !errors.Is(err, boom) {
		t.Fatalf("error %v does not wrap %v", err, boom)
	}
}

// A receive-phase failure leaves partial server state behind; the instance
// must be quarantined so no later sample can observe or extend that state.
func TestReceiveFailureQuarantinesInstance(t *testing.T) {
	svc := newFakeService("baseline")
	boom := errors.New("connection reset by peer")
	svc.deliverHook = func(call int, candidate []byte) error {
		if call == 2 {
			return boom // partial mutation already applied by Deliver
		}
		return nil
	}
	store := &memoryStore{}
	ex, err := txmut.NewExecutor(context.Background(), svc, store)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	if _, err := ex.RunTrial(context.Background(), "trial-0", []byte("AAAA")); err != nil {
		t.Fatalf("trial-0: %v", err)
	}

	_, err = ex.RunTrial(context.Background(), "trial-1", []byte("BAD!"))
	pe := asPhaseErr(t, err)
	if pe.Phase != txmut.PhaseReceive {
		t.Fatalf("phase = %v, want %v", pe.Phase, txmut.PhaseReceive)
	}
	if ex.State() != txmut.StateQuarantined {
		t.Fatalf("state = %v, want quarantined", ex.State())
	}

	// The corrupted trial left residue on the server...
	if got := svc.currentState(); len(got) != 2 || got[1] != "rx:BAD!" {
		t.Fatalf("server state = %v, want residue from corrupted trial", got)
	}
	// ...but subsequent samples are rejected before touching the service.
	deliverCalls := svc.deliverCalls
	if _, err := ex.RunTrial(context.Background(), "trial-2", []byte("CCCC")); !errors.Is(err, txmut.ErrQuarantined) {
		t.Fatalf("trial-2 err = %v, want ErrQuarantined", err)
	}
	if svc.deliverCalls != deliverCalls {
		t.Fatalf("quarantined instance still delivered candidates to the service")
	}
	// No receipt may exist for the incomplete trial.
	if n := len(store.all()); n != 1 {
		t.Fatalf("stored %d receipts, want 1 (only the complete trial)", n)
	}
}

// A restore failure after the receipt is written keeps the receipt but
// quarantines the dirty instance.
func TestRestoreFailureAfterReceiptQuarantines(t *testing.T) {
	svc := newFakeService("baseline")
	boom := errors.New("restore: snapshot store unreachable")
	svc.restoreHook = func(call int, snapshotID string) error {
		if call == 2 { // call 1 is the pre-trial restore, call 2 the post-receipt one
			return boom
		}
		return nil
	}
	store := &memoryStore{}
	ex, err := txmut.NewExecutor(context.Background(), svc, store)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	_, err = ex.RunTrial(context.Background(), "trial-0", []byte("AAAA"))
	pe := asPhaseErr(t, err)
	if pe.Phase != txmut.PhaseRestore {
		t.Fatalf("phase = %v, want %v", pe.Phase, txmut.PhaseRestore)
	}
	if ex.State() != txmut.StateQuarantined {
		t.Fatalf("state = %v, want quarantined", ex.State())
	}
	// The complete trial receipt survived even though the instance did not.
	receipts := store.all()
	if len(receipts) != 1 || receipts[0].TrialID != "trial-0" {
		t.Fatalf("receipts = %+v, want the completed trial-0 receipt", receipts)
	}
	if _, err := ex.RunTrial(context.Background(), "trial-1", []byte("BBBB")); !errors.Is(err, txmut.ErrQuarantined) {
		t.Fatalf("trial-1 err = %v, want ErrQuarantined", err)
	}
}

// A restore failure before delivery must quarantine the instance without
// delivering the candidate at all.
func TestRestoreFailureBeforeDeliveryQuarantines(t *testing.T) {
	svc := newFakeService("baseline")
	boom := errors.New("restore: checksum mismatch")
	svc.restoreHook = func(call int, snapshotID string) error { return boom }
	ex, err := txmut.NewExecutor(context.Background(), svc, &memoryStore{})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	_, err = ex.RunTrial(context.Background(), "trial-0", []byte("AAAA"))
	pe := asPhaseErr(t, err)
	if pe.Phase != txmut.PhaseRestore {
		t.Fatalf("phase = %v, want %v", pe.Phase, txmut.PhaseRestore)
	}
	if svc.deliverCalls != 0 {
		t.Fatalf("candidate delivered despite failed pre-trial restore")
	}
	if ex.State() != txmut.StateQuarantined {
		t.Fatalf("state = %v, want quarantined", ex.State())
	}
}

// If the receipt cannot be persisted, the trial outcome is unknown and the
// instance must not be reused.
func TestReceiptFailureQuarantines(t *testing.T) {
	svc := newFakeService("baseline")
	boom := errors.New("journal disk full")
	store := &memoryStore{err: boom}
	ex, err := txmut.NewExecutor(context.Background(), svc, store)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}

	_, err = ex.RunTrial(context.Background(), "trial-0", []byte("AAAA"))
	pe := asPhaseErr(t, err)
	if pe.Phase != txmut.PhaseReceipt {
		t.Fatalf("phase = %v, want %v", pe.Phase, txmut.PhaseReceipt)
	}
	if ex.State() != txmut.StateQuarantined {
		t.Fatalf("state = %v, want quarantined", ex.State())
	}
	if _, err := ex.RunTrial(context.Background(), "trial-1", []byte("BBBB")); !errors.Is(err, txmut.ErrQuarantined) {
		t.Fatalf("trial-1 err = %v, want ErrQuarantined", err)
	}
}

// A fresh executor on a clean service still sees only baseline state after
// a sibling instance was quarantined mid-corruption.
func TestFreshInstanceAfterQuarantineSeesBaseline(t *testing.T) {
	dirty := newFakeService("baseline")
	dirty.deliverHook = func(call int, candidate []byte) error {
		return errors.New("corrupted handshake")
	}
	ex1, err := txmut.NewExecutor(context.Background(), dirty, &memoryStore{})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	if _, err := ex1.RunTrial(context.Background(), "trial-0", []byte("BAD!")); err == nil {
		t.Fatalf("expected receive failure")
	}

	clean := newFakeService("baseline")
	ex2, err := txmut.NewExecutor(context.Background(), clean, &memoryStore{})
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	receipt, err := ex2.RunTrial(context.Background(), "trial-1", []byte("AAAA"))
	if err != nil {
		t.Fatalf("trial-1: %v", err)
	}
	if got, want := string(receipt.Reply), "baseline,rx:AAAA"; got != want {
		t.Fatalf("fresh instance saw %q, want %q", got, want)
	}
}
