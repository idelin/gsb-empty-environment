package netsec

import (
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestFileStorePersistsCheckedAndConsumedStates(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	path := filepath.Join(t.TempDir(), "state.json")
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	x, y := mustShares(t, f, 6), mustShares(t, f, 7)

	store1, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	h1 := newTestHost(t, p, store1, nil, [3]PartyKey{})
	res, err := h1.Sacrifice(candidate, check)
	if err != nil || !res.Accepted {
		t.Fatalf("Sacrifice: %+v %v", res, err)
	}
	if err := h1.Close(); err != nil {
		t.Fatal(err)
	}

	// A checked candidate survives a clean shutdown and can be consumed later.
	store2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := storeStatus(t, store2, candidate.ID); status != StatusChecked {
		t.Fatalf("candidate after reopen=%q, want checked", status)
	}
	if status, _ := storeStatus(t, store2, check.ID); status != StatusBurned {
		t.Fatalf("check after reopen=%q, want burned", status)
	}
	h2 := newTestHost(t, p, store2, nil, [3]PartyKey{})
	mul, err := h2.Multiply(candidate, x, y)
	if err != nil || !mul.Produced {
		t.Fatalf("Multiply after reopen: %+v %v", mul, err)
	}
	if z, _ := mul.Secret(f); z != f.Mul(6, 7) {
		t.Fatalf("z=%d, want 42", z)
	}
	if err := h2.Close(); err != nil {
		t.Fatal(err)
	}

	// Consumption is durable across another restart.
	store3, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store3.Close()
	if status, _ := storeStatus(t, store3, candidate.ID); status != StatusConsumed {
		t.Fatalf("candidate after second reopen=%q, want consumed", status)
	}
	h3 := newTestHost(t, p, store3, nil, [3]PartyKey{})
	if _, err := h3.Multiply(candidate, x, y); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("reuse after restart error=%v, want already used", err)
	}
}

func TestFileStoreRecoversInterruptedCheckAsBurned(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	path := filepath.Join(t.TempDir(), "state.json")
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")

	store1, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a process dying after the reservation but before finalization.
	err = store1.Update(func(s *State) error {
		s.Triples[candidate.ID] = TripleRecord{Status: StatusChecking, Digest: TripleDigest(f, candidate)}
		s.Triples[check.ID] = TripleRecord{Status: StatusChecking, Digest: TripleDigest(f, check)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store1.Close(); err != nil {
		t.Fatal(err)
	}

	store2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	for _, id := range []string{candidate.ID, check.ID} {
		if status, _ := storeStatus(t, store2, id); status != StatusBurned {
			t.Fatalf("%s after recovery=%q, want burned", id, status)
		}
	}
	h := newTestHost(t, p, store2, nil, [3]PartyKey{})
	if _, err := h.Sacrifice(candidate, check); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("interrupted pair reused: %v", err)
	}
}

func TestFileStoreRecoversInterruptedMultiplyAsConsumed(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	path := filepath.Join(t.TempDir(), "state.json")
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")

	store1, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	// A crash after the ID was reserved for multiplication must not allow reuse.
	err = store1.Update(func(s *State) error {
		s.Triples[candidate.ID] = TripleRecord{Status: StatusMultiplying, Digest: TripleDigest(f, candidate)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store1.Close(); err != nil {
		t.Fatal(err)
	}

	store2, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	if status, _ := storeStatus(t, store2, candidate.ID); status != StatusConsumed {
		t.Fatalf("after recovery=%q, want consumed", status)
	}
	h := newTestHost(t, p, store2, nil, [3]PartyKey{})
	x, y := mustShares(t, f, 3), mustShares(t, f, 9)
	if _, err := h.Multiply(candidate, x, y); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("interrupted multiplication reused: %v", err)
	}
}

type abortStore struct {
	Store
	mu      sync.Mutex
	updates int
	abortAt int
}

func (s *abortStore) Update(fn func(*State) error) error {
	s.mu.Lock()
	s.updates++
	n := s.updates
	s.mu.Unlock()
	if n == s.abortAt {
		return errors.New("injected process death")
	}
	return s.Store.Update(fn)
}

func TestAbortAtCheckFinalizationRecoversAsBurned(t *testing.T) {
	const p = uint32(17)
	path := filepath.Join(t.TempDir(), "state.json")
	base, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")

	// Update #1 is the checking reservation; update #2 is finalization.
	aborting := &abortStore{Store: base, abortAt: 2}
	h := newTestHost(t, p, aborting, nil, [3]PartyKey{})
	res, err := h.Sacrifice(candidate, check)
	if err == nil {
		t.Fatal("expected injected finalization failure")
	}
	if res.Accepted {
		t.Fatal("accepted was reported even though finalization failed")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	for _, id := range []string{candidate.ID, check.ID} {
		if status, _ := storeStatus(t, recovered, id); status != StatusBurned {
			t.Fatalf("%s after interrupted check=%q, want burned", id, status)
		}
	}
}

func TestAbortAfterMultiplyReservationPersistsConsumption(t *testing.T) {
	const p = uint32(17)
	f := mustField(t, p)
	path := filepath.Join(t.TempDir(), "state.json")
	store1, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	bus := newTestBus()
	h := newTestHost(t, p, store1, bus, [3]PartyKey{})
	provider, _ := NewHonestProvider(p, rand.Reader)
	candidate := mustHonestTriple(t, provider, "candidate")
	check := mustHonestTriple(t, provider, "check")
	if res, err := h.Sacrifice(candidate, check); err != nil || !res.Accepted {
		t.Fatalf("setup sacrifice: %+v %v", res, err)
	}
	// The ID has already been durably consumed before any d/e broadcast.
	bus.setFail(func(m SignedMessage) error {
		if m.Label == LabelD {
			return errors.New("injected transport failure")
		}
		return nil
	})
	x, y := mustShares(t, f, 4), mustShares(t, f, 5)
	if mul, err := h.Multiply(candidate, x, y); err == nil || mul.Produced {
		t.Fatalf("Multiply: %+v %v", mul, err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	recovered, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	if status, _ := storeStatus(t, recovered, candidate.ID); status != StatusConsumed {
		t.Fatalf("after abort=%q, want consumed", status)
	}
	h2 := newTestHost(t, p, recovered, nil, [3]PartyKey{})
	if _, err := h2.Multiply(candidate, x, y); !errors.Is(err, ErrTripleAlreadyUsed) {
		t.Fatalf("reuse after abort=%v, want already used", err)
	}
}

func TestStoreUpdateIsAllOrNothing(t *testing.T) {
	f := mustField(t, 17)
	tr := mustTriple(t, f, "t", 1, 2, 2)
	for _, store := range []Store{NewMemoryStore(), mustFileStore(t)} {
		sentinel := errors.New("abort")
		err := store.Update(func(s *State) error {
			s.Triples[tr.ID] = TripleRecord{Status: StatusBurned, Digest: TripleDigest(f, tr)}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("Update error=%v, want sentinel", err)
		}
		if status, _ := storeStatus(t, store, tr.ID); status != StatusUnused {
			t.Fatalf("failed transaction left status=%q, want unused", status)
		}
	}
}

func TestFileStoreConcurrentUpdates(t *testing.T) {
	f := mustField(t, 17)
	store := mustFileStore(t)
	const n = 32
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			id := fmt.Sprintf("id-%d", i)
			errCh <- store.Update(func(s *State) error {
				s.Triples[id] = TripleRecord{Status: StatusBurned, Digest: TripleDigest(f, Triple{ID: id})}
				return nil
			})
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("concurrent update: %v", err)
		}
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("id-%d", i)
		if status, _ := storeStatus(t, store, id); status != StatusBurned {
			t.Fatalf("%s status=%q, want burned", id, status)
		}
	}
}

func storeStatus(t *testing.T, store Store, id string) (TripleStatus, error) {
	t.Helper()
	var status TripleStatus
	err := store.View(func(s *State) error {
		status = recordOf(s, id).Status
		return nil
	})
	return status, err
}

func mustFileStore(t *testing.T) *FileStore {
	t.Helper()
	store, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
